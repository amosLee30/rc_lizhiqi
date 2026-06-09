// Package observ serves status queries (coarse by default, detail gated) and
// admin operations (list dead, replay).
package observ

import (
	"rc_lizhiqi/internal/model"
	"rc_lizhiqi/internal/store"
)

// EventView is one status-history entry in a detail response.
type EventView struct {
	From         model.Status `json:"from"`
	To           model.Status `json:"to"`
	Coarse       model.Coarse `json:"coarse"`
	AttemptNo    int          `json:"attempt_no"`
	ResponseCode int          `json:"response_code"`
	Error        string       `json:"error,omitempty"`
	OccurredAt   int64        `json:"occurred_at"`
}

// StatusView is a status query result. Detail fields are only filled when the
// caller is authorized (ops).
type StatusView struct {
	TrackingID string       `json:"tracking_id"`
	Status     model.Coarse `json:"status"`
	// detail-only:
	InternalStatus model.Status `json:"internal_status,omitempty"`
	Attempts       int          `json:"attempts,omitempty"`
	LastError      string       `json:"last_error,omitempty"`
	History        []EventView  `json:"history,omitempty"`
}

// Service answers queries against the store.
type Service struct{ store *store.Store }

// New builds an observability service.
func New(s *store.Store) *Service { return &Service{store: s} }

// Status returns the coarse status for a tracking ID; when detail is true it
// also includes internal status, attempts and the full history.
// Returns nil if the tracking ID is unknown.
func (s *Service) Status(trackingID string, detail bool) (*StatusView, error) {
	n, err := s.store.Get(trackingID)
	if err != nil || n == nil {
		return nil, err
	}
	v := &StatusView{TrackingID: n.ID, Status: model.CoarseOf(n.Status)}
	if !detail {
		return v, nil
	}
	v.InternalStatus = n.Status
	v.Attempts = n.Attempts
	v.LastError = n.LastError
	events, err := s.store.Events(n.ID)
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		v.History = append(v.History, EventView{
			From: e.FromStatus, To: e.ToStatus, Coarse: e.CoarseStatus,
			AttemptNo: e.AttemptNo, ResponseCode: e.ResponseCode, Error: e.Error, OccurredAt: e.OccurredAt,
		})
	}
	return v, nil
}

// ListDead returns up to limit dead notifications for ops triage.
func (s *Service) ListDead(limit int) ([]model.Notification, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.store.ListDead(limit)
}

// Replay re-queues a dead notification.
func (s *Service) Replay(trackingID string) (bool, error) {
	return s.store.Replay(trackingID)
}
