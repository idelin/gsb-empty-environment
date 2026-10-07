package quota

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func mustUsage(t *testing.T, m *Manager, user string) Stats {
	t.Helper()
	s, err := m.Usage(user)
	if err != nil {
		t.Fatalf("Usage(%q): %v", user, err)
	}
	return s
}

func mustReserve(t *testing.T, m *Manager, user string, n int64) *Reservation {
	t.Helper()
	r, err := m.Reserve(user, n)
	if err != nil {
		t.Fatalf("Reserve(%q, %d): %v", user, n, err)
	}
	return r
}

func TestExtendReserveShortWriteCommit(t *testing.T) {
	m := New()
	if err := m.SetLimit("alice", 1000); err != nil {
		t.Fatal(err)
	}
	r := mustReserve(t, m, "alice", 400)
	if s := mustUsage(t, m, "alice"); s.Reserved != 400 || s.Used != 0 {
		t.Fatalf("after reserve: %+v", s)
	}
	// Short write: only 150 of 400 reserved bytes materialize.
	if err := m.Commit(r, 150); err != nil {
		t.Fatal(err)
	}
	s := mustUsage(t, m, "alice")
	if s.Used != 150 || s.Reserved != 0 {
		t.Fatalf("after short commit: %+v, want used=150 reserved=0", s)
	}
	if s.Free() != 850 {
		t.Fatalf("free = %d, want 850", s.Free())
	}
}

func TestAbortReleasesFullReservation(t *testing.T) {
	m := New()
	m.SetLimit("alice", 1000)
	r := mustReserve(t, m, "alice", 400)
	if err := m.Abort(r); err != nil {
		t.Fatal(err)
	}
	if s := mustUsage(t, m, "alice"); s.Used != 0 || s.Reserved != 0 {
		t.Fatalf("after abort: %+v", s)
	}
	// Settling twice is an error and must not double-release.
	if err := m.Abort(r); err != ErrReservationClosed {
		t.Fatalf("second abort: %v", err)
	}
	if s := mustUsage(t, m, "alice"); s.Reserved != 0 {
		t.Fatalf("after double abort: %+v", s)
	}
}

