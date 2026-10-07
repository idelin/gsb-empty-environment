package ledger

import (
	"fmt"
	"sync"
	"testing"
)

func mustAccount(t *testing.T, s *Store, id string) *Account {
	t.Helper()
	a, err := s.Account(id)
	if err != nil {
		t.Fatalf("get account %s: %v", id, err)
	}
	return a
}

func setup(t *testing.T, id, currency string, amount Money) *Store {
	t.Helper()
	s := NewStore()
	if err := s.CreateAccount(id); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if _, err := s.Deposit("dep-"+id, id, currency, amount); err != nil {
		t.Fatalf("deposit: %v", err)
	}
	return s
}

func TestFreezeSeparatesBuckets(t *testing.T) {
	s := setup(t, "alice", "USD", 1000)
	rec, err := s.Freeze("req-f1", "alice", "USD", 400)
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	a := mustAccount(t, s, "alice")
	if a.Available["USD"] != 600 || a.Frozen["USD"] != 400 {
		t.Fatalf("buckets not separated: avail=%d frozen=%d", a.Available["USD"], a.Frozen["USD"])
	}
	f, err := s.GetFreeze("alice", rec.FreezeID)
	if err != nil {
		t.Fatalf("get freeze: %v", err)
	}
	if f.RequestID != "req-f1" || f.Currency != "USD" || f.Remaining != 400 || f.Status != FreezeOpen {
		t.Fatalf("freeze not bound to request/currency: %v", f)
	}
	if err := s.CheckConservation(); err != nil {
		t.Fatal(err)
	}
}

func TestPartialSettleThenOnlyRemainderReleasable(t *testing.T) {
	s := setup(t, "alice", "USD", 1000)
	fr, _ := s.Freeze("req-f1", "alice", "USD", 400)

	if _, err := s.Settle("req-s1", "alice", fr.FreezeID, 150); err != nil {
		t.Fatalf("partial settle: %v", err)
	}
	// 250 remains; releasing more must fail.
	if _, err := s.Release("req-r-over", "alice", fr.FreezeID, 251); err != ErrInsufficientFrozen {
		t.Fatalf("expected ErrInsufficientFrozen, got %v", err)
	}
	// Settling more than the remainder must fail too.
	if _, err := s.Settle("req-s-over", "alice", fr.FreezeID, 251); err != ErrInsufficientFrozen {
		t.Fatalf("expected ErrInsufficientFrozen, got %v", err)
	}
	if _, err := s.Release("req-r1", "alice", fr.FreezeID, 250); err != nil {
		t.Fatalf("release remainder: %v", err)
	}
	a := mustAccount(t, s, "alice")
	if a.Available["USD"] != 850 || a.Frozen["USD"] != 0 {
		t.Fatalf("bad buckets: avail=%d frozen=%d", a.Available["USD"], a.Frozen["USD"])
	}
	if s.Settled("USD") != 150 {
		t.Fatalf("settled ledger = %d, want 150", s.Settled("USD"))
	}
	// Freeze is closed: any further settle/release is rejected.
	if _, err := s.Settle("req-s2", "alice", fr.FreezeID, 1); err != ErrFreezeClosed {
		t.Fatalf("expected ErrFreezeClosed, got %v", err)
	}
	if _, err := s.Release("req-r2", "alice", fr.FreezeID, 1); err != ErrFreezeClosed {
		t.Fatalf("expected ErrFreezeClosed, got %v", err)
	}
	if err := s.CheckConservation(); err != nil {
		t.Fatal(err)
	}
}

