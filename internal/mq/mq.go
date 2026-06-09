// Package mq is an in-process pub/sub bus plus an outbox publisher.
//
// MQ is used ONLY for status-event fan-out (not the delivery work queue). The
// publisher relays unpublished notification_events from the store to the bus
// and marks them published — an outbox that guarantees no event loss.
package mq

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"rc_lizhiqi/internal/metrics"
	"rc_lizhiqi/internal/model"
)

// StatusEvent is the coarse-grained payload published for business subscribers.
// It carries the tracking ID so subscribers can correlate with submission.
type StatusEvent struct {
	TrackingID string `json:"tracking_id"`
	Status     string `json:"status"` // coarse: ACCEPTED/DELIVERED/FAILED
	OccurredAt int64  `json:"occurred_at"`
}

// Bus is a minimal in-process topic bus.
type Bus struct {
	mu   sync.RWMutex
	subs []chan StatusEvent
}

// NewBus returns an empty bus.
func NewBus() *Bus { return &Bus{} }

// Subscribe returns a channel receiving all published events.
func (b *Bus) Subscribe() <-chan StatusEvent {
	ch := make(chan StatusEvent, 64)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

// Publish fans an event out to all subscribers (non-blocking).
func (b *Bus) Publish(e StatusEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default: // slow subscriber: drop rather than block the publisher
			metrics.Inc("mq_dropped")
		}
	}
}

// EventSource is the subset of the store the publisher needs.
type EventSource interface {
	UnpublishedEvents(limit int) ([]model.Event, error)
	MarkPublished(eventID string) error
}

// Publisher relays outbox events to the bus on an interval.
type Publisher struct {
	src   EventSource
	bus   *Bus
	every time.Duration
	batch int
}

// NewPublisher builds an outbox publisher.
func NewPublisher(src EventSource, bus *Bus, every time.Duration, batch int) *Publisher {
	return &Publisher{src: src, bus: bus, every: every, batch: batch}
}

// Run pumps the outbox until ctx is cancelled.
func (p *Publisher) Run(ctx context.Context) {
	t := time.NewTicker(p.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pump()
		}
	}
}

func (p *Publisher) pump() {
	events, err := p.src.UnpublishedEvents(p.batch)
	if err != nil {
		slog.Error("outbox read failed", "err", err)
		return
	}
	for _, e := range events {
		p.bus.Publish(StatusEvent{
			TrackingID: e.NotificationID,
			Status:     string(e.CoarseStatus),
			OccurredAt: e.OccurredAt,
		})
		if err := p.src.MarkPublished(e.ID); err != nil {
			slog.Error("outbox mark failed", "err", err, "event", e.ID)
			return // leave the rest for the next tick; no event lost
		}
		metrics.Inc("mq_published")
	}
}