func TestReserveBeyondLimitFailsWithoutLeak(t *testing.T) {
	m := New()
	m.SetLimit("alice", 100)
	mustReserve(t, m, "alice", 100)
	if _, err := m.Reserve("alice", 1); err != ErrQuotaExceeded {
		t.Fatalf("reserve over limit: %v", err)
	}
	if s := mustUsage(t, m, "alice"); s.Reserved != 100 {
		t.Fatalf("failed reserve leaked: %+v", s)
	}
	if _, err := m.Reserve("alice", 0); err != ErrInvalidAmount {
		t.Fatalf("zero reserve: %v", err)
	}
	if _, err := m.Reserve("bob", 1); err != ErrUnknownUser {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestCommitFailuresLeaveReservationIntact(t *testing.T) {
	m := New()
	m.SetLimit("alice", 1000)
	r := mustReserve(t, m, "alice", 100)

	// Committing more than reserved fails; reservation stays open.
	if err := m.Commit(r, 101); err != ErrCommitExceedsReservation {
		t.Fatalf("oversized commit: %v", err)
	}
	if s := mustUsage(t, m, "alice"); s.Reserved != 100 || s.Used != 0 {
		t.Fatalf("oversized commit changed state: %+v", s)
	}

	// Stale version fails; reservation stays open.
	stale := *r
	stale.version++
	if err := m.Commit(&stale, 50); err != ErrVersionMismatch {
		t.Fatalf("stale commit: %v", err)
	}
	if s := mustUsage(t, m, "alice"); s.Reserved != 100 || s.Used != 0 {
		t.Fatalf("stale commit changed state: %+v", s)
	}

	// Negative commit rejected.
	if err := m.Commit(r, -1); err != ErrInvalidAmount {
		t.Fatalf("negative commit: %v", err)
	}

	// Real commit still works; a second settle is rejected without effect.
	if err := m.Commit(r, 60); err != nil {
		t.Fatal(err)
	}
	if err := m.Commit(r, 10); err != ErrReservationClosed {
		t.Fatalf("double commit: %v", err)
	}
	if s := mustUsage(t, m, "alice"); s.Used != 60 || s.Reserved != 0 {
		t.Fatalf("after double commit: %+v", s)
	}
}

func TestTruncateReleasesUsage(t *testing.T) {
	m := New()
	m.SetLimit("alice", 1000)
	m.Commit(mustReserve(t, m, "alice", 300), 300)
	if err := m.Release("alice", 120); err != nil {
		t.Fatal(err)
	}
	if s := mustUsage(t, m, "alice"); s.Used != 180 {
		t.Fatalf("after truncate: %+v", s)
	}
	// Releasing more than used fails without going negative.
	if err := m.Release("alice", 181); err != ErrInsufficientUsage {
		t.Fatalf("over-release: %v", err)
	}
	if s := mustUsage(t, m, "alice"); s.Used != 180 {
		t.Fatalf("failed release changed state: %+v", s)
	}
}

func TestTransferValidatesTargetQuota(t *testing.T) {
	m := New()
	m.SetLimit("alice", 100)
	m.SetLimit("bob", 50)
	m.Commit(mustReserve(t, m, "alice", 80), 80)

	// Target cannot absorb 60 bytes: both sides unchanged.
	if err := m.Transfer("alice", "bob", 60); err != ErrQuotaExceeded {
		t.Fatalf("oversized transfer: %v", err)
	}
	if s := mustUsage(t, m, "alice"); s.Used != 80 {
		t.Fatalf("failed transfer debited source: %+v", s)
	}
	if s := mustUsage(t, m, "bob"); s.Used != 0 {
		t.Fatalf("failed transfer credited target: %+v", s)
	}

	// Source cannot spend bytes it does not have.
	if err := m.Transfer("alice", "bob", 90); err != ErrInsufficientUsage {
		t.Fatalf("overdraw transfer: %v", err)
	}

	// Target's own reservations count against its quota.
	r := mustReserve(t, m, "bob", 20)
	if err := m.Transfer("alice", "bob", 40); err != ErrQuotaExceeded {
		t.Fatalf("transfer ignoring target reservation: %v", err)
	}
	m.Abort(r)

	if err := m.Transfer("alice", "bob", 40); err != nil {
		t.Fatal(err)
	}
	if s := mustUsage(t, m, "alice"); s.Used != 40 {
		t.Fatalf("source after transfer: %+v", s)
	}
	if s := mustUsage(t, m, "bob"); s.Used != 40 {
		t.Fatalf("target after transfer: %+v", s)
	}
}

// TestByteConservation runs a random op sequence against a model and
// verifies after every step that bytes are neither created nor destroyed
// and no account exceeds its limit.
func TestByteConservation(t *testing.T) {
	m := New()
	limits := map[string]int64{"a": 500, "b": 800, "c": 1000}
	users := []string{"a", "b", "c"}
	for u, l := range limits {
		m.SetLimit(u, l)
	}

	type model struct{ used, reserved int64 }
	models := map[string]*model{}
	for u := range limits {
		models[u] = &model{}
	}
	open := map[*Reservation]int64{}
	rng := rand.New(rand.NewSource(42))

	check := func(step int) {
		var totalUsed, totalReserved int64
		for _, u := range users {
			s, err := m.Usage(u)
			if err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
			md := models[u]
			if s.Used != md.used || s.Reserved != md.reserved {
				t.Fatalf("step %d: %s manager=%+v model=%+v", step, u, s, md)
			}
			if s.Used < 0 || s.Reserved < 0 || s.Used+s.Reserved > s.Limit {
				t.Fatalf("step %d: %s invariant broken: %+v", step, u, s)
			}
			totalUsed += s.Used
			totalReserved += s.Reserved
		}
		var openSum int64
		for _, n := range open {
			openSum += n
		}
		if openSum != totalReserved {
			t.Fatalf("step %d: reserved=%d but outstanding reservations sum to %d",
				step, totalReserved, openSum)
		}
	}

	for step := 0; step < 20000; step++ {
		u := users[rng.Intn(len(users))]
		md := models[u]
		switch rng.Intn(5) {
		case 0: // extend: reserve
			n := int64(1 + rng.Intn(200))
			r, err := m.Reserve(u, n)
			if err == nil {
				md.reserved += n
				open[r] = n
			} else if err != ErrQuotaExceeded {
				t.Fatalf("step %d: reserve: %v", step, err)
			}
		case 1: // commit (possibly short write)
			if len(open) == 0 {
				continue
			}
			var r *Reservation
			for r = range open {
			}
			n := open[r]
			actual := int64(rng.Int63n(n + 1))
			if err := m.Commit(r, actual); err != nil {
				t.Fatalf("step %d: commit: %v", step, err)
			}
			models[r.user].reserved -= n
			models[r.user].used += actual
			delete(open, r)
		case 2: // abort
			if len(open) == 0 {
				continue
			}
			var r *Reservation
			for r = range open {
			}
			if err := m.Abort(r); err != nil {
				t.Fatalf("step %d: abort: %v", step, err)
			}
			models[r.user].reserved -= open[r]
			delete(open, r)
		case 3: // truncate: release
			n := int64(1 + rng.Intn(200))
			err := m.Release(u, n)
			if n <= md.used {
				if err != nil {
					t.Fatalf("step %d: release: %v", step, err)
				}
				md.used -= n
			} else if err != ErrInsufficientUsage {
				t.Fatalf("step %d: release should fail: %v", step, err)
			}
		case 4: // cross-user transfer
			v := users[rng.Intn(len(users))]
			n := int64(1 + rng.Intn(200))
			err := m.Transfer(u, v, n)
			fits := n <= md.used && (u == v || models[v].used+models[v].reserved+n <= limits[v])
			if fits {
				if err != nil {
					t.Fatalf("step %d: transfer %s->%s %d: %v", step, u, v, n, err)
				}
				md.used -= n
				models[v].used += n
			} else if err == nil {
				t.Fatalf("step %d: transfer %s->%s %d should have failed", step, u, v, n)
			}
		}
		check(step)
	}
}

// TestConcurrentCommitNoLeak hammers the manager with concurrent
// reserve/commit/abort cycles, including commits that must fail (double
// commit, stale version, oversized commit), and verifies no quota leaks.
func TestConcurrentCommitNoLeak(t *testing.T) {
	m := New()
	const users = 4
	const workers = 8
	const iters = 2000
	for i := 0; i < users; i++ {
		m.SetLimit(string(rune('a'+i)), 1<<20)
	}

	var committed int64 // total bytes successfully committed, for conservation
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			user := string(rune('a' + w%users))
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < iters; i++ {
				n := int64(1 + rng.Intn(100))
				r, err := m.Reserve(user, n)
				if err == ErrQuotaExceeded {
					continue
				}
				if err != nil {
					t.Errorf("reserve: %v", err)
					return
				}
				switch rng.Intn(5) {
				case 0: // short write
					actual := rng.Int63n(n + 1)
					if err := m.Commit(r, actual); err != nil {
						t.Errorf("commit: %v", err)
						return
					}
					atomic.AddInt64(&committed, actual)
				case 1: // write fails entirely
					if err := m.Abort(r); err != nil {
						t.Errorf("abort: %v", err)
						return
					}
				case 2: // oversized commit must fail, then abort
					if err := m.Commit(r, n+1); err != ErrCommitExceedsReservation {
						t.Errorf("oversized commit: %v", err)
						return
					}
					if err := m.Abort(r); err != nil {
						t.Errorf("abort after failed commit: %v", err)
						return
					}
				case 3: // full commit, then a losing double commit
					if err := m.Commit(r, n); err != nil {
						t.Errorf("commit: %v", err)
						return
					}
					atomic.AddInt64(&committed, n)
					if err := m.Commit(r, n); err != ErrReservationClosed {
						t.Errorf("double commit: %v", err)
						return
					}
				case 4: // stale version must fail, then real commit
					stale := *r
					stale.version++
					if err := m.Commit(&stale, n); err != ErrVersionMismatch {
						t.Errorf("stale commit: %v", err)
						return
					}
					if err := m.Commit(r, n); err != nil {
						t.Errorf("commit after stale: %v", err)
						return
					}
					atomic.AddInt64(&committed, n)
				}
			}
		}(w)
	}
	wg.Wait()

	var totalUsed int64
	for i := 0; i < users; i++ {
		s := mustUsage(t, m, string(rune('a'+i)))
		if s.Reserved != 0 {
			t.Fatalf("user %c: %d reserved bytes leaked", 'a'+i, s.Reserved)
		}
		totalUsed += s.Used
	}
	if totalUsed != atomic.LoadInt64(&committed) {
		t.Fatalf("byte conservation violated: used=%d committed=%d", totalUsed, committed)
	}
}

// TestConcurrentTransferConservation moves bytes back and forth between
// two accounts concurrently; the pair's total usage must stay constant
// and neither account may go negative or over limit.
func TestConcurrentTransferConservation(t *testing.T) {
	m := New()
	m.SetLimit("alice", 10000)
	m.SetLimit("bob", 10000)
	m.Commit(mustReserve(t, m, "alice", 10000), 10000)

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < 5000; i++ {
				n := int64(1 + rng.Intn(200))
				var err error
				if rng.Intn(2) == 0 {
					err = m.Transfer("alice", "bob", n)
				} else {
					err = m.Transfer("bob", "alice", n)
				}
				if err != nil && err != ErrQuotaExceeded && err != ErrInsufficientUsage {
					t.Errorf("transfer: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	a := mustUsage(t, m, "alice")
	b := mustUsage(t, m, "bob")
	if a.Used+b.Used != 10000 {
		t.Fatalf("conservation violated: alice=%d bob=%d", a.Used, b.Used)
	}
	if a.Used < 0 || a.Used > a.Limit || b.Used < 0 || b.Used > b.Limit {
		t.Fatalf("limits violated: alice=%+v bob=%+v", a, b)
	}
}
