// Package deliver is the reliable-delivery worker: it claims leased work,
// asks the supplier adapter to build+sign the request, sends it over HTTP,
// and classifies the result into delivered / retry / dead. Transport, retry,
// lease and 2xx-judgement live here (not in adapters).
package deliver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"rc_lizhiqi/internal/adapter"
	"rc_lizhiqi/internal/config"
	"rc_lizhiqi/internal/metrics"
	"rc_lizhiqi/internal/model"
	"rc_lizhiqi/internal/secret"
	"rc_lizhiqi/internal/store"
)

// Worker runs the delivery loop.
type Worker struct {
	store     *store.Store
	registry  *adapter.Registry
	resolver  secret.Resolver
	suppliers map[string]config.SupplierConfig
	breaker   *breaker
	client    *http.Client

	owner    string
	leaseSec int
	batch    int
	poll     time.Duration
	workers  int
	now      func() int64
}

// SetClock overrides the clock used for retry scheduling (tests only).
func (w *Worker) SetClock(now func() int64) { w.now = now }

// New builds a delivery worker from app config.
func New(s *store.Store, r *adapter.Registry, res secret.Resolver, cfg config.App) *Worker {
	return &Worker{
		store:     s,
		registry:  r,
		resolver:  res,
		suppliers: cfg.Suppliers,
		breaker:   newBreaker(cfg.BreakerFailures, time.Duration(cfg.BreakerCoolMS)*time.Millisecond),
		client:    &http.Client{Timeout: time.Duration(cfg.HTTPTimeoutMS) * time.Millisecond},
		owner:     fmt.Sprintf("w-%d", time.Now().UnixNano()),
		leaseSec:  cfg.LeaseSeconds,
		batch:     cfg.ClaimBatch,
		poll:      time.Duration(cfg.PollIntervalMS) * time.Millisecond,
		workers:   cfg.Workers,
		now:       func() int64 { return time.Now().Unix() },
	}
}

// Run polls for work until ctx is cancelled. Also runs the archiver loop.
func (w *Worker) Run(ctx context.Context) {
	go w.archiveLoop(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := w.store.ClaimBatch(w.owner, w.leaseSec, w.batch)
		if err != nil {
			slog.Error("claim failed", "err", err)
		}
		if len(n) > 0 {
			w.processBatch(ctx, n)
		}
		w.sleepWithJitter(ctx)
	}
}

// Tick claims and processes a single batch (used by tests).
func (w *Worker) Tick(ctx context.Context) (int, error) {
	n, err := w.store.ClaimBatch(w.owner, w.leaseSec, w.batch)
	if err != nil {
		return 0, err
	}
	w.processBatch(ctx, n)
	return len(n), nil
}

func (w *Worker) processBatch(ctx context.Context, batch []model.Notification) {
	sem := make(chan struct{}, max(1, w.workers))
	var wg sync.WaitGroup
	for i := range batch {
		n := batch[i]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			w.process(ctx, &n)
		}()
	}
	wg.Wait()
}

func (w *Worker) process(ctx context.Context, n *model.Notification) {
	metrics.Inc("delivery_attempt")

	// Isolate sick suppliers: an open breaker skips the HTTP call but still lets
	// attempts progress toward DEAD (long-term-unavailable handling).
	if w.breaker.Open(n.Type) {
		metrics.Inc("breaker_skip")
		w.fail(n, 0, "circuit open for supplier")
		return
	}

	ad, ok := w.registry.Get(n.Type)
	if !ok {
		w.store.MarkDead(n, "no adapter for type", 0)
		return
	}
	sec, err := w.resolveSecret(n.Type)
	if err != nil {
		w.fail(n, 0, "secret resolve: "+err.Error())
		return
	}
	params, err := decodeParams(n.Params)
	if err != nil {
		w.store.MarkDead(n, "bad params: "+err.Error(), 0)
		return
	}
	req, err := ad.BuildRequest(params, sec)
	if err != nil {
		w.fail(n, 0, "build request: "+err.Error())
		return
	}

	status, body, err := w.send(ctx, req)
	ad.HandleResponse(status, body)

	switch {
	case err == nil && status >= 200 && status < 300:
		w.breaker.Success(n.Type)
		metrics.Inc("delivered")
		_ = w.store.MarkDelivered(n, status)
	case isRetryable(status, err):
		w.breaker.Failure(n.Type)
		w.fail(n, status, errString(err, status))
	default: // non-retryable (e.g., 4xx config/auth error)
		metrics.Inc("dead_nonretryable")
		_ = w.store.MarkDead(n, errString(err, status), status)
	}
}

// fail schedules a retry with backoff, or moves to DEAD if attempts are exhausted.
func (w *Worker) fail(n *model.Notification, code int, msg string) {
	if n.Attempts >= n.MaxAttempts {
		metrics.Inc("dead_exhausted")
		slog.Warn("notification dead", "id", n.ID, "type", n.Type, "attempts", n.Attempts, "err", msg)
		_ = w.store.MarkDead(n, msg, code)
		return
	}
	metrics.Inc("retry")
	next := w.now() + backoffSeconds(n.Attempts)
	_ = w.store.MarkRetry(n, next, msg, code)
}

func (w *Worker) send(ctx context.Context, r *adapter.Request) (int, []byte, error) {
	method := r.Method
	if method == "" {
		method = http.MethodPost
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, r.URL, bytes.NewReader(r.Body))
	if err != nil {
		return 0, nil, err
	}
	for k, v := range r.Headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := w.client.Do(httpReq)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, body, nil
}

func (w *Worker) resolveSecret(typ string) (string, error) {
	c, ok := w.suppliers[typ]
	if !ok || c.SecretRef == "" {
		return "", nil
	}
	return w.resolver.Resolve(c.SecretRef)
}

func (w *Worker) archiveLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if nn, err := w.store.ArchiveTerminal(200); err == nil && nn > 0 {
				metrics.Add("archived", int64(nn))
			}
		}
	}
}

func (w *Worker) sleepWithJitter(ctx context.Context) {
	jitter := time.Duration(rand.Int63n(int64(w.poll/2) + 1))
	select {
	case <-ctx.Done():
	case <-time.After(w.poll + jitter):
	}
}

// isRetryable classifies failures: transport errors, timeouts, 429 and 5xx are
// retryable; deterministic 4xx are not.
func isRetryable(status int, err error) bool {
	if err != nil {
		return true // transport error / timeout
	}
	return status == http.StatusTooManyRequests || (status >= 500 && status <= 599)
}

// backoffSeconds returns exponential backoff with jitter, capped at 5 minutes.
func backoffSeconds(attempts int) int64 {
	base := 2.0
	d := math.Min(300, base*math.Pow(2, float64(attempts-1)))
	jitter := rand.Float64() * d * 0.2
	return int64(d + jitter)
}

func errString(err error, status int) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("http %d", status)
}

func decodeParams(raw string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}