func TestFaultReplayReturnsDuplicateReceiptWithoutReapplying(t *testing.T) {
	s := setup(t, "alice", "USD", 1000)
	r1, err := s.Freeze("req-f1", "alice", "USD", 400)
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	// Replay the same request id (client retry after a fault).
	r2, err := s.Freeze("req-f1", "alice", "USD", 400)
	if err != nil {
		t.Fatalf("replay freeze: %v", err)
	}
	if r1 != r2 {
		t.Fatalf("receipts differ on replay: %+v vs %+v", r1, r2)
	}
	a := mustAccount(t, s, "alice")
	if a.Frozen["USD"] != 400 {
		t.Fatalf("freeze applied twice: frozen=%d", a.Frozen["USD"])
	}

	s1, err := s.Settle("req-s1", "alice", r1.FreezeID, 100)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	s2, err := s.Settle("req-s1", "alice", r1.FreezeID, 100)
	if err != nil {
		t.Fatalf("replay settle: %v", err)
	}
	if s1 != s2 {
		t.Fatalf("settle receipts differ on replay: %+v vs %+v", s1, s2)
	}
	if s.Settled("USD") != 100 {
		t.Fatalf("settle applied twice: settled=%d", s.Settled("USD"))
	}
	if err := s.CheckConservation(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentDuplicateRequestsShareOneEffect(t *testing.T) {
	s := setup(t, "alice", "USD", 1000)
	const n = 32
	recs := make([]Receipt, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			recs[i], errs[i] = s.Freeze("req-dup", "alice", "USD", 400)
		}(i)
	}
	wg.Wait()
	for i := range recs {
		if errs[i] != nil {
			t.Fatalf("concurrent freeze %d: %v", i, errs[i])
		}
		if recs[i] != recs[0] {
			t.Fatalf("receipt %d differs", i)
		}
	}
	if a := mustAccount(t, s, "alice"); a.Frozen["USD"] != 400 {
		t.Fatalf("duplicate freeze applied: frozen=%d", a.Frozen["USD"])
	}
}

// Concurrent settle and release commits against the same freeze are
// adjudicated by version: every attempt either commits or retries on a
// fresh snapshot, and the totals must add up exactly.
func TestConcurrentSettleReleaseAdjudicatedByVersion(t *testing.T) {
	s := setup(t, "alice", "USD", 10000)
	fr, err := s.Freeze("req-f1", "alice", "USD", 1000)
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	const workers = 8
	const perWorker = 50 // each settles 50 and releases 50, in 1-unit steps
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(2)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				req := fmt.Sprintf("s-%d-%d", w, i)
				if _, err := s.Settle(req, "alice", fr.FreezeID, 1); err != nil {
					t.Errorf("settle %s: %v", req, err)
					return
				}
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				req := fmt.Sprintf("r-%d-%d", w, i)
				if _, err := s.Release(req, "alice", fr.FreezeID, 1); err != nil {
					t.Errorf("release %s: %v", req, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	total := Money(workers * perWorker)
	if got := s.Settled("USD"); got != total {
		t.Fatalf("settled=%d, want %d", got, total)
	}
	a := mustAccount(t, s, "alice")
	if a.Frozen["USD"] != 1000-2*total {
		t.Fatalf("frozen=%d, want %d", a.Frozen["USD"], 1000-2*total)
	}
	if a.Available["USD"] != 9000+total {
		t.Fatalf("available=%d, want %d", a.Available["USD"], 9000+total)
	}
	f, _ := s.GetFreeze("alice", fr.FreezeID)
	if f.Remaining != 1000-2*total {
		t.Fatalf("remaining=%d, want %d", f.Remaining, 1000-2*total)
	}
	if err := s.CheckConservation(); err != nil {
		t.Fatal(err)
	}
}

func TestBatchIsAtomicAcrossAccounts(t *testing.T) {
	s := NewStore()
	for _, id := range []string{"a", "b", "c"} {
		if err := s.CreateAccount(id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Deposit("dep-"+id, id, "USD", 500); err != nil {
			t.Fatal(err)
		}
	}
	// Failing batch: c has insufficient funds for the last leg.
	_, err := s.Batch("req-bad", []BatchOp{
		{AccountID: "a", Currency: "USD", Amount: -200},
		{AccountID: "b", Currency: "USD", Amount: 200},
		{AccountID: "c", Currency: "USD", Amount: -9999},
	})
	if err != ErrInsufficientFunds {
		t.Fatalf("expected ErrInsufficientFunds, got %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if a := mustAccount(t, s, id); a.Available["USD"] != 500 {
			t.Fatalf("batch not rolled back for %s: avail=%d", id, a.Available["USD"])
		}
	}
	// Successful batch: multi-leg transfer commits together.
	if _, err := s.Batch("req-ok", []BatchOp{
		{AccountID: "a", Currency: "USD", Amount: -200},
		{AccountID: "b", Currency: "USD", Amount: 120},
		{AccountID: "c", Currency: "USD", Amount: 80},
	}); err != nil {
		t.Fatalf("batch: %v", err)
	}
	want := map[string]Money{"a": 300, "b": 620, "c": 580}
	for id, w := range want {
		if a := mustAccount(t, s, id); a.Available["USD"] != w {
			t.Fatalf("%s avail=%d, want %d", id, a.Available["USD"], w)
		}
	}
	// Replaying the batch request id must not move money again.
	if _, err := s.Batch("req-ok", []BatchOp{
		{AccountID: "a", Currency: "USD", Amount: -200},
		{AccountID: "b", Currency: "USD", Amount: 120},
		{AccountID: "c", Currency: "USD", Amount: 80},
	}); err != nil {
		t.Fatalf("replay batch: %v", err)
	}
	for id, w := range want {
		if a := mustAccount(t, s, id); a.Available["USD"] != w {
			t.Fatalf("replay moved funds for %s: avail=%d, want %d", id, a.Available["USD"], w)
		}
	}
	if err := s.CheckConservation(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentBatchesPreserveConservation(t *testing.T) {
	s := NewStore()
	ids := []string{"a", "b", "c", "d"}
	for _, id := range ids {
		if err := s.CreateAccount(id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Deposit("dep-"+id, id, "USD", 1000); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			from := ids[i%len(ids)]
			to := ids[(i+1)%len(ids)]
			_, err := s.Batch(fmt.Sprintf("req-b-%d", i), []BatchOp{
				{AccountID: from, Currency: "USD", Amount: -10},
				{AccountID: to, Currency: "USD", Amount: 10},
			})
			if err != nil {
				t.Errorf("batch %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if err := s.CheckConservation(); err != nil {
		t.Fatal(err)
	}
	sum := Money(0)
	for _, id := range ids {
		sum += mustAccount(t, s, id).Available["USD"]
	}
	if sum != 4000 {
		t.Fatalf("total available=%d, want 4000", sum)
	}
}

func TestVersionConflictOnStaleCommit(t *testing.T) {
	s := setup(t, "alice", "USD", 100)
	st, ver, err := s.snapshot("alice")
	if err != nil {
		t.Fatal(err)
	}
	// A concurrent mutation bumps the version.
	if _, err := s.Deposit("dep-2", "alice", "USD", 50); err != nil {
		t.Fatal(err)
	}
	// Committing the stale snapshot must be rejected by adjudication.
	if _, err := s.commit(map[string]*accountState{"alice": st}, map[string]uint64{"alice": ver}, newEffects()); err != ErrVersionConflict {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}
}
