package compute

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// buildDiamond creates A -> {B, C}, B -> D, C -> D and returns the
// nodes plus a run counter for D.
func buildDiamond(t *testing.T, dFn Func) (*Graph, *Node, *Node, *Node, *Node) {
	t.Helper()
	g := NewGraph()
	mk := func(name string) *Node {
		n, err := g.Node(name)
		if err != nil {
			t.Fatalf("node %s: %v", name, err)
		}
		return n
	}
	a, b, c, d := mk("A"), mk("B"), mk("C"), mk("D")
	for _, edge := range [][2]*Node{{b, d}, {c, d}, {a, b}, {a, c}} {
		if err := g.AddDependency(edge[0], edge[1]); err != nil {
			t.Fatalf("edge %s->%s: %v", edge[0].Name(), edge[1].Name(), err)
		}
	}
	d.SetFunc(dFn)
	b.SetFunc(func(ctx context.Context, deps []Result) (interface{}, error) {
		return deps[0].Val.(int) + 10, nil
	})
	c.SetFunc(func(ctx context.Context, deps []Result) (interface{}, error) {
		return deps[0].Val.(int) + 100, nil
	})
	a.SetFunc(func(ctx context.Context, deps []Result) (interface{}, error) {
		return deps[0].Val.(int) + deps[1].Val.(int), nil
	})
	return g, a, b, c, d
}

func TestDiamondSharing(t *testing.T) {
	var dRuns int32
	var dMaxRefs int32
	rt := NewRuntime()
	var dn *Node
	_, a, _, _, d := buildDiamond(t, func(ctx context.Context, deps []Result) (interface{}, error) {
		atomic.AddInt32(&dRuns, 1)
		// While D runs, both B and C must hold a reference to it.
		for {
			refs := int32(rt.Refs(dn))
			if refs > atomic.LoadInt32(&dMaxRefs) {
				atomic.StoreInt32(&dMaxRefs, refs)
			}
			if refs == 2 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		return 1, nil
	})
	dn = d

	sub := rt.Subscribe(a)
	val, err := sub.Wait()
	if err != nil {
		t.Fatalf("A failed: %v", err)
	}
	if want := (1 + 10) + (1 + 100); val.(int) != want {
		t.Fatalf("A = %v, want %d", val, want)
	}
	sub.Unsubscribe()

	if got := atomic.LoadInt32(&dRuns); got != 1 {
		t.Fatalf("D ran %d times, want 1 (shared by B and C)", got)
	}
	if got := atomic.LoadInt32(&dMaxRefs); got != 2 {
		t.Fatalf("D max refs = %d, want 2", got)
	}
	if got := rt.Running(); got != 0 {
		t.Fatalf("%d generations still running, want 0", got)
	}
}

func TestSubscriberExitReleasesOnlyItsReference(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	g := NewGraph()
	n, err := g.Node("N")
	if err != nil {
		t.Fatal(err)
	}
	n.SetFunc(func(ctx context.Context, deps []Result) (interface{}, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	rt := NewRuntime()

	s1 := rt.Subscribe(n)
	s2 := rt.Subscribe(n)
	<-started
	if got := rt.Refs(n); got != 2 {
		t.Fatalf("refs = %d, want 2", got)
	}

	// First subscriber leaves: the computation must keep running.
	s1.Unsubscribe()
	select {
	case <-cancelled:
		t.Fatal("computation cancelled while one subscriber remained")
	case <-time.After(50 * time.Millisecond):
	}
	if got := rt.Refs(n); got != 1 {
		t.Fatalf("refs = %d, want 1", got)
	}

	// Last subscriber leaves: zero references cancel the computation.
	s2.Unsubscribe()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("computation not cancelled at zero references")
	}
	if _, err := s2.Wait(); err != context.Canceled {
		t.Fatalf("Wait err = %v, want context.Canceled", err)
	}
	if got := rt.Running(); got != 0 {
		t.Fatalf("%d generations still running, want 0", got)
	}
}

func TestSharedFailurePropagatesToAllSubscribers(t *testing.T) {
	dErr := errors.New("D exploded")
	var dRuns int32
	rt := NewRuntime()
	_, a, b, c, _ := buildDiamond(t, func(ctx context.Context, deps []Result) (interface{}, error) {
		atomic.AddInt32(&dRuns, 1)
		time.Sleep(20 * time.Millisecond) // let B and C both join
		return nil, dErr
	})

	// Subscribe the whole diamond plus one direct subscriber of D.
	subs := []*Subscription{
		rt.Subscribe(a),
		rt.Subscribe(b),
		rt.Subscribe(c),
		rt.Subscribe(rt.gensGet(t, a, "D")),
	}
	for i, s := range subs {
		if _, err := s.Wait(); err != dErr {
			t.Fatalf("subscriber %d got err %v, want %v", i, err, dErr)
		}
		s.Unsubscribe()
	}
	if got := atomic.LoadInt32(&dRuns); got != 1 {
		t.Fatalf("D ran %d times, want 1", got)
	}
}

// gensGet resolves node D from the graph built by buildDiamond.
func (rt *Runtime) gensGet(t *testing.T, a *Node, name string) *Node {
	t.Helper()
	n := a.graph.Get(name)
	if n == nil {
		t.Fatalf("node %s not found", name)
	}
	return n
}

func TestNewSubscriptionStartsNewGeneration(t *testing.T) {
	var calls int32
	dErr := errors.New("first generation fails")
	g := NewGraph()
	d, err := g.Node("D")
	if err != nil {
		t.Fatal(err)
	}
	d.SetFunc(func(ctx context.Context, deps []Result) (interface{}, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return nil, dErr
		}
		return 42, nil
	})
	rt := NewRuntime()

	s1 := rt.Subscribe(d)
	if _, err := s1.Wait(); err != dErr {
		t.Fatalf("first generation err = %v, want %v", err, dErr)
	}
	// s1 is still subscribed, yet the failed generation is retired:
	// a new subscription must start a fresh generation.
	s2 := rt.Subscribe(d)
	val, err := s2.Wait()
	if err != nil || val.(int) != 42 {
		t.Fatalf("second generation = %v, %v; want 42, nil", val, err)
	}
	s1.Unsubscribe()
	s2.Unsubscribe()
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("D ran %d times, want 2", got)
	}
}

