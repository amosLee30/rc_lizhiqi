package store

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"rc_lizhiqi/internal/id"
	"rc_lizhiqi/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mkNotif() *model.Notification {
	return &model.Notification{ID: id.New(), IdempotencyKey: id.New(), SourceSystem: "billing", Type: "crm", Params: "{}", MaxAttempts: 3}
}

func TestAcceptIdempotentReturnsSameID(t *testing.T) {
	s := newTestStore(t)
	n := mkNotif()
	gotID, created, err := s.Accept(n)
	if err != nil || !created {
		t.Fatalf("first accept: id=%s created=%v err=%v", gotID, created, err)
	}
	// Resubmit with the same (source, key) but a new ULID.
	n2 := mkNotif()
	n2.SourceSystem, n2.IdempotencyKey = n.SourceSystem, n.IdempotencyKey
	gotID2, created2, err := s.Accept(n2)
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("expected duplicate, got created")
	}
	if gotID2 != gotID {
		t.Fatalf("idempotent resubmit must return same id: %s != %s", gotID2, gotID)
	}
}

func TestClaimNoDoublePickupUnderConcurrency(t *testing.T) {
	s := newTestStore(t)
	const k = 50
	for i := 0; i < k; i++ {
		if _, _, err := s.Accept(mkNotif()); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				batch, err := s.ClaimBatch("w", 30, 7)
				if err != nil {
					t.Error(err)
					return
				}
				if len(batch) == 0 {
					return
				}
				mu.Lock()
				for _, n := range batch {
					seen[n.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != k {
		t.Fatalf("expected %d claimed, got %d", k, len(seen))
	}
	for nid, c := range seen {
		if c != 1 {
			t.Fatalf("notification %s claimed %d times (double pickup)", nid, c)
		}
	}
}

func TestExpiredLeaseIsReclaimed(t *testing.T) {
	s := newTestStore(t)
	var clk int64 = 1000
	s.SetClock(func() int64 { return atomic.LoadInt64(&clk) })

	if _, _, err := s.Accept(mkNotif()); err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimBatch("w1", 30, 10) // lease_until = 1030
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim: %v len=%d", err, len(first))
	}
	// Before lease expiry: not claimable.
	if again, _ := s.ClaimBatch("w2", 30, 10); len(again) != 0 {
		t.Fatal("claimed an actively-leased notification")
	}
	// Advance past the lease: the claim query (reaper folded in) reclaims it.
	atomic.StoreInt64(&clk, 1100)
	reclaimed, err := s.ClaimBatch("w2", 30, 10)
	if err != nil || len(reclaimed) != 1 {
		t.Fatalf("expected reclaim after lease expiry, got %d (err=%v)", len(reclaimed), err)
	}
	if reclaimed[0].Attempts != 2 {
		t.Fatalf("attempts should increment on each claim, got %d", reclaimed[0].Attempts)
	}
}

func TestArchiveTerminalMovesOutOfHotTable(t *testing.T) {
	s := newTestStore(t)
	n := mkNotif()
	if _, _, err := s.Accept(n); err != nil {
		t.Fatal(err)
	}
	claimed, _ := s.ClaimBatch("w", 30, 10)
	if err := s.MarkDelivered(&claimed[0], 200); err != nil {
		t.Fatal(err)
	}
	moved, err := s.ArchiveTerminal(100)
	if err != nil || moved != 1 {
		t.Fatalf("archive: moved=%d err=%v", moved, err)
	}
	// Hot table no longer yields it to the claim query.
	if c, _ := s.ClaimBatch("w", 30, 10); len(c) != 0 {
		t.Fatal("terminal row still in hot table")
	}
	// But it's still queryable from the archive.
	got, err := s.Get(n.ID)
	if err != nil || got == nil || got.Status != model.StatusDelivered {
		t.Fatalf("archived row not queryable: %+v err=%v", got, err)
	}
}
