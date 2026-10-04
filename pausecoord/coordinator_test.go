package pausecoord

import (
	"testing"
	"time"
)

// waitFor fails the test if ch is not closed within d.
func waitFor(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// assertBlocked fails the test if ch is closed within d.
func assertBlocked(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s unexpectedly unblocked", what)
	case <-time.After(d):
	}
}

// assertBlockedErr fails the test if ch delivers within d.
func assertBlockedErr(t *testing.T, ch <-chan error, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s unexpectedly unblocked", what)
	case <-time.After(d):
	}
}

// waitPausing blocks until the coordinator has entered the pausing
// state, so tests can deterministically send workers to checkpoints
// after a pause round has started.
func waitPausing(t *testing.T, c *Coordinator) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if c.Paused() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("coordinator did not enter pausing state")
}

func TestBasicPauseResume(t *testing.T) {
	c := New()
	w := c.Register()

	paused := make(chan error, 1)
	go func() { paused <- c.Pause(2 * time.Second) }()
	waitPausing(t, c)

	released := make(chan struct{})
	go func() {
		w.Checkpoint() // confirms the round, then parks
		close(released)
	}()

	if err := <-paused; err != nil {
		t.Fatalf("Pause failed: %v", err)
	}
	// The worker confirmed but must stay parked while the pause holds.
	assertBlocked(t, released, 100*time.Millisecond, "parked worker")

	c.Resume()
	waitFor(t, released, 2*time.Second, "worker release after resume")
}

func TestTwoConsecutivePauses(t *testing.T) {
	c := New()
	w := c.Register()

	// A worker that keeps hitting safe points; each pause round must be
	// confirmed afresh because the epoch changes every round.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			w.Checkpoint()
			time.Sleep(time.Millisecond)
		}
	}()

	for round := 1; round <= 2; round++ {
		if err := c.Pause(2 * time.Second); err != nil {
			t.Fatalf("pause round %d failed: %v", round, err)
		}
		c.Resume()
	}
	close(stop)
	waitFor(t, done, 2*time.Second, "worker loop exit")
}

func TestSecondPauseRequiresFreshConfirmation(t *testing.T) {
	c := New()
	w := c.Register()

	// gate controls when the worker may reach its second checkpoint
	// after the first round completes.
	gate := make(chan struct{})
	atGate := make(chan struct{})
	done := make(chan struct{})

	first := make(chan error, 1)
	go func() { first <- c.Pause(2 * time.Second) }()
	waitPausing(t, c)

	go func() {
		w.Checkpoint() // confirms round 1, parks until Resume
		close(atGate)  // left the checkpoint, about to do work
		<-gate         // simulate work between safe points
		w.Checkpoint() // confirms round 2
		close(done)
	}()

	if err := <-first; err != nil {
		t.Fatalf("first Pause failed: %v", err)
	}
	c.Resume()

	// Wait until the worker has actually left its checkpoint, so round 2
	// can only be confirmed by a fresh checkpoint after the gate opens.
	waitFor(t, atGate, 2*time.Second, "worker to leave first checkpoint")

	second := make(chan error, 1)
	go func() { second <- c.Pause(2 * time.Second) }()
	waitPausing(t, c)

	// While the gate is shut the worker cannot confirm round 2; the
	// round-1 confirmation is stale and must not complete this pause.
	assertBlockedErr(t, second, 100*time.Millisecond, "second Pause before fresh confirmation")

	close(gate)
	if err := <-second; err != nil {
		t.Fatalf("second Pause failed: %v", err)
	}
	c.Resume()
	waitFor(t, done, 2*time.Second, "worker completion")
}

func TestPauseTimeoutAbandons(t *testing.T) {
	c := New()
	w := c.Register() // never checkpoints in time

	if err := c.Pause(100 * time.Millisecond); err != ErrPauseTimeout {
		t.Fatalf("expected ErrPauseTimeout, got %v", err)
	}
	if c.Paused() {
		t.Fatal("coordinator still paused after timeout; round should be abandoned")
	}

	// After abandonment a checkpoint must not block.
	ck := make(chan struct{})
	go func() {
		w.Checkpoint()
		close(ck)
	}()
	waitFor(t, ck, 2*time.Second, "checkpoint after abandoned pause")

	// A later round works normally again.
	paused := make(chan error, 1)
	go func() { paused <- c.Pause(2 * time.Second) }()
	waitPausing(t, c)
	go w.Checkpoint()
	if err := <-paused; err != nil {
		t.Fatalf("Pause after abandon failed: %v", err)
	}
	c.Resume()
}

