package execctx_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"example.org/netsec/q0109/execctx"
)

const frameKey = "execctx-test-frame"

// Plugin is the untrusted extension point exercised by the tests.
type Plugin interface {
	Name() string
	Invoke(ctx context.Context, host Host, payload int) error
}

// Host is the callback surface the gateway offers to plugins.
type Host interface {
	Reenter(ctx context.Context, plugin Plugin, payload int, charge int64) error
	FrameOf(ctx context.Context) *execctx.Frame
	Controller() *execctx.Controller
}

// Gateway is the minimal host: every plugin call, including callbacks, is a
// fresh charged entry on the same Controller. The gateway owns settlement, so
// a plugin that settles its own frame simply makes the wrapper a no-op.
type Gateway struct {
	ctrl *execctx.Controller
	mu   sync.Mutex
	ends []execctx.EndEvent
}

func NewGateway(cfg execctx.Config) *Gateway {
	return &Gateway{ctrl: execctx.New(cfg)}
}

func (g *Gateway) Controller() *execctx.Controller { return g.ctrl }

func (g *Gateway) FrameOf(ctx context.Context) *execctx.Frame {
	f, _ := ctx.Value(frameKey).(*execctx.Frame)
	return f
}

// Dispatch starts a top-level plugin invocation.
func (g *Gateway) Dispatch(p Plugin, payload int, charge int64) error {
	return g.invoke(nil, p, payload, charge)
}

func (g *Gateway) Reenter(ctx context.Context, p Plugin, payload int, charge int64) error {
	return g.invoke(g.FrameOf(ctx), p, payload, charge)
}

func (g *Gateway) invoke(parent *execctx.Frame, p Plugin, payload int, charge int64) (err error) {
	f, err := g.ctrl.Enter(parent, charge)
	if err != nil {
		return fmt.Errorf("admit %s: %w", p.Name(), err)
	}
	ctx := context.WithValue(context.Background(), frameKey, f)
	defer func() {
		if r := recover(); r != nil {
			pe := &execctx.PanicError{Value: r}
			_ = f.Fail(pe, f.Charge())
			err = pe
		}
	}()
	ierr := p.Invoke(ctx, g, payload)
	switch {
	case f.State() != execctx.StateActive:
		return ierr
	case ierr != nil:
		_ = f.Fail(ierr, f.Charge())
		return ierr
	default:
		_ = f.Complete(0)
		return nil
	}
}

// Finish closes the request and records the single end event.
func (g *Gateway) Finish() execctx.EndEvent {
	ev := g.ctrl.Finish()
	g.mu.Lock()
	g.ends = append(g.ends, ev)
	g.mu.Unlock()
	return ev
}

func (g *Gateway) endCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.ends)
}

func assertBudget(t *testing.T, c *execctx.Controller, wantConsumed int64) {
	t.Helper()
	if got := c.Consumed(); got != wantConsumed {
		t.Fatalf("consumed = %d, want %d (charged=%d refunded=%d)",
			got, wantConsumed, c.Charged(), c.Refunded())
	}
	if c.Consumed() > c.Budget() {
		t.Fatalf("consumed %d exceeds budget %d", c.Consumed(), c.Budget())
	}
	if c.Charged()-c.Refunded() != c.Consumed() {
		t.Fatalf("accounting invariant broken: %d-%d != %d",
			c.Charged(), c.Refunded(), c.Consumed())
	}
}

// recursivePlugin reenters the same request depth times, charging every
// entry first. The leaf returns an error; each frame's refund policy is set
// by the plugin.
type recursivePlugin struct {
	depth        int
	charge       int64
	refundOnFail bool
}

func (p *recursivePlugin) Name() string { return "recursive" }

func (p *recursivePlugin) Invoke(ctx context.Context, host Host, _ int) error {
	f := host.FrameOf(ctx)
	if f.Depth() >= p.depth {
		return errors.New("leaf failure")
	}
	return host.Reenter(ctx, p, 0, p.charge)
}

func TestRecursiveReentryChargesEveryLevel(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 100})
	const depth, charge = 4, int64(10)
	err := g.Dispatch(&recursivePlugin{depth: depth, charge: charge}, 0, charge)
	if err == nil {
		t.Fatal("expected leaf failure to propagate")
	}
	c := g.Controller()
	if c.Entries() != depth {
		t.Fatalf("entries = %d, want %d", c.Entries(), depth)
	}
	// Every frame failed and was fully refunded by the gateway wrapper.
	assertBudget(t, c, 0)
	if c.Refunded() != depth*charge {
		t.Fatalf("refunded = %d, want %d", c.Refunded(), depth*charge)
	}
	if c.Inflight() != 0 {
		t.Fatalf("inflight = %d, want 0", c.Inflight())
	}
}

func TestRecursiveReentryStopsAtBudget(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 25})
	p := &recursivePlugin{depth: 10, charge: 10, refundOnFail: true}
	err := g.Dispatch(p, 0, 10)
	if !errors.Is(err, execctx.ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
	c := g.Controller()
	if c.Entries() != 2 {
		t.Fatalf("entries = %d, want 2", c.Entries())
	}
	if c.Rejected() == 0 {
		t.Fatal("expected at least one rejected entry")
	}
	assertBudget(t, c, 0)
}