func TestCancelRaceWithNewGeneration(t *testing.T) {
	// Hammer subscribe/unsubscribe against short-lived computations to
	// exercise the race between last-unsubscribe cancellation, natural
	// completion, and a new generation replacing a stale one.
	var runs int32
	g := NewGraph()
	n, err := g.Node("N")
	if err != nil {
		t.Fatal(err)
	}
	n.SetFunc(func(ctx context.Context, deps []Result) (interface{}, error) {
		atomic.AddInt32(&runs, 1)
		select {
		case <-time.After(time.Millisecond):
			return 1, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	rt := NewRuntime()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s := rt.Subscribe(n)
				if j%3 == 0 {
					s.Unsubscribe() // may cancel a shared generation
					continue
				}
				s.Wait() // ignore result: canceled or computed
				s.Unsubscribe()
			}
		}()
	}
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for rt.Running() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := rt.Running(); got != 0 {
		t.Fatalf("%d generations leaked", got)
	}
	if got := atomic.LoadInt32(&runs); got == 0 {
		t.Fatal("computation never ran")
	}
}

func TestCycleRejection(t *testing.T) {
	g := NewGraph()
	mk := func(name string) *Node {
		n, err := g.Node(name)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	a, b, c, d := mk("A"), mk("B"), mk("C"), mk("D")

	mustOK := func(from, to *Node) {
		if err := g.AddDependency(from, to); err != nil {
			t.Fatalf("edge %s->%s rejected: %v", from.Name(), to.Name(), err)
		}
	}
	mustCycle := func(from, to *Node) {
		if err := g.AddDependency(from, to); err == nil {
			t.Fatalf("edge %s->%s accepted, want cycle rejection", from.Name(), to.Name())
		}
	}

	mustCycle(a, a) // self-loop
	mustOK(a, b)
	mustOK(b, c)
	mustCycle(c, a) // closes A->B->C->A
	mustCycle(c, b) // closes B->C->B
	mustOK(a, c)    // redundant edge, still acyclic
	mustOK(b, d)
	mustOK(c, d) // diamond shape is fine
	mustCycle(d, a)
	mustCycle(d, b)

	// Rejected edges must not have mutated the graph.
	if got := len(c.Deps()); got != 1 {
		t.Fatalf("C has %d deps, want 1 (only D)", got)
	}
	if got := len(d.Deps()); got != 0 {
		t.Fatalf("D has %d deps, want 0", got)
	}
}

func TestDependencyResultsAlignWithDepsOrder(t *testing.T) {
	g := NewGraph()
	mk := func(name string, val int) *Node {
		n, err := g.Node(name)
		if err != nil {
			t.Fatal(err)
		}
		v := val
		n.SetFunc(func(ctx context.Context, deps []Result) (interface{}, error) {
			return v, nil
		})
		return n
	}
	x := mk("X", 1)
	y := mk("Y", 2)
	sum, err := g.Node("SUM")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.AddDependency(sum, x); err != nil {
		t.Fatal(err)
	}
	if err := g.AddDependency(sum, y); err != nil {
		t.Fatal(err)
	}
	sum.SetFunc(func(ctx context.Context, deps []Result) (interface{}, error) {
		if deps[0].Node != x || deps[1].Node != y {
			t.Errorf("dep order = %s,%s; want X,Y", deps[0].Node.Name(), deps[1].Node.Name())
		}
		return deps[0].Val.(int) + deps[1].Val.(int), nil
	})

	rt := NewRuntime()
	s := rt.Subscribe(sum)
	val, err := s.Wait()
	if err != nil || val.(int) != 3 {
		t.Fatalf("SUM = %v, %v; want 3, nil", val, err)
	}
	s.Unsubscribe()
}
