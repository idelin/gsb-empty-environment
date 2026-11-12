package execctx_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.org/netsec/q0109/execctx"
)

// detachPlugin hands its work to a lease and returns so the synchronous call
// completes while the asynchronous callback stays authorized.
type detachPlugin struct {
	lease   *execctx.Lease
	reserve int64
}

func (p *detachPlugin) Name() string { return "detach" }

func (p *detachPlugin) Invoke(ctx context.Context, host Host, _ int) error {
	f := host.FrameOf(ctx)
	l, err := f.Detach(p.reserve)
	if err != nil {
		return err
	}
	p.lease = l
	return nil
}

func TestLeaseAuthorizesAsyncCallback(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 100})
	dp := &detachPlugin{reserve: 20}
	if err := g.Dispatch(dp, 0, 10); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	lease := dp.lease
	if lease == nil || !lease.Active() {
		t.Fatal("missing active lease")
	}
	c := g.Controller()
	// Charge 10 inherited as reservation plus 20 extra reserve.
	assertBudget(t, c, 30)
	if c.Inflight() != 0 || c.ActiveLeases() != 1 {
		t.Fatalf("inflight=%d leases=%d", c.Inflight(), c.ActiveLeases())
	}

	f, err := lease.Enter(5)
	if err != nil {
		t.Fatalf("lease enter: %v", err)
	}
	// Lease entry is itself charged first; reservation stays reserved too.
	assertBudget(t, c, 35)
	if err := f.Complete(0); err != nil {
		t.Fatalf("complete: %v", err)
	}
	// Complete keeps the charge; only Release returns the reservation.
	assertBudget(t, c, 35)

	if err := lease.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	assertBudget(t, c, 5)
	if err := lease.Release(); !errors.Is(err, execctx.ErrInvalidLease) {
		t.Fatalf("double release err = %v, want ErrInvalidLease", err)
	}
	if _, err := lease.Enter(1); !errors.Is(err, execctx.ErrInvalidLease) {
		t.Fatalf("enter after release err = %v, want ErrInvalidLease", err)
	}
}

func TestUnauthorizedCallbackRejectedAfterFinish(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 100})
	dp := &detachPlugin{reserve: 10}
	if err := g.Dispatch(dp, 0, 10); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	lease := dp.lease

	ev := g.Finish()
	if ev.ActiveLeases != 0 {
		t.Fatalf("active leases at finish = %d, want 0", ev.ActiveLeases)
	}
	if lease.Active() {
		t.Fatal("lease should be revoked at finish")
	}
	// Finish refunds the reservation: the budget is fully returned.
	assertBudget(t, g.Controller(), 0)

	if _, err := lease.Enter(1); !errors.Is(err, execctx.ErrInvalidLease) {
		t.Fatalf("post-finish callback err = %v, want ErrInvalidLease", err)
	}
	if _, err := g.Controller().Enter(nil, 1); !errors.Is(err, execctx.ErrFinished) {
		t.Fatalf("post-finish entry err = %v, want ErrFinished", err)
	}

	select {
	case <-g.Controller().End():
	default:
		t.Fatal("end event not published")
	}
}

func TestDetachReservesWithinBudget(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 15})
	dp := &detachPlugin{reserve: 10}
	err := g.Dispatch(dp, 0, 10)
	if !errors.Is(err, execctx.ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
	// Rejected detach must leave the frame settled by the gateway wrapper.
	if g.Controller().Inflight() != 0 {
		t.Fatalf("inflight = %d, want 0", g.Controller().Inflight())
	}
	assertBudget(t, g.Controller(), 0)
}