func TestMaxDepthRejectsDeepReentry(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 1000, MaxDepth: 3})
	err := g.Dispatch(&recursivePlugin{depth: 10, charge: 1}, 0, 1)
	if !errors.Is(err, execctx.ErrMaxDepth) {
		t.Fatalf("err = %v, want ErrMaxDepth", err)
	}
	if g.Controller().Entries() != 3 {
		t.Fatalf("entries = %d, want 3", g.Controller().Entries())
	}
}

// doubleCompletePlugin settles the same frame repeatedly and tries to refund
// twice.
type doubleCompletePlugin struct{}

func (doubleCompletePlugin) Name() string { return "double-complete" }

func (doubleCompletePlugin) Invoke(ctx context.Context, host Host, _ int) error {
	f := host.FrameOf(ctx)
	if err := f.Fail(errors.New("first failure"), f.Charge()); err != nil {
		return err
	}
	if err := f.Complete(f.Charge()); !errors.Is(err, execctx.ErrSettled) {
		return fmt.Errorf("second settle err = %v, want ErrSettled", err)
	}
	if err := f.Fail(errors.New("again"), f.Charge()); !errors.Is(err, execctx.ErrSettled) {
		return fmt.Errorf("third settle err = %v, want ErrSettled", err)
	}
	return nil
}

func TestDoubleCompletionSettlesAndRefundsOnce(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 50})
	if err := g.Dispatch(doubleCompletePlugin{}, 0, 30); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	c := g.Controller()
	if c.Entries() != 1 || c.Inflight() != 0 {
		t.Fatalf("entries=%d inflight=%d", c.Entries(), c.Inflight())
	}
	assertBudget(t, c, 0)
	if c.Refunded() != 30 {
		t.Fatalf("refunded = %d, want 30", c.Refunded())
	}
}

// overRefundPlugin asks back more than the frame was charged, then lets the
// gateway perform the legitimate failure settlement.
type overRefundPlugin struct{}

func (overRefundPlugin) Name() string { return "over-refund" }

func (overRefundPlugin) Invoke(ctx context.Context, host Host, _ int) error {
	f := host.FrameOf(ctx)
	if err := f.Fail(errors.New("boom"), f.Charge()+1); !errors.Is(err, execctx.ErrBadRefund) {
		return fmt.Errorf("err = %v, want ErrBadRefund", err)
	}
	if f.State() != execctx.StateActive {
		return fmt.Errorf("state = %s, want active", f.State())
	}
	return errors.New("boom")
}

func TestOverRefundRejectedThenValidFail(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 40})
	if err := g.Dispatch(overRefundPlugin{}, 0, 15); err == nil {
		t.Fatal("expected plugin failure")
	}
	assertBudget(t, g.Controller(), 0)
}

// nestedFailPlugin nests one failing leaf inside a healthy outer call. Only
// the leaf refunds; the outer frame completes and keeps its charge.
type nestedFailPlugin struct{ leafCharge int64 }

func (nestedFailPlugin) Name() string { return "nested-fail" }

func (p nestedFailPlugin) Invoke(ctx context.Context, host Host, depth int) error {
	if depth == 0 {
		return errors.New("leaf failed")
	}
	if err := host.Reenter(ctx, p, depth-1, p.leafCharge); err != nil {
		// The host swallows the child failure at the boundary; the outer
		// call still succeeds.
		return nil
	}
	return nil
}

func TestFailureRefundsLeafOnly(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 100})
	if err := g.Dispatch(nestedFailPlugin{leafCharge: 7}, 1, 10); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	c := g.Controller()
	if c.Entries() != 2 {
		t.Fatalf("entries = %d, want 2", c.Entries())
	}
	// Outer success keeps 10; the failed leaf's 7 is refunded.
	assertBudget(t, c, 10)
}

// settledParentPlugin completes its frame, then tries to reenter through it.
type settledParentPlugin struct{}

func (settledParentPlugin) Name() string { return "settled-parent" }

func (settledParentPlugin) Invoke(ctx context.Context, host Host, _ int) error {
	f := host.FrameOf(ctx)
	if err := f.Complete(0); err != nil {
		return err
	}
	if err := host.Reenter(ctx, settledParentPlugin{}, 0, 1); !errors.Is(err, execctx.ErrReentered) {
		return fmt.Errorf("reenter err = %v, want ErrReentered", err)
	}
	return nil
}

func TestReentryThroughSettledFrameRejected(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 10})
	if err := g.Dispatch(settledParentPlugin{}, 0, 5); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	assertBudget(t, g.Controller(), 5)
}

func TestUniqueEndEvent(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 10})
	const callers = 32
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			g.Finish()
		}()
	}
	wg.Wait()
	if g.endCount() != callers {
		t.Fatalf("recorded finishes = %d, want %d", g.endCount(), callers)
	}
	events := 0
	for {
		select {
		case <-g.Controller().End():
			events++
		default:
			goto done
		}
	}
done:
	if events != 1 {
		t.Fatalf("end channel events = %d, want 1", events)
	}
	if !g.Controller().Finished() {
		t.Fatal("controller not finished")
	}
}

func TestEntriesRejectedAfterFinish(t *testing.T) {
	g := NewGateway(execctx.Config{Budget: 100})
	g.Finish()
	err := g.Dispatch(&recursivePlugin{depth: 1, charge: 1}, 0, 1)
	if !errors.Is(err, execctx.ErrFinished) {
		t.Fatalf("err = %v, want ErrFinished", err)
	}
}