func TestStaleConfirmationNotCounted(t *testing.T) {
	c := New()
	good := c.Register()
	bad := c.Register() // never confirms, forcing the timeout

	// Round 1: good confirms, bad does not, so the round is abandoned.
	first := make(chan error, 1)
	go func() { first <- c.Pause(200 * time.Millisecond) }()
	waitPausing(t, c)

	parked := make(chan struct{})
	go func() {
		good.Checkpoint() // confirms round 1, parks, released on abandon
		close(parked)
	}()
	if err := <-first; err != ErrPauseTimeout {
		t.Fatalf("expected ErrPauseTimeout, got %v", err)
	}
	bad.Close()
	waitFor(t, parked, 2*time.Second, "good worker released after abandon")

	// good now carries a stale confirmation for the abandoned epoch.
	// Round 2 must not count it: with good outside any checkpoint the
	// pause has to time out.
	if err := c.Pause(100 * time.Millisecond); err != ErrPauseTimeout {
		t.Fatalf("stale confirmation was counted: expected ErrPauseTimeout, got %v", err)
	}

	// A fresh confirmation completes a new round.
	third := make(chan error, 1)
	go func() { third <- c.Pause(2 * time.Second) }()
	waitPausing(t, c)
	ck := make(chan struct{})
	go func() {
		good.Checkpoint()
		close(ck)
	}()
	if err := <-third; err != nil {
		t.Fatalf("Pause with fresh confirmation failed: %v", err)
	}
	c.Resume()
	waitFor(t, ck, 2*time.Second, "worker release")
}

func TestWorkerExitDuringPause(t *testing.T) {
	c := New()
	w1 := c.Register()
	w2 := c.Register()

	paused := make(chan error, 1)
	go func() { paused <- c.Pause(2 * time.Second) }()
	waitPausing(t, c)

	released := make(chan struct{})
	go func() {
		w1.Checkpoint() // confirms, then parks
		close(released)
	}()

	// w2 never checkpoints but exits mid-pause; it must drop out of the
	// required set so the round can complete.
	time.Sleep(50 * time.Millisecond)
	w2.Close()

	if err := <-paused; err != nil {
		t.Fatalf("Pause failed although exiting worker dropped out: %v", err)
	}
	c.Resume()
	waitFor(t, released, 2*time.Second, "parked worker release")
}

func TestNewWorkerWaitsForResume(t *testing.T) {
	c := New()
	w := c.Register()

	paused := make(chan error, 1)
	go func() { paused <- c.Pause(2 * time.Second) }()
	waitPausing(t, c)
	go w.Checkpoint()
	if err := <-paused; err != nil {
		t.Fatalf("Pause failed: %v", err)
	}

	registered := make(chan *Worker, 1)
	go func() { registered <- c.Register() }()

	// Registration must block while a snapshot could be read.
	select {
	case <-registered:
		t.Fatal("new worker registered during pause")
	case <-time.After(100 * time.Millisecond):
	}

	c.Resume()
	select {
	case nw := <-registered:
		nw.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("registration still blocked after resume")
	}
}

func TestWithSnapshot(t *testing.T) {
	c := New()
	w := c.Register()

	go func() {
		for !c.Paused() {
			time.Sleep(time.Millisecond)
		}
		w.Checkpoint()
	}()

	ran := false
	if err := c.WithSnapshot(2*time.Second, func() { ran = true }); err != nil {
		t.Fatalf("WithSnapshot failed: %v", err)
	}
	if !ran {
		t.Fatal("snapshot function did not run")
	}
	if c.Paused() {
		t.Fatal("coordinator still paused after WithSnapshot returned")
	}
}
