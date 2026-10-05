package pool

import (
	"sync"
	"testing"
	"time"
)

// barrier parks a test hook until the test releases it.
type barrier struct {
	entered chan struct{}
	release chan struct{}
}

func newBarrier() *barrier {
	return &barrier{entered: make(chan struct{}), release: make(chan struct{})}
}

func taskReturning(v interface{}, err error) Task {
	return func() (interface{}, error) { return v, err }
}

// Race ordering 1a: cancel lands before the task starts.
// The pending ticket must never run, and its slot must be returned.
func TestCancelBeforeStart(t *testing.T) {
	p := New(1)
	b := newBarrier()
	p.hookPreStart = func() { close(b.entered); <-b.release }

	t1, err := p.Submit(taskReturning(42, nil))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-b.entered          // slot held, task parked before markRunning
	p.hookPreStart = nil // fire only for the first submission
	if got := t1.State(); got != StatePending {
		t.Fatalf("state = %v, want pending", got)
	}
	if !t1.Cancel() {
		t.Fatal("Cancel returned false, want true")
	}
	close(b.release)
	if _, err := t1.Wait(); err != ErrCancelled {
		t.Fatalf("Wait err = %v, want ErrCancelled", err)
	}
	if got := t1.State(); got != StateCancelled {
		t.Fatalf("state = %v, want cancelled", got)
	}

	// Capacity was returned: a new submission proceeds immediately.
	t2, err := p.SubmitWithin(taskReturning(7, nil), time.Second)
	if err != nil {
		t.Fatalf("submit after cancel: %v", err)
	}
	if v, err := t2.Wait(); err != nil || v != 7 {
		t.Fatalf("Wait = %v, %v; want 7, nil", v, err)
	}
	p.Shutdown()
}

// Race ordering 1b: cancel lands after the task started.
// The running ticket is cancelled and its eventual result discarded.
func TestCancelAfterStartDiscardsResult(t *testing.T) {
	p := New(1)
	b := newBarrier()
	p.hookPostStart = func() { close(b.entered); <-b.release }

	tk, err := p.Submit(taskReturning(99, nil))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-b.entered // markRunning done, task function not yet called
	if got := tk.State(); got != StateRunning {
		t.Fatalf("state = %v, want running", got)
	}
	if !tk.Cancel() {
		t.Fatal("Cancel returned false, want true")
	}
	close(b.release)
	if _, err := tk.Wait(); err != ErrCancelled {
		t.Fatalf("Wait err = %v, want ErrCancelled (result must be discarded)", err)
	}
	p.Shutdown()
}

// Race ordering 2a: result delivery lands before cancel.
// The delivered result must survive; Cancel must lose.
func TestDeliveredResultSurvivesCancel(t *testing.T) {
	p := New(1)
	tk, err := p.Submit(taskReturning("ok", nil))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	v, err := tk.Wait() // blocks until delivered
	if err != nil || v != "ok" {
		t.Fatalf("Wait = %v, %v; want ok, nil", v, err)
	}
	if tk.Cancel() {
		t.Fatal("Cancel returned true after delivery, want false")
	}
	if got := tk.State(); got != StateDone {
		t.Fatalf("state = %v, want done", got)
	}
	// Result is still intact after the lost cancellation.
	v, err = tk.Wait()
	if err != nil || v != "ok" {
		t.Fatalf("Wait after cancel = %v, %v; want ok, nil", v, err)
	}
	p.Shutdown()
}

// Race ordering 2b: cancel lands between task return and result
// delivery. The cancellation wins and the result is discarded.
func TestCancelBeatsCompletion(t *testing.T) {
	p := New(1)
	b := newBarrier()
	p.hookPreComplete = func() { close(b.entered); <-b.release }

	tk, err := p.Submit(taskReturning(1, nil))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-b.entered // task returned, complete() not yet attempted
	if !tk.Cancel() {
		t.Fatal("Cancel returned false, want true")
	}
	close(b.release)
	if _, err := tk.Wait(); err != ErrCancelled {
		t.Fatalf("Wait err = %v, want ErrCancelled", err)
	}
	if got := tk.State(); got != StateCancelled {
		t.Fatalf("state = %v, want cancelled", got)
	}
	p.Shutdown()
}