func TestCancellationCannotBeClearedByChild(t *testing.T) {
	c := execctx.New(execctx.Config{Budget: 100})
	parent, err := c.Enter(nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	c.Cancel(errors.New("host abort"))

	// Frames expose no cancel/reset API; new entries are refused after cancel.
	if _, err := c.Enter(parent, 5); !errors.Is(err, execctx.ErrCanceled) {
		t.Fatalf("err = %v, want ErrCanceled", err)
	}
	if !c.Canceled() || c.CancelCause() == nil {
		t.Fatal("cancellation state lost")
	}
	// Repeated cancel keeps the original cause and the single Done channel.
	c.Cancel(errors.New("second abort"))
	if c.CancelCause().Error() != "host abort" {
		t.Fatalf("cause = %v, want first cause", c.CancelCause())
	}
	select {
	case <-parent.Done():
	default:
		t.Fatal("parent Done channel not closed")
	}
	if !errors.Is(parent.Err(), context.Canceled) {
		t.Fatalf("frame Err = %v, want context.Canceled", parent.Err())
	}
	// An already-admitted frame can still settle and refund.
	if err := parent.Fail(errors.New("cleanup"), parent.Charge()); err != nil {
		t.Fatalf("fail after cancel: %v", err)
	}
	assertBudget(t, c, 0)
}

func TestDeadlineSharedAcrossNestedFrames(t *testing.T) {
	base := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	clock := execctx.NewFakeClock(base)
	c := execctx.New(execctx.Config{
		Budget:   100,
		Deadline: base.Add(50 * time.Millisecond),
		Clock:    clock,
	})
	parent, err := c.Enter(nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(49 * time.Millisecond)
	if c.Canceled() {
		t.Fatal("canceled before deadline")
	}
	child, err := c.Enter(parent, 10)
	if err != nil {
		t.Fatalf("nested entry before deadline: %v", err)
	}
	clock.Advance(2 * time.Millisecond)
	if !c.Canceled() {
		t.Fatal("deadline did not cancel the request")
	}
	if !errors.Is(c.CancelCause(), execctx.ErrDeadlineExceeded) {
		t.Fatalf("cause = %v", c.CancelCause())
	}
	if _, err := c.Enter(child, 1); !errors.Is(err, execctx.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want ErrDeadlineExceeded", err)
	}
	if !errors.Is(child.Err(), context.DeadlineExceeded) {
		t.Fatalf("frame Err = %v, want context.DeadlineExceeded", child.Err())
	}
	if dl, ok := child.Deadline(); !ok || !dl.Equal(base.Add(50*time.Millisecond)) {
		t.Fatalf("deadline = %v ok=%v", dl, ok)
	}
}

func TestBornCanceledWhenDeadlineInPast(t *testing.T) {
	c := execctx.New(execctx.Config{
		Budget:   10,
		Deadline: time.Now().Add(-time.Minute),
	})
	if !c.Canceled() {
		t.Fatal("expected born-canceled controller")
	}
	if _, err := c.Enter(nil, 1); !errors.Is(err, execctx.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want ErrDeadlineExceeded", err)
	}
}

// panicPlugin blows up inside the host wrapper.
type panicPlugin struct{ msg string }

func (p panicPlugin) Name() string { return "panic" }

func (panicPlugin) Invoke(context.Context, Host, int) error {
	panic("kaboom")
}

func TestPanicContainedAndRefunded(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 50})
	err := g.Dispatch(panicPlugin{msg: "kaboom"}, 0, 12)
	if err == nil {
		t.Fatal("expected panic error")
	}
	var pe *execctx.PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *PanicError", err)
	}
	assertBudget(t, g.Controller(), 0)
	if g.Controller().Inflight() != 0 {
		t.Fatalf("inflight = %d, want 0", g.Controller().Inflight())
	}
}

func TestConcurrentReentriesShareOneBudget(t *testing.T) {
	c := execctx.New(execctx.Config{Budget: 100})
	const goroutines, charge = 20, int64(10)
	var wg sync.WaitGroup
	var admitted, rejected atomic.Int64
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			f, err := c.Enter(nil, charge)
			if err != nil {
				if !errors.Is(err, execctx.ErrBudgetExhausted) {
					t.Errorf("unexpected err: %v", err)
				}
				rejected.Add(1)
				return
			}
			admitted.Add(1)
			if err := f.Complete(0); err != nil {
				t.Errorf("complete: %v", err)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 10 || rejected.Load() != 10 {
		t.Fatalf("admitted=%d rejected=%d, want 10/10", admitted.Load(), rejected.Load())
	}
	if c.Entries() != 10 || c.Consumed() != 100 {
		t.Fatalf("entries=%d consumed=%d", c.Entries(), c.Consumed())
	}
	assertBudget(t, c, 100)
	ev := c.Finish()
	if ev.Charged != 100 || ev.Refunded != 0 || ev.Inflight != 0 {
		t.Fatalf("end event = %+v", ev)
	}
}

func TestInvalidCharges(t *testing.T) {
	c := execctx.New(execctx.Config{Budget: 10})
	for _, amount := range []int64{0, -1} {
		if _, err := c.Enter(nil, amount); !errors.Is(err, execctx.ErrInvalidCharge) {
			t.Fatalf("amount=%d err=%v", amount, err)
		}
	}
}
