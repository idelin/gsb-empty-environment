package leasefence

import (
	"errors"
	"testing"
	"time"
)

// Generation reuse: an old job blocked inside its prepare phase must be
// rejected at final validation after expiry and re-acquire, even though it
// holds a ticket that looked valid when Begin returned. Only the new
// generation's job may apply its transaction.
func TestExpiredInFlightCannotCommitIntoNewGeneration(t *testing.T) {
	f, clock := newTestFence(t, 10*time.Second)
	defer f.Close()

	oldHandle, err := f.Acquire()
	if err != nil {
		t.Fatalf("Acquire old: %v", err)
	}
	committer := newBlockingCommitter()
	jobErr := make(chan error, 1)
	go func() { jobErr <- runJob(f, oldHandle, committer) }()

	// Wait until the old job has registered its ticket and is blocked in
	// prepare, then expire and recycle the lease underneath it.
	oldToken := <-committer.started
	clock.Advance(11 * time.Second)
	if !f.ReapExpired() {
		t.Fatal("ReapExpired returned false")
	}
	newHandle, err := f.Acquire()
	if err != nil {
		t.Fatalf("Acquire new: %v", err)
	}

	// Releasing the blocker lets the old job reach its mandatory final check.
	committer.release()
	checkErr(t, <-jobErr, ErrFenced)

	// The new holder's job runs end to end and is the only applied mutation.
	newCommitter := newBlockingCommitter()
	go func() { newCommitter.gate <- struct{}{} }()
	if err := runJob(f, newHandle, newCommitter); err != nil {
		t.Fatalf("new generation job: %v", err)
	}

	applied := newCommitter.appliedTokens()
	if len(applied) != 1 || applied[0].Generation() != 2 {
		t.Fatalf("applied tokens = %+v, want only generation 2", applied)
	}
	prepared := committer.preparedTokens()
	if len(prepared) != 1 || prepared[0] != oldToken || oldToken.Generation() != 1 {
		t.Fatalf("old job prepare state = %+v, want only token %+v", prepared, oldToken)
	}

	// Handles from the dead generation are fenced forever; an old handle can
	// never Begin a ticket under the new holder.
	if _, err := f.Begin(oldHandle); !errors.Is(err, ErrFenced) {
		t.Fatalf("Begin with stale handle: %v, want ErrFenced", err)
	}
}

// While a job is blocked in prepare, a renewal keeps its old-version ticket
// valid; reaping is the only thing that terminates a version.
func TestBlockingCommitterRenewalRaceStillCommittable(t *testing.T) {
	f, clock := newTestFence(t, 10*time.Second)
	defer f.Close()

	h, err := f.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	committer := newBlockingCommitter()
	jobErr := make(chan error, 1)
	go func() { jobErr <- runJob(f, h, committer) }()

	tok := <-committer.started
	clock.Advance(9 * time.Second)
	if _, err := f.Renew(h); err != nil {
		t.Fatalf("Renew while job blocked: %v", err)
	}
	// Renewal pushed the deadline out, so the old in-flight ticket survives.
	committer.release()
	if err := <-jobErr; err != nil {
		t.Fatalf("job should commit under renewed lease, got %v", err)
	}
	applied := committer.appliedTokens()
	if len(applied) != 1 || applied[0] != tok {
		t.Fatalf("applied tokens = %+v, want only %+v", applied, tok)
	}
}

// Late reaping must never kill a newer lease: both an expired reaper call that
// arrives after a renewal and one that arrives after a whole new generation
// are no-ops.
func TestLateReaperNeverTouchesNewHolder(t *testing.T) {
	f, clock := newTestFence(t, 10*time.Second)
	defer f.Close()

	h, err := f.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	original := keyOf(h.Token())

	// A reaper that fires before the deadline changes nothing.
	clock.Advance(5 * time.Second)
	if f.terminateVersion(original, clock.Now()) {
		t.Fatal("early reaper terminated a live version")
	}

	// Renewal replaces the current version. A late reaper still carrying the
	// old (gen 1, ver 1) key cannot terminate (gen 1, ver 2).
	h2, err := f.Renew(h)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	clock.Advance(6 * time.Second)
	if f.terminateVersion(original, clock.Now()) {
		t.Fatal("stale reaper killed the renewed version")
	}
	if !f.Snapshot().Held {
		t.Fatal("fence lost its holder to a stale reaper")
	}
	ticket, err := f.Begin(h2)
	if err != nil {
		t.Fatalf("Begin after stale reaper: %v", err)
	}

	// Expire the renewed lease, recycle the whole generation, then fire both
	// stale keys at the new holder.
	clock.Advance(5 * time.Second)
	if !f.ReapExpired() {
		t.Fatal("ReapExpired of renewed version returned false")
	}
	newHandle, err := f.Acquire()
	if err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}
	if f.terminateVersion(original, clock.Now()) {
		t.Fatal("generation-1 v1 reaper touched generation 2")
	}
	renewedKey := keyOf(h2.Token())
	if f.terminateVersion(renewedKey, clock.Now()) {
		t.Fatal("generation-1 v2 reaper touched generation 2")
	}

	newTicket, err := f.Begin(newHandle)
	if err != nil {
		t.Fatalf("Begin under new holder: %v", err)
	}
	if err := f.Validate(newTicket); err != nil {
		t.Fatalf("new holder validation after stale reapers: %v", err)
	}
	if err := f.Validate(ticket); !errors.Is(err, ErrFenced) {
		t.Fatalf("old renewed ticket: %v, want ErrFenced", err)
	}

	// ReapExpired on the healthy new holder is equally harmless.
	if f.ReapExpired() {
		t.Fatal("ReapExpired killed a fresh generation")
	}
	if err := f.Validate(newTicket); err != nil {
		t.Fatalf("new ticket after late ReapExpired: %v", err)
	}
}

// The background reaper driven by a ticker reaps only a genuinely expired
// lease and leaves renewed work alone.
func TestRunReaperRespectsControllableClock(t *testing.T) {
	f, clock := newTestFence(t, 10*time.Second)
	defer f.Close()

	h, err := f.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	ticks := make(chan time.Time, 4)
	done := make(chan struct{})
	go func() {
		f.RunReaper(ticks)
		close(done)
	}()

	ticks <- clock.Now()
	waitFor(t, f, func(s Snapshot) bool { return s.Held }, "first tick leaves lease held")
	clock.Advance(5 * time.Second)
	if _, err := f.Renew(h); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	ticks <- clock.Now()
	waitFor(t, f, func(s Snapshot) bool { return s.Held && s.Version == 2 },
		"tick after renewal leaves lease held at version 2")
	clock.Advance(10*time.Second + time.Nanosecond)
	ticks <- clock.Now()
	waitFor(t, f, func(s Snapshot) bool { return !s.Held },
		"tick past the renewed deadline reaps the lease")
	close(ticks)
	<-done
}
