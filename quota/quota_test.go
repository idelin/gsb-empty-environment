package quota

import (
	"sync"
	"testing"
)

func mustReserve(t *testing.T, m *Manager, user string, size int64) *Reservation {
	t.Helper()
	r, err := m.Reserve(user, size)
	if err != nil {
		t.Fatalf("Reserve(%s, %d) failed: %v", user, size, err)
	}
	return r
}

func checkStats(t *testing.T, m *Manager, user string, used, reserved int64) {
	t.Helper()
	u, r, _ := m.Stats(user)
	if u != used || r != reserved {
		t.Fatalf("Stats(%s) = used %d, reserved %d; want used %d, reserved %d",
			user, u, r, used, reserved)
	}
}

// Short write: commit fewer bytes than reserved, difference is released.
func TestShortWriteReleasesDifference(t *testing.T) {
	m := NewManager()
	m.SetLimit("u", 1000)

	r := mustReserve(t, m, "u", 100)
	checkStats(t, m, "u", 0, 100)

	if err := r.Commit(40); err != nil {
		t.Fatalf("Commit(40) failed: %v", err)
	}
	checkStats(t, m, "u", 40, 0)

	// The released 60 bytes must be available again.
	r2 := mustReserve(t, m, "u", 960)
	if err := r2.Abort(); err != nil {
		t.Fatalf("Abort failed: %v", err)
	}
	checkStats(t, m, "u", 40, 0)
}

