package compute

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingFunc returns a Func that counts invocations, signals start, and
// blocks until release is closed (or ctx canceled).
func blockingFunc(started chan<- struct{}, release <-chan struct{}, val any) (Func, *atomic.Int32) {
	var calls atomic.Int32
	return func(ctx context.Context, deps ...any) (any, error) {
		calls.Add(1)
		if started != nil {
			close(started)
			started = nil
		}
		select {
		case <-release:
			return val, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, &calls
}

func adder(key string) Func {
	return func(ctx context.Context, deps ...any) (any, error) {
		sum := 0
		for _, d := range deps {
			sum += d.(int)
		}
		return sum, nil
	}
}

// buildDiamond wires a; b<-a; c<-a; d<-b,c and returns the graph plus the
// invocation counter of a's compute function.
func buildDiamond(t *testing.T, aFn Func) *Graph {
	t.Helper()
	g := New()
	if aFn == nil {
		aFn = func(ctx context.Context, deps ...any) (any, error) {
			return 1, nil
		}
	}
	if err := g.Add("a", nil, aFn); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("b", []string{"a"}, adder("b")); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("c", []string{"a"}, adder("c")); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("d", []string{"b", "c"}, adder("d")); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestDiamondSharing(t *testing.T) {
	var aCalls atomic.Int32
	aFn := func(ctx context.Context, deps ...any) (any, error) {
		aCalls.Add(1)
		return 1, nil
	}
	g := buildDiamond(t, aFn)

	sub, err := g.Subscribe("d")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	// Diamond bottom "a" holds one reference per parent edge.
	if got := g.Refs("a"); got != 2 {
		t.Fatalf("Refs(a) = %d, want 2", got)
	}
	for _, k := range []string{"b", "c", "d"} {
		if got := g.Refs(k); got != 1 {
			t.Fatalf("Refs(%s) = %d, want 1", k, got)
		}
	}

	vals, err := sub.Results(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if vals[0].(int) != 2 {
		t.Fatalf("d = %v, want 2 (b=1 + c=1)", vals[0])
	}
	if got := aCalls.Load(); got != 1 {
		t.Fatalf("a computed %d times, want 1 (shared by b and c)", got)
	}

	// A second overlapping subscription reuses the cached generation.
	sub2, err := g.Subscribe("d")
	if err != nil {
		t.Fatal(err)
	}
	defer sub2.Close()
	if _, err := sub2.Results(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := aCalls.Load(); got != 1 {
		t.Fatalf("a recomputed while still referenced: %d calls", got)
	}
	if got := g.Refs("a"); got != 4 {
		t.Fatalf("Refs(a) = %d, want 4 with two subscribers", got)
	}
}

func TestSubscriberExitReleasesOnlyItsReference(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	aFn, aCalls := blockingFunc(started, release, 1)
	g := buildDiamond(t, aFn)

	sub1, err := g.Subscribe("d")
	if err != nil {
		t.Fatal(err)
	}
	sub2, err := g.Subscribe("d")
	if err != nil {
		t.Fatal(err)
	}
	<-started

	// First subscriber exits while "a" is still running: only its
	// references are released, the shared computation keeps running.
	sub1.Close()
	if got := g.Refs("a"); got != 2 {
		t.Fatalf("after first Close, Refs(a) = %d, want 2", got)
	}
	if got := g.Refs("d"); got != 1 {
		t.Fatalf("after first Close, Refs(d) = %d, want 1", got)
	}
	if got := aCalls.Load(); got != 1 {
		t.Fatalf("a restarted on partial release: %d calls", got)
	}

	close(release)
	vals, err := sub2.Results(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if vals[0].(int) != 2 {
		t.Fatalf("d = %v, want 2", vals[0])
	}

	// Last release drops everything.
	sub2.Close()
	for _, k := range []string{"a", "b", "c", "d"} {
		if got := g.Refs(k); got != 0 {
			t.Fatalf("after final Close, Refs(%s) = %d, want 0", k, got)
		}
	}
}

func TestCancelAtZeroRefsThenNewGeneration(t *testing.T) {
	release := make(chan struct{})
	var aCalls atomic.Int32
	aCtxs := make(chan context.Context, 4)
	aFn := func(ctx context.Context, deps ...any) (any, error) {
		call := aCalls.Add(1)
		aCtxs <- ctx
		select {
		case <-release:
			return int(call), nil
		case <-ctx.Done():
			// Canceled: pretend a late result arrives anyway.
			return int(call), nil
		}
	}
	g := buildDiamond(t, aFn)

	sub1, err := g.Subscribe("d")
	if err != nil {
		t.Fatal(err)
	}
	ctx1 := <-aCtxs
	// Dropping the last reference cancels the in-flight generation.
	sub1.Close()
	select {
	case <-ctx1.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight computation not canceled at zero refs")
	}
	close(release) // let the orphaned goroutine finish; its result must be discarded

	// A new subscription starts a fresh generation with a fresh context.
	sub2, err := g.Subscribe("d")
	if err != nil {
		t.Fatal(err)
	}
	defer sub2.Close()
	ctx2 := <-aCtxs
	select {
	case <-ctx2.Done():
		t.Fatal("new generation's context already canceled")
	default:
	}
	vals, err := sub2.Results(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if vals[0].(int) != 4 { // b=2 + c=2 from a's second generation
		t.Fatalf("d = %v, want 4 from recomputed generation", vals[0])
	}
	if got := aCalls.Load(); got != 2 {
		t.Fatalf("a computed %d times, want 2", got)
	}
}

func TestFailurePropagatesToAllSubscribers(t *testing.T) {
	errBoom := errors.New("boom")
	var aCalls atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	aFn := func(ctx context.Context, deps ...any) (any, error) {
		aCalls.Add(1)
		if fail.Load() {
			return nil, errBoom
		}
		return 1, nil
	}
	g := buildDiamond(t, aFn)

	subA, err := g.Subscribe("a")
	if err != nil {
		t.Fatal(err)
	}
	defer subA.Close()
	// Subscribe to both "c" and "d": awaiting both guarantees the whole
	// failing subgraph has settled before the retry below, because d's
	// failure can propagate before its sibling c's generation finishes.
	subD, err := g.Subscribe("c", "d")
	if err != nil {
		t.Fatal(err)
	}
	defer subD.Close()

	// Every still-subscribed waiter observes the shared failure.
	if _, err := subA.Results(context.Background()); !errors.Is(err, errBoom) {
		t.Fatalf("subscriber of a got %v, want errBoom", err)
	}
	if _, err := subD.Results(context.Background()); !errors.Is(err, errBoom) {
		t.Fatalf("subscriber of d got %v, want errBoom", err)
	}
	if got := aCalls.Load(); got != 1 {
		t.Fatalf("a ran %d times, want 1 shared failed generation", got)
	}

	// Failure is not cached: a new subscription starts a new generation.
	fail.Store(false)
	sub2, err := g.Subscribe("d")
	if err != nil {
		t.Fatal(err)
	}
	defer sub2.Close()
	vals, err := sub2.Results(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if vals[0].(int) != 2 {
		t.Fatalf("d = %v, want 2 after retry", vals[0])
	}
	if got := aCalls.Load(); got != 2 {
		t.Fatalf("a ran %d times, want 2 (new generation after failure)", got)
	}

	// The old subscribers observe the fresh generation too.
	vals, err = subD.Results(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if vals[1].(int) != 2 {
		t.Fatalf("retried d = %v, want 2", vals[1])
	}
}

func TestCycleRejection(t *testing.T) {
	g := New()
	noop := adder("noop")

	// Forward references are allowed...
	if err := g.Add("a", []string{"b"}, noop); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("b", []string{"c"}, noop); err != nil {
		t.Fatal(err)
	}
	// ...but closing the loop back to "a" is rejected.
	if err := g.Add("c", []string{"a"}, noop); !errors.Is(err, ErrCycle) {
		t.Fatalf("Add(c<-a) = %v, want ErrCycle", err)
	}
	// The rejected node is rolled back.
	if _, err := g.Subscribe("c"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("Subscribe(c) = %v, want ErrUnknownNode", err)
	}
	// "c" can still be defined with acyclic edges.
	if err := g.Add("c", nil, noop); err != nil {
		t.Fatal(err)
	}

	// Self-loop.
	if err := g.Add("s", []string{"s"}, noop); !errors.Is(err, ErrCycle) {
		t.Fatalf("Add(s<-s) = %v, want ErrCycle", err)
	}

	// Two-node cycle through a forward reference.
	if err := g.Add("x", []string{"y"}, noop); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("y", []string{"x"}, noop); !errors.Is(err, ErrCycle) {
		t.Fatalf("Add(y<-x) = %v, want ErrCycle", err)
	}
	if _, err := g.Subscribe("y"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("Subscribe(y) = %v, want ErrUnknownNode", err)
	}
}

func TestConcurrentSubscribeClose(t *testing.T) {
	g := buildDiamond(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				sub, err := g.Subscribe("d")
				if err != nil {
					t.Error(err)
					return
				}
				if j%3 == 0 {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					sub.Results(ctx)
					cancel()
				}
				sub.Close()
			}
		}(i)
	}
	wg.Wait()
	for _, k := range []string{"a", "b", "c", "d"} {
		if got := g.Refs(k); got != 0 {
			t.Fatalf("Refs(%s) = %d after churn, want 0", k, got)
		}
	}
}

func TestMultiRootSubscription(t *testing.T) {
	g := New()
	if err := g.Add("a", nil, func(ctx context.Context, deps ...any) (any, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("b", []string{"a"}, adder("b")); err != nil {
		t.Fatal(err)
	}
	sub, err := g.Subscribe("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	vals, err := sub.Results(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if vals[0].(int) != 1 || vals[1].(int) != 1 {
		t.Fatalf("got %v, want [1 1]", vals)
	}
	// "a" is referenced once directly and once through "b".
	if got := g.Refs("a"); got != 2 {
		t.Fatalf("Refs(a) = %d, want 2", got)
	}
}

func TestResultsAfterClose(t *testing.T) {
	g := buildDiamond(t, nil)
	sub, err := g.Subscribe("d")
	if err != nil {
		t.Fatal(err)
	}
	sub.Close()
	sub.Close() // idempotent
	if _, err := sub.Results(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Results after Close = %v, want ErrClosed", err)
	}
}

func TestAddValidation(t *testing.T) {
	g := New()
	noop := adder("noop")
	if err := g.Add("n", nil, nil); !errors.Is(err, ErrNilFunc) {
		t.Fatalf("Add nil func = %v, want ErrNilFunc", err)
	}
	if err := g.Add("n", nil, noop); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("n", nil, noop); !errors.Is(err, ErrDuplicateNode) {
		t.Fatalf("re-Add = %v, want ErrDuplicateNode", err)
	}
	if _, err := g.Subscribe("missing"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("Subscribe(missing) = %v, want ErrUnknownNode", err)
	}
	// Missing transitive dependency is reported at subscribe time.
	if err := g.Add("p", []string{"q"}, noop); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Subscribe("p"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("Subscribe(p) = %v, want ErrUnknownNode", err)
	}
}
