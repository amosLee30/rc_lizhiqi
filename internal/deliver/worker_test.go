package deliver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"rc_lizhiqi/internal/adapter"
	"rc_lizhiqi/internal/config"
	"rc_lizhiqi/internal/id"
	"rc_lizhiqi/internal/model"
	"rc_lizhiqi/internal/mq"
	"rc_lizhiqi/internal/secret"
	"rc_lizhiqi/internal/store"
)

type harness struct {
	st  *store.Store
	w   *Worker
	clk *int64
}

func newHarness(t *testing.T, typ, url string) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var clk int64 = 1000
	st.SetClock(func() int64 { return atomic.LoadInt64(&clk) })

	reg := adapter.NewRegistry()
	reg.Register(adapter.NewBearerAdapter(typ, url, "POST", nil))

	cfg := config.Defaults()
	cfg.Workers = 2
	cfg.Suppliers = map[string]config.SupplierConfig{typ: {Type: typ, Endpoint: url}}
	res, _ := secret.NewFileResolver("")
	w := New(st, reg, res, cfg)
	w.SetClock(func() int64 { return atomic.LoadInt64(&clk) })

	return &harness{st: st, w: w, clk: &clk}
}

func (h *harness) accept(t *testing.T, typ string, maxAttempts int) string {
	t.Helper()
	n := &model.Notification{ID: id.New(), IdempotencyKey: id.New(), SourceSystem: "s", Type: typ, Params: `{"k":"v"}`, MaxAttempts: maxAttempts}
	if _, _, err := h.st.Accept(n); err != nil {
		t.Fatal(err)
	}
	return n.ID
}

func (h *harness) advance(d int64) { atomic.AddInt64(h.clk, d) }

func (h *harness) status(t *testing.T, nid string) model.Status {
	t.Helper()
	n, err := h.st.Get(nid)
	if err != nil || n == nil {
		t.Fatalf("get %s: %v", nid, err)
	}
	return n.Status
}

func TestDeliverSuccessOn2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	h := newHarness(t, "crm", srv.URL)
	nid := h.accept(t, "crm", 3)

	if _, err := h.w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.status(t, nid); got != model.StatusDelivered {
		t.Fatalf("want DELIVERED, got %s", got)
	}
}

func TestNonRetryable4xxGoesDead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(400) }))
	defer srv.Close()
	h := newHarness(t, "crm", srv.URL)
	nid := h.accept(t, "crm", 3)

	if _, err := h.w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.status(t, nid); got != model.StatusDead {
		t.Fatalf("want DEAD on 4xx, got %s", got)
	}
}

func TestRetryableExhaustsToDead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	h := newHarness(t, "crm", srv.URL)
	nid := h.accept(t, "crm", 2)

	// attempt 1: 500 -> retry
	if _, err := h.w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.status(t, nid); got != model.StatusRetrying {
		t.Fatalf("want RETRYING after first 500, got %s", got)
	}
	// advance past backoff so it becomes claimable; attempt 2 exhausts -> dead
	h.advance(3600)
	if _, err := h.w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.status(t, nid); got != model.StatusDead {
		t.Fatalf("want DEAD after exhaustion, got %s", got)
	}
}

func TestReplayDeadThenDeliver(t *testing.T) {
	var code int64 = 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(atomic.LoadInt64(&code)))
	}))
	defer srv.Close()
	h := newHarness(t, "crm", srv.URL)
	nid := h.accept(t, "crm", 1)

	// maxAttempts=1 => first 500 goes straight to DEAD
	if _, err := h.w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.status(t, nid); got != model.StatusDead {
		t.Fatalf("want DEAD, got %s", got)
	}
	// fix the supplier, replay, and it should deliver
	atomic.StoreInt64(&code, 200)
	ok, err := h.st.Replay(nid)
	if err != nil || !ok {
		t.Fatalf("replay: ok=%v err=%v", ok, err)
	}
	if _, err := h.w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.status(t, nid); got != model.StatusDelivered {
		t.Fatalf("want DELIVERED after replay, got %s", got)
	}
}

func TestOutboxPublishesEventsCarryingTrackingID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	h := newHarness(t, "crm", srv.URL)
	nid := h.accept(t, "crm", 3)
	if _, err := h.w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	bus := mq.NewBus()
	ch := bus.Subscribe()
	pub := mq.NewPublisher(h.st, bus, 10*time.Millisecond, 100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pub.Run(ctx)

	deadline := time.After(2 * time.Second)
	sawDelivered := false
	for !sawDelivered {
		select {
		case e := <-ch:
			if e.TrackingID != nid {
				t.Fatalf("event tracking id %q != %q", e.TrackingID, nid)
			}
			if e.Status == string(model.CoarseDelivered) {
				sawDelivered = true
			}
		case <-deadline:
			t.Fatal("did not receive DELIVERED status event via MQ")
		}
	}
}
