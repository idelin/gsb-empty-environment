package leasefence

import (
	"errors"
	"testing"
	"time"
)

// Renewal bumps the version monotonically without terminating the version the
// in-flight job was registered under, so a job already preparing its commit
// stays committable after the holder renews.
func TestRenewalBumpsVersionAndKeepsInFlightCommittable(t *testing.T) {
	f, clock := newTestFence(t, 10*time.Second)
	defer f.Close()

	h1, err := f.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if g, v := h1.Token().Generation(), h1.Token().Version(); g != 1 || v != 1 {
		t.Fatalf("initial token = (gen=%d ver=%d), want (1,1)", g, v)
	}

	ticket, err := f.Begin(h1)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if got := f.Snapshot().InFlight; got != 1 {
		t.Fatalf("InFlight = %d, want 1", got)
	}

	clock.Advance(4 * time.Second)
	h2, err := f.Renew(h1)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if h2.Token().Generation() != 1 || h2.Token().Version() != 2 {
		t.Fatalf("renewed token = (gen=%d ver=%d), want (1,2)",
			h2.Token().Generation(), h2.Token().Version())
	}

	// The old version's ticket survives the renewal and still validates.
	if err := f.Validate(ticket); err != nil {
		t.Fatalf("Validate after renewal: %v", err)
	}
	committer := newBlockingCommitter()
	go func() { committer.gate <- struct{}{} }()
	if err := f.Commit(ticket, committer.submit); err != nil {
		t.Fatalf("Commit of pre-renewal ticket: %v", err)
	}
	if got := f.Snapshot().InFlight; got != 0 {
		t.Fatalf("InFlight after commit = %d, want 0", got)
	}

	// Version keeps increasing monotonically within the same generation.
	h3, err := f.Renew(h2)
	if err != nil {
		t.Fatalf("second Renew: %v", err)
	}
	if h3.Token().Version() != 3 {
		t.Fatalf("version after second renewal = %d, want 3", h3.Token().Version())
	}

	// A second Acquire while held is rejected instead of reusing generation.
	if _, err := f.Acquire(); !errors.Is(err, ErrHeld) {
		t.Fatalf("Acquire while held: %v, want ErrHeld", err)
	}
}

// Timeout only takes effect when a reaper runs; reaping terminates exactly the
// current version and makes its tickets uncommittable, while Acquire after
// reaping hands out a fresh generation.
func TestTimeoutReapTerminatesExactVersion(t *testing.T) {
	f, clock := newTestFence(t, 10*time.Second)
	defer f.Close()

	h, err := f.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	ticket, err := f.Begin(h)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// One instant before the deadline the lease is still alive.
	clock.Advance(10*time.Second - time.Nanosecond)
	if f.ReapExpired() {
		t.Fatal("ReapExpired before the deadline returned true")
	}
	if err := f.Validate(ticket); err != nil {
		t.Fatalf("Validate before the deadline: %v", err)
	}

	clock.Advance(time.Nanosecond)
	if !f.ReapExpired() {
		t.Fatal("ReapExpired at the deadline returned false")
	}
	// Idempotent: nothing is held anymore.
	if f.ReapExpired() {
		t.Fatal("second ReapExpired returned true")
	}
	checkErr(t, f.Validate(ticket), ErrFenced)
	if _, err := f.Renew(h); !errors.Is(err, ErrFenced) {
		t.Fatalf("Renew after reap: %v, want ErrFenced", err)
	}
	if f.Snapshot().TerminatedPairs != 1 {
		t.Fatalf("TerminatedPairs = %d, want 1", f.Snapshot().TerminatedPairs)
	}

	// The resource is reused by a new holder: generation moves forward,
	// version restarts inside the new generation.
	h2, err := f.Acquire()
	if err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}
	if g, v := h2.Token().Generation(), h2.Token().Version(); g != 2 || v != 1 {
		t.Fatalf("new holder token = (gen=%d ver=%d), want (2,1)", g, v)
	}
	ticket2, err := f.Begin(h2)
	if err != nil {
		t.Fatalf("Begin under new holder: %v", err)
	}
	if err := f.Validate(ticket2); err != nil {
		t.Fatalf("new holder ticket validation: %v", err)
	}
	// The old ticket can never be confused with the new holder's work.
	checkErr(t, f.Validate(ticket), ErrFenced)
}

// A pending Begin after the deadline is rejected with ErrExpired rather than
// registering a ticket that could later be mistaken for a new lease's work.
func TestOperationsAfterDeadlineButBeforeReap(t *testing.T) {
	f, clock := newTestFence(t, 10*time.Second)
	defer f.Close()

	h, err := f.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	clock.Advance(10*time.Second + time.Nanosecond)

	if _, err := f.Begin(h); !errors.Is(err, ErrExpired) {
		t.Fatalf("Begin after deadline: %v, want ErrExpired", err)
	}
	if _, err := f.Renew(h); !errors.Is(err, ErrExpired) {
		t.Fatalf("Renew after deadline: %v, want ErrExpired", err)
	}
	// Acquire itself performs the reaping and then starts generation 2.
	h2, err := f.Acquire()
	if err != nil {
		t.Fatalf("Acquire after expiry: %v", err)
	}
	if h2.Token().Generation() != 2 {
		t.Fatalf("generation after expiry re-acquire = %d, want 2",
			h2.Token().Generation())
	}
}
