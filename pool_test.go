package pool

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var bg = context.Background()

// barrier blocks until n parties arrive, then releases all of them.
type barrier struct {
	mu    sync.Mutex
	n     int
	count int
	ch    chan struct{}
}

func newBarrier(n int) *barrier { return &barrier{n: n, ch: make(chan struct{})} }

func (b *barrier) await() {
	b.mu.Lock()
	b.count++
	if b.count == b.n {
		close(b.ch)
	}
	b.mu.Unlock()
	<-b.ch
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func waitTimers(t *testing.T, c *ManualClock, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.Timers() != n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d manual timers, have %d", n, c.Timers())
		}
		time.Sleep(time.Millisecond)
	}
}

// --- Race matrix: Cancel (C) vs Start (S) vs Deliver (D), enumerated ---

// C before S: cancel lands while the ticket is still pending; fn never runs.
func TestRaceCancelBeforeStart(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	p := New(1, 1, WithHooks(Hooks{
		BeforeStart: func(*Ticket) { close(entered); <-release },
	}))
	defer p.Wait()
	defer p.Close()

	ran := atomic.Bool{}
	tk, err := p.Submit(bg, func(context.Context) (any, error) {
		ran.Store(true)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitClosed(t, entered, "worker parked before start CAS")

	if !tk.Cancel() {
		t.Fatal("cancel should win while ticket is pending")
	}
	close(release)

	if _, err := tk.Wait(bg); !errors.Is(err, ErrCanceled) {
		t.Fatalf("Wait = %v, want ErrCanceled", err)
	}
	if ran.Load() {
		t.Fatal("canceled ticket must not run")
	}
	if got := tk.State(); got != Canceled {
		t.Fatalf("state = %v, want Canceled", got)
	}
	if got := p.Available(); got != 1 {
		t.Fatalf("Available = %d, want 1 (slot must be returned)", got)
	}
}

// S then C before D: cancel lands while fn is running but before the result
// is delivered; the result is discarded and Canceled is the terminal state.
func TestRaceStartThenCancelBeforeDeliver(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	p := New(1, 1, WithHooks(Hooks{
		BeforeDeliver: func(*Ticket) { close(entered); <-release },
	}))
	defer p.Wait()
	defer p.Close()

	tk, err := p.Submit(bg, func(context.Context) (any, error) { return 42, nil })
	if err != nil {
		t.Fatal(err)
	}
	waitClosed(t, entered, "worker parked before deliver CAS")

	if !tk.Cancel() {
		t.Fatal("cancel should win before delivery")
	}
	close(release)

	if _, err := tk.Wait(bg); !errors.Is(err, ErrCanceled) {
		t.Fatalf("Wait = %v, want ErrCanceled", err)
	}
	if got := tk.State(); got != Canceled {
		t.Fatalf("state = %v, want Canceled", got)
	}
	if got := p.Available(); got != 1 {
		t.Fatalf("Available = %d, want 1 (slot must be returned)", got)
	}
}

// S then D then C: the delivered result cannot be overwritten by cancel.
func TestRaceDeliverThenCancel(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	p := New(1, 1, WithHooks(Hooks{
		BeforeDeliver: func(*Ticket) { close(entered); <-release },
	}))
	defer p.Wait()
	defer p.Close()

	tk, err := p.Submit(bg, func(context.Context) (any, error) { return 42, nil })
	if err != nil {
		t.Fatal(err)
	}
	waitClosed(t, entered, "worker parked before deliver CAS")
	close(release)
	waitClosed(t, tk.Done(), "delivery")

	if tk.Cancel() {
		t.Fatal("delivered result must not be cancelable")
	}
	res, err := tk.Wait(bg)
	if err != nil || res != 42 {
		t.Fatalf("Wait = (%v, %v), want (42, nil)", res, err)
	}
	if got := tk.State(); got != Succeeded {
		t.Fatalf("state = %v, want Succeeded", got)
	}
	if got := p.Available(); got != 1 {
		t.Fatalf("Available = %d, want 1 (slot must be returned)", got)
	}
}

// C after S via AfterStart: cancel transitions Running->Canceled, the task's
// context is canceled, and Canceled remains the single terminal state.
func TestRaceCancelAfterStartObserved(t *testing.T) {
	started := make(chan struct{})
	p := New(1, 1, WithHooks(Hooks{
		AfterStart: func(*Ticket) { close(started) },
	}))
	defer p.Wait()
	defer p.Close()

	sawCtxCancel := make(chan struct{})
	tk, err := p.Submit(bg, func(ctx context.Context) (any, error) {
		<-ctx.Done()
		close(sawCtxCancel)
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	waitClosed(t, started, "running state observed")

	if !tk.Cancel() {
		t.Fatal("cancel should win while running and undelivered")
	}
	if _, err := tk.Wait(bg); !errors.Is(err, ErrCanceled) {
		t.Fatalf("Wait = %v, want ErrCanceled", err)
	}
	waitClosed(t, sawCtxCancel, "running task to observe context cancellation")
	if got := tk.State(); got != Canceled {
		t.Fatalf("state = %v, want Canceled", got)
	}
}

// --- Capacity ---

// A slot is returned the moment the ticket reaches its terminal state, even
// while the canceled task's fn is still executing.
func TestCapacityReturnedOnCancel(t *testing.T) {
	p := New(1, 1)
	defer p.Wait()
	defer p.Close()

	gate := make(chan struct{})
	started := make(chan struct{})
	tk1, err := p.Submit(bg, func(context.Context) (any, error) {
		close(started)
		<-gate
		return 1, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitClosed(t, started, "first task running")

	if got := p.Available(); got != 0 {
		t.Fatalf("Available = %d, want 0 while running", got)
	}
	cctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := p.Submit(cctx, func(context.Context) (any, error) { return 0, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Submit on full pool = %v, want context.Canceled", err)
	}

	if !tk1.Cancel() {
		t.Fatal("cancel of running ticket should succeed")
	}
	if got := p.Available(); got != 1 {
		t.Fatalf("Available = %d, want 1 after cancel (slot returned)", got)
	}

	tk2, err := p.Submit(bg, func(context.Context) (any, error) { return 2, nil })
	if err != nil {
		t.Fatalf("Submit after capacity returned: %v", err)
	}
	close(gate)
	if res, err := tk2.Wait(bg); err != nil || res != 2 {
		t.Fatalf("tk2.Wait = (%v, %v), want (2, nil)", res, err)
	}
	if _, err := tk1.Wait(bg); !errors.Is(err, ErrCanceled) {
		t.Fatalf("tk1.Wait = %v, want ErrCanceled", err)
	}
}

// --- Controllable clock ---

func TestWaitTimeoutManualClock(t *testing.T) {
	clock := NewManualClock(time.Unix(1700000000, 0))
	p := New(1, 1, WithClock(clock))
	defer p.Wait()
	defer p.Close()

	gate := make(chan struct{})
	tk, err := p.Submit(bg, func(context.Context) (any, error) {
		<-gate
		return "done", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := tk.WaitTimeout(30 * time.Second)
		errCh <- err
	}()
	waitTimers(t, clock, 1)

	clock.Advance(29 * time.Second)
	select {
	case err := <-errCh:
		t.Fatalf("WaitTimeout fired early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	clock.Advance(1 * time.Second)
	if err := <-errCh; !errors.Is(err, ErrTimeout) {
		t.Fatalf("WaitTimeout = %v, want ErrTimeout", err)
	}

	close(gate)
	if res, err := tk.Wait(bg); err != nil || res != "done" {
		t.Fatalf("Wait = (%v, %v), want (done, nil)", res, err)
	}
}

// --- Shutdown ---

// Close force-cancels pending and running tickets so every waiter exits;
// Submit afterwards fails with ErrClosed and all capacity is returned.
func TestCloseReleasesWaiters(t *testing.T) {
	p := New(1, 2)

	started := make(chan struct{})
	tkRun, err := p.Submit(bg, func(ctx context.Context) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	queuedRan := atomic.Bool{}
	tkQueued, err := p.Submit(bg, func(context.Context) (any, error) {
		queuedRan.Store(true)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitClosed(t, started, "running task started")

	waitErrs := make(chan error, 2)
	go func() { _, err := tkRun.Wait(bg); waitErrs <- err }()
	go func() { _, err := tkQueued.Wait(bg); waitErrs <- err }()

	p.Close()

	for i := 0; i < 2; i++ {
		select {
		case err := <-waitErrs:
			if !errors.Is(err, ErrCanceled) {
				t.Fatalf("waiter exited with %v, want ErrCanceled", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("waiter did not exit after Close")
		}
	}
	if queuedRan.Load() {
		t.Fatal("queued ticket must not run after Close")
	}
	if _, err := p.Submit(bg, func(context.Context) (any, error) { return 0, nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit after Close = %v, want ErrClosed", err)
	}

	p.Wait()
	if got := p.Available(); got != 2 {
		t.Fatalf("Available = %d, want 2 after Close", got)
	}
}

// --- Stress: single terminal state under concurrent cancel/deliver ---

func TestConcurrentCancelDeliverSingleTerminal(t *testing.T) {
	p := New(4, 8)

	const n = 300
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		tk, err := p.Submit(bg, func(context.Context) (any, error) { return i, nil })
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(2)
		go func(tk *Ticket) {
			defer wg.Done()
			runtime.Gosched()
			tk.Cancel()
		}(tk)
		go func(tk *Ticket) {
			defer wg.Done()
			res, err := tk.Wait(bg)
			st := tk.State()
			switch {
			case errors.Is(err, ErrCanceled):
				if st != Canceled {
					t.Errorf("ticket %d: ErrCanceled but state %v", tk.ID(), st)
				}
			case err != nil:
				t.Errorf("ticket %d: unexpected error %v", tk.ID(), err)
			default:
				if st != Succeeded {
					t.Errorf("ticket %d: result delivered but state %v", tk.ID(), st)
				}
				if res == nil {
					t.Errorf("ticket %d: succeeded with nil result", tk.ID())
				}
			}
			if !st.Terminal() {
				t.Errorf("ticket %d: non-terminal state %v after Wait", tk.ID(), st)
			}
		}(tk)
	}
	wg.Wait()

	if got := p.Available(); got != 8 {
		t.Fatalf("Available = %d, want 8: capacity leaked or double-released", got)
	}
	p.Close()
	p.Wait()
}

// Double cancel and cancel-after-close are no-ops reporting false.
func TestCancelIdempotent(t *testing.T) {
	p := New(1, 1)
	defer p.Wait()

	tk, err := p.Submit(bg, func(context.Context) (any, error) { return 1, nil })
	if err != nil {
		t.Fatal(err)
	}
	p.Close() // force-cancels the ticket
	if tk.Cancel() {
		t.Fatal("cancel after terminal state must return false")
	}
	if got := tk.State(); got != Canceled {
		t.Fatalf("state = %v, want Canceled", got)
	}
}