// Capacity accounting: a full pool rejects SubmitWithin via the fake
// clock, and cancelling the running ticket returns its slot.
func TestCapacityReturnedOnCancel(t *testing.T) {
	clock := NewFakeClock()
	p := New(1, WithClock(clock))

	block := make(chan struct{})
	started := make(chan struct{})
	t1, err := p.Submit(func() (interface{}, error) {
		close(started)
		<-block
		return 1, nil
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-started // t1 running, pool full

	// A second submission times out on the fake clock, not real time.
	errCh := make(chan error, 1)
	go func() {
		_, err := p.SubmitWithin(taskReturning(2, nil), time.Second)
		errCh <- err
	}()
	clock.WaitTimers(1) // submitter is parked on its timer
	clock.Advance(2 * time.Second)
	if err := <-errCh; err != ErrTimeout {
		t.Fatalf("SubmitWithin err = %v, want ErrTimeout", err)
	}

	// Cancel the running ticket and let its task return; the slot is
	// returned even though the result is discarded.
	if !t1.Cancel() {
		t.Fatal("Cancel returned false, want true")
	}
	close(block)
	if _, err := t1.Wait(); err != ErrCancelled {
		t.Fatalf("Wait err = %v, want ErrCancelled", err)
	}

	t2, err := p.Submit(taskReturning(3, nil))
	if err != nil {
		t.Fatalf("submit after slot returned: %v", err)
	}
	if v, err := t2.Wait(); err != nil || v != 3 {
		t.Fatalf("Wait = %v, %v; want 3, nil", v, err)
	}
	p.Shutdown()
}

// Shutdown wakes submitters blocked on a full pool with ErrShutdown.
func TestShutdownWakesBlockedSubmitters(t *testing.T) {
	p := New(1)

	block := make(chan struct{})
	entered := make(chan struct{})
	t1, err := p.Submit(func() (interface{}, error) {
		close(entered)
		<-block
		return 1, nil
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-entered // pool full

	waiting := make(chan struct{})
	p.hookPreSubmit = func() { close(waiting) }
	submitErr := make(chan error, 1)
	go func() {
		_, err := p.Submit(taskReturning(2, nil))
		submitErr <- err
	}()
	<-waiting             // second submitter is about to block on the full pool
	p.hookPreSubmit = nil // fire only once

	shutdownDone := make(chan struct{})
	go func() { p.Shutdown(); close(shutdownDone) }()

	if err := <-submitErr; err != ErrShutdown {
		t.Fatalf("blocked Submit err = %v, want ErrShutdown", err)
	}
	// Shutdown is still waiting for the running task.
	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned while a task was still running")
	default:
	}
	close(block)
	if v, err := t1.Wait(); err != nil || v != 1 {
		t.Fatalf("running task result = %v, %v; want 1, nil", v, err)
	}
	<-shutdownDone

	// New submissions fail immediately after shutdown.
	if _, err := p.Submit(taskReturning(3, nil)); err != ErrShutdown {
		t.Fatalf("Submit after shutdown err = %v, want ErrShutdown", err)
	}
}

// Shutdown cancels pending tickets, releasing their Wait callers.
func TestShutdownCancelsPendingAndWaitersExit(t *testing.T) {
	p := New(1)
	b := newBarrier()
	p.hookPreStart = func() { close(b.entered); <-b.release }

	tk, err := p.Submit(taskReturning(1, nil))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-b.entered // ticket pending, worker parked before start

	waitErr := make(chan error, 1)
	go func() {
		_, err := tk.Wait()
		waitErr <- err
	}()

	shutdownDone := make(chan struct{})
	go func() { p.Shutdown(); close(shutdownDone) }()

	if err := <-waitErr; err != ErrCancelled {
		t.Fatalf("Wait err = %v, want ErrCancelled", err)
	}
	close(b.release) // let the parked worker observe the cancellation
	<-shutdownDone
	if got := tk.State(); got != StateCancelled {
		t.Fatalf("state = %v, want cancelled", got)
	}
}

// Under random interleavings (run with -race), every ticket reaches
// exactly one terminal state and Wait agrees with it.
func TestSingleTerminalStateUnderRace(t *testing.T) {
	p := New(4)
	const n = 2000
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		tk, err := p.Submit(taskReturning(i, nil))
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cancelled := tk.Cancel()
			v, err := tk.Wait()
			switch tk.State() {
			case StateCancelled:
				if err != ErrCancelled {
					t.Errorf("cancelled ticket: Wait err = %v, want ErrCancelled", err)
				}
				if !cancelled {
					t.Errorf("ticket cancelled but Cancel returned false")
				}
			case StateDone:
				if err != nil || v != i {
					t.Errorf("done ticket: Wait = %v, %v; want %d, nil", v, err, i)
				}
				if cancelled {
					t.Errorf("Cancel returned true but result was delivered")
				}
			default:
				t.Errorf("ticket in non-terminal state %v after Wait", tk.State())
			}
		}()
	}
	wg.Wait()
	p.Shutdown()
}