// Full write commits everything reserved.
func TestFullWriteCommit(t *testing.T) {
	m := NewManager()
	m.SetLimit("u", 100)
	r := mustReserve(t, m, "u", 100)
	if err := r.Commit(100); err != nil {
		t.Fatalf("Commit(100) failed: %v", err)
	}
	checkStats(t, m, "u", 100, 0)
	if _, err := m.Reserve("u", 1); err != ErrQuotaExceeded {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
}

// Abort releases the whole reservation.
func TestAbortReleasesReservation(t *testing.T) {
	m := NewManager()
	m.SetLimit("u", 100)
	r := mustReserve(t, m, "u", 60)
	if err := r.Abort(); err != nil {
		t.Fatalf("Abort failed: %v", err)
	}
	checkStats(t, m, "u", 0, 0)
}

// A reservation counts against the limit while outstanding.
func TestReservationBlocksOthers(t *testing.T) {
	m := NewManager()
	m.SetLimit("u", 100)
	mustReserve(t, m, "u", 80)
	if _, err := m.Reserve("u", 21); err != ErrQuotaExceeded {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	if _, err := m.Reserve("u", 20); err != nil {
		t.Fatalf("Reserve(20) failed: %v", err)
	}
	checkStats(t, m, "u", 0, 100)
}

// Failed operations must not change state or bump the version.
func TestFailedOpsDoNotLeak(t *testing.T) {
	m := NewManager()
	m.SetLimit("u", 100)
	r := mustReserve(t, m, "u", 100)
	if err := r.Commit(60); err != nil {
		t.Fatal(err)
	}
	v := m.Version()

	cases := []error{
		func() error { _, err := m.Reserve("u", 41); return err }(),
		func() error { _, err := m.Reserve("u", 0); return err }(),
		func() error { _, err := m.Reserve("u", -5); return err }(),
		func() error { return m.Release("u", 61) }(),
		func() error { return m.Release("u", 0) }(),
		func() error { return m.Transfer("u", "v", 61) }(),
		func() error { return m.Transfer("u", "v", 0) }(),
		func() error { return r.Commit(10) }(), // already settled
		func() error { return r.Abort() }(),    // already settled
	}
	for i, err := range cases {
		if err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
	}
	checkStats(t, m, "u", 60, 0)
	checkStats(t, m, "v", 0, 0)
	if m.Version() != v {
		t.Fatalf("version advanced on failed ops: got %d, want %d", m.Version(), v)
	}
}

// Committing more than reserved fails and leaves the reservation open.
func TestCommitExceedsReserved(t *testing.T) {
	m := NewManager()
	m.SetLimit("u", 100)
	r := mustReserve(t, m, "u", 50)
	if err := r.Commit(51); err != ErrCommitExceedsSize {
		t.Fatalf("expected ErrCommitExceedsSize, got %v", err)
	}
	checkStats(t, m, "u", 0, 50)
	if err := r.Commit(50); err != nil {
		t.Fatalf("Commit(50) after failed commit failed: %v", err)
	}
	checkStats(t, m, "u", 50, 0)
}

// Truncation releases committed usage.
func TestTruncateReleasesUsage(t *testing.T) {
	m := NewManager()
	m.SetLimit("u", 100)
	r := mustReserve(t, m, "u", 80)
	if err := r.Commit(80); err != nil {
		t.Fatal(err)
	}
	if err := m.Release("u", 30); err != nil {
		t.Fatalf("Release(30) failed: %v", err)
	}
	checkStats(t, m, "u", 50, 0)
	if err := m.Release("u", 51); err != ErrInsufficientUsage {
		t.Fatalf("expected ErrInsufficientUsage, got %v", err)
	}
	checkStats(t, m, "u", 50, 0)
}

// Transfer validates the target quota atomically: failures move nothing.
func TestTransferValidatesTarget(t *testing.T) {
	m := NewManager()
	m.SetLimit("a", 100)
	m.SetLimit("b", 50)
	r := mustReserve(t, m, "a", 100)
	if err := r.Commit(100); err != nil {
		t.Fatal(err)
	}

	// Target quota too small: nothing moves.
	if err := m.Transfer("a", "b", 60); err != ErrQuotaExceeded {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	checkStats(t, m, "a", 100, 0)
	checkStats(t, m, "b", 0, 0)

	// Source balance too small: nothing moves.
	if err := m.Transfer("a", "b", 101); err != ErrInsufficientUsage {
		t.Fatalf("expected ErrInsufficientUsage, got %v", err)
	}
	checkStats(t, m, "a", 100, 0)
	checkStats(t, m, "b", 0, 0)

	// Target's outstanding reservation counts against its limit.
	rb := mustReserve(t, m, "b", 20)
	if err := m.Transfer("a", "b", 31); err != ErrQuotaExceeded {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	if err := m.Transfer("a", "b", 30); err != nil {
		t.Fatalf("Transfer(30) failed: %v", err)
	}
	checkStats(t, m, "a", 70, 0)
	checkStats(t, m, "b", 30, 20)
	if err := rb.Abort(); err != nil {
		t.Fatal(err)
	}
}

// Byte conservation under concurrent commits: the sum of committed
// bytes always equals final usage, reservations drain to zero, and the
// version equals the number of successful mutations.
func TestConcurrentCommitConservation(t *testing.T) {
	m := NewManager()
	m.SetLimit("u", 1<<20)

	const workers = 32
	const writesPerWorker = 200

	var wg sync.WaitGroup
	var mu sync.Mutex
	var totalCommitted int64
	var successfulOps uint64

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < writesPerWorker; i++ {
				size := int64((w*writesPerWorker+i)%97 + 1)
				r, err := m.Reserve("u", size)
				if err == ErrQuotaExceeded {
					continue
				}
				if err != nil {
					t.Errorf("unexpected reserve error: %v", err)
					return
				}
				mu.Lock()
				successfulOps++
				mu.Unlock()

				actual := size
				if i%3 == 0 {
					actual = size / 2 // short write
				}
				if err := r.Commit(actual); err != nil {
					t.Errorf("commit failed: %v", err)
					return
				}
				mu.Lock()
				totalCommitted += actual
				successfulOps++
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	used, reserved, _ := m.Stats("u")
	if reserved != 0 {
		t.Fatalf("reserved = %d after all commits, want 0", reserved)
	}
	if used != totalCommitted {
		t.Fatalf("used = %d, want %d (sum of committed bytes)", used, totalCommitted)
	}
	// 1 for SetLimit plus one per successful reserve/commit.
	if want := uint64(1) + successfulOps; m.Version() != want {
		t.Fatalf("version = %d, want %d", m.Version(), want)
	}
}

// Concurrent reserves against a tight limit: failures must not leak,
// so used+reserved never exceeds the limit and final state is exact.
func TestConcurrentReserveFailuresNoLeak(t *testing.T) {
	m := NewManager()
	const limit = 1000
	m.SetLimit("u", limit)

	const workers = 16
	const attempts = 500

	var wg sync.WaitGroup
	var mu sync.Mutex
	var committedTotal int64
	var successfulOps uint64

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < attempts; i++ {
				r, err := m.Reserve("u", 50)
				if err == ErrQuotaExceeded {
					continue
				}
				if err != nil {
					t.Errorf("unexpected reserve error: %v", err)
					return
				}
				mu.Lock()
				successfulOps++
				mu.Unlock()

				// Invariant: quota accounting never exceeds the limit.
				used, reserved, _ := m.Stats("u")
				if used+reserved > limit {
					t.Errorf("conservation violated: used %d + reserved %d > limit %d",
						used, reserved, limit)
					return
				}

				if i%2 == 0 {
					if err := r.Commit(50); err != nil {
						t.Errorf("commit failed: %v", err)
						return
					}
					mu.Lock()
					committedTotal += 50
					successfulOps++
					mu.Unlock()
				} else {
					if err := r.Abort(); err != nil {
						t.Errorf("abort failed: %v", err)
						return
					}
					mu.Lock()
					successfulOps++
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()

	used, reserved, _ := m.Stats("u")
	if reserved != 0 {
		t.Fatalf("reserved = %d, want 0 (aborts leaked)", reserved)
	}
	if used != committedTotal {
		t.Fatalf("used = %d, want %d", used, committedTotal)
	}
	if want := uint64(1) + successfulOps; m.Version() != want {
		t.Fatalf("version = %d, want %d", m.Version(), want)
	}
}

// Concurrent transfers between two users: total bytes across both
// accounts is conserved and failures move nothing.
func TestConcurrentTransferConservation(t *testing.T) {
	m := NewManager()
	m.SetLimit("a", 10000)
	m.SetLimit("b", 10000)
	r := mustReserve(t, m, "a", 10000)
	if err := r.Commit(10000); err != nil {
		t.Fatal(err)
	}

	const workers = 16
	const transfers = 500

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < transfers; i++ {
				if (w+i)%2 == 0 {
					m.Transfer("a", "b", 10)
				} else {
					m.Transfer("b", "a", 10)
				}
			}
		}(w)
	}
	wg.Wait()

	usedA, _, _ := m.Stats("a")
	usedB, _, _ := m.Stats("b")
	if usedA+usedB != 10000 {
		t.Fatalf("conservation violated: a %d + b %d = %d, want 10000",
			usedA, usedB, usedA+usedB)
	}
	if usedA < 0 || usedB < 0 {
		t.Fatalf("negative balance: a %d, b %d", usedA, usedB)
	}
}
