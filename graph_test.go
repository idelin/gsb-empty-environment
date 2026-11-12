package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// graph is a small in-memory resolution graph standing in for the recursive
// host. Each key maps to a value and a list of dependency subqueries; the
// dependencies are issued concurrently through the SubResolver.
type graph struct {
	mu       sync.Mutex
	nodes    map[Key]graphNode
	started  map[Key]int
	finished map[Key]int
	block    map[Key]chan struct{}
}

type graphNode struct {
	value string
	deps  []Key
	// block, when non-nil, is held open until closed to keep a query in flight.
	block bool
}

func newGraph() *graph {
	return &graph{
		nodes:    map[Key]graphNode{},
		started:  map[Key]int{},
		finished: map[Key]int{},
		block:    map[Key]chan struct{}{},
	}
}

func (g *graph) add(key Key, value string, deps ...Key) {
	g.nodes[key] = graphNode{value: value, deps: deps}
}

func (g *graph) addBlocking(key Key, release chan struct{}, deps ...Key) {
	g.nodes[key] = graphNode{value: key.Name + "-ok", deps: deps, block: true}
	g.block[key] = release
}

func (g *graph) Resolve(ctx context.Context, sub SubResolver, key Key) (string, error) {
	g.mu.Lock()
	node, ok := g.nodes[key]
	g.started[key]++
	gate := g.block[key]
	g.mu.Unlock()

	if !ok {
		node = graphNode{value: key.Name}
	}

	if node.block && gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	if len(node.deps) > 0 {
		errCh := make(chan error, len(node.deps))
		var wg sync.WaitGroup
		for _, dep := range node.deps {
			wg.Add(1)
			go func(d Key) {
				defer wg.Done()
				if _, err := sub.Resolve(ctx, d); err != nil {
					errCh <- err
				}
			}(dep)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			if err != nil {
				return "", err
			}
		}
	}

	g.mu.Lock()
	g.finished[key]++
	g.mu.Unlock()
	return node.value, nil
}

func (g *graph) countStarted(key Key) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.started[key]
}

func (g *graph) countFinished(key Key) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.finished[key]
}

func k(kind Kind, name string) Key { return Key{Kind: kind, Name: name} }

// waitFor polls cond until it becomes true or the timeout elapses.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}

func TestBudgetTreeSharesRootTotal(t *testing.T) {
	root := NewRootBudget(3)
	if got := root.Remaining(); got != 3 {
		t.Fatalf("initial remaining = %d, want 3", got)
	}

	ns := root.Child()     // nameserver address branch
	alias := root.Child()  // alias chase branch
	grand := alias.Child() // deeper alias hop

	if err := ns.Acquire(); err != nil {
		t.Fatalf("ns acquire: %v", err)
	}
	if err := alias.Acquire(); err != nil {
		t.Fatalf("alias acquire: %v", err)
	}
	if err := grand.Acquire(); err != nil {
		t.Fatalf("grand acquire: %v", err)
	}

	if got := root.TotalUsed(); got != 3 {
		t.Fatalf("total used = %d, want 3", got)
	}
	if got := root.Remaining(); got != 0 {
		t.Fatalf("remaining = %d, want 0", got)
	}
	if err := ns.Acquire(); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("fourth acquire err = %v, want ErrBudgetExhausted", err)
	}

	ns.Release(1)
	if got := root.Remaining(); got != 1 {
		t.Fatalf("remaining after release = %d, want 1", got)
	}
	if ns.TryAcquire(2) {
		t.Fatal("TryAcquire beyond limit reported success")
	}
	if got := root.Remaining(); got != 1 {
		t.Fatalf("failed TryAcquire charged budget, remaining = %d", got)
	}
	if !ns.TryAcquire(1) {
		t.Fatal("TryAcquire fitting the remaining budget failed")
	}
	if ns.TryAcquire(1) {
		t.Fatal("TryAcquire past the spent total reported success")
	}
	if root.Children() != 3 {
		t.Fatalf("children = %d, want 3", root.Children())
	}
}

func TestMultiRootsShareCoalescedSubquery(t *testing.T) {
	g := newGraph()
	shared := k(KindNS, "ns.glow.")
	release := make(chan struct{})
	g.addBlocking(shared, release)
	g.add(k(KindName, "root-a."), "a", shared)
	g.add(k(KindName, "root-b."), "b", shared)

	eng := NewEngine(g, -1) // unlimited engine budget; roots carry their own
	budgetA := NewRootBudget(8)
	budgetB := NewRootBudget(8)

	type outcome struct {
		val string
		err error
	}
	resCh := make(chan outcome, 2)
	start := make(chan struct{})

	go func() {
		<-start
		v, err := eng.Resolve(WithBudget(context.Background(), budgetA), k(KindName, "root-a."))
		resCh <- outcome{v, err}
	}()
	go func() {
		<-start
		v, err := eng.Resolve(WithBudget(context.Background(), budgetB), k(KindName, "root-b."))
		resCh <- outcome{v, err}
	}()

	close(start)

	if !waitFor(2*time.Second, func() bool { return g.countStarted(shared) > 0 }) {
		t.Fatal("shared subquery never started")
	}

	if got := g.countStarted(shared); got != 1 {
		t.Fatalf("shared query started %d times, want 1", got)
	}
	if eng.Flights() < 1 {
		t.Fatal("expected an in-flight shared query")
	}

	close(release)

	for i := 0; i < 2; i++ {
		select {
		case r := <-resCh:
			if r.err != nil {
				t.Fatalf("root %d resolve: %v", i, r.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("root resolution did not finish")
		}
	}

	if got := g.countStarted(shared); got != 1 {
		t.Fatalf("shared query started %d times overall, want 1", got)
	}
	if g.countFinished(shared) != 1 {
		t.Fatalf("shared query finished %d times, want 1", g.countFinished(shared))
	}
	// Each root budget is charged only for the queries its own tree drove;
	// the merged subquery is charged once, to whichever tree drove it.
	total := budgetA.TotalUsed() + budgetB.TotalUsed()
	if total != 3 {
		t.Fatalf("combined budget charge = %d, want 3 (two roots + one shared)", total)
	}
}

func TestAllWaitersCancelStopsSharedQuery(t *testing.T) {
	g := newGraph()
	shared := k(KindNS, "stuck.ns.")
	release := make(chan struct{})
	g.addBlocking(shared, release)

	eng := NewEngine(g, -1)

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())

	type out struct{ err error }
	resCh := make(chan out, 2)
	go func() {
		_, err := eng.Resolve(ctx1, shared)
		resCh <- out{err}
	}()
	go func() {
		_, err := eng.Resolve(ctx2, shared)
		resCh <- out{err}
	}()

	if !waitFor(2*time.Second, func() bool { return g.countStarted(shared) > 0 }) {
		t.Fatal("shared subquery never started")
	}
	if got := g.countStarted(shared); got != 1 {
		t.Fatalf("shared query started %d times, want 1", got)
	}

	// Independent cancellation: cancelling one waiter leaves the query alive.
	cancel1()
	select {
	case r := <-resCh:
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("waiter1 err = %v, want context.Canceled", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter did not return")
	}

	if eng.Flights() != 1 {
		t.Fatalf("flights after single cancel = %d, want 1", eng.Flights())
	}

	// Last waiter leaving tears down the shared driver.
	cancel2()
	select {
	case r := <-resCh:
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("waiter2 err = %v, want context.Canceled", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second waiter did not return")
	}

	if !waitFor(2*time.Second, func() bool { return eng.Flights() == 0 }) {
		t.Fatalf("flight still present after all waiters cancelled: %d", eng.Flights())
	}

	// The driver was cancelled before the gate opened, so it must not finish.
	if g.countFinished(shared) != 0 {
		t.Fatalf("cancelled shared query finished %d times, want 0", g.countFinished(shared))
	}

	// A fresh resolution after cancellation starts a brand-new flight.
	go func() { _, _ = eng.Resolve(context.Background(), shared) }()
	if !waitFor(2*time.Second, func() bool { return g.countStarted(shared) == 2 }) {
		t.Fatalf("fresh flight did not start, started=%d", g.countStarted(shared))
	}
	close(release)
}

func TestCycleReturnsWitnessWithoutDeadlock(t *testing.T) {
	g := newGraph()
	a := k(KindAlias, "a.")
	b := k(KindAlias, "b.")
	c := k(KindNS, "c.")
	g.add(a, "a", b)
	g.add(b, "b", c)
	g.add(c, "c", a) // c loops back into the ancestor a

	eng := NewEngine(g, -1)
	done := make(chan error, 1)
	go func() {
		_, err := eng.Resolve(context.Background(), a)
		done <- err
	}()

	select {
	case err := <-done:
		var ce *CycleError
		if !errors.As(err, &ce) {
			t.Fatalf("err = %v, want *CycleError", err)
		}
		if len(ce.Witness) < 2 {
			t.Fatalf("witness too short: %v", ce.Witness)
		}
		if ce.Witness[0] != a || ce.Witness[len(ce.Witness)-1] != a {
			t.Fatalf("witness %v must start and end at %s", ce.Witness, a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cycle resolution deadlocked instead of returning a witness")
	}

	// A direct self-loop must also fail immediately rather than wait on itself.
	self := k(KindName, "loop.")
	g.add(self, "loop", self)
	_, err := eng.Resolve(context.Background(), self)
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("self-loop err = %v, want *CycleError", err)
	}
	if len(ce.Witness) != 2 || ce.Witness[0] != self || ce.Witness[1] != self {
		t.Fatalf("self-loop witness = %v", ce.Witness)
	}
}

func TestBudgetExhaustionStopsDelegationChain(t *testing.T) {
	g := newGraph()
	const depth = 6
	var first Key
	var prev Key
	for i := 0; i < depth; i++ {
		key := k(KindName, fmt.Sprintf("hop%d.", i))
		if i == 0 {
			first = key
		} else {
			g.add(prev, prev.Name, key)
		}
		prev = key
	}
	g.add(prev, prev.Name) // terminal leaf

	const limit = 3
	eng := NewEngine(g, limit)

	_, err := eng.Resolve(context.Background(), first)
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}

	if got := eng.budget.TotalUsed(); got != limit {
		t.Fatalf("total used = %d, want %d", got, limit)
	}

	// Exactly limit driver goroutines charged the budget; the fourth hop
	// never started.
	started := 0
	for i := 0; i < depth; i++ {
		started += g.countStarted(k(KindName, fmt.Sprintf("hop%d.", i)))
	}
	if started != limit {
		t.Fatalf("started %d subqueries, want %d", started, limit)
	}

	// An engine with a zero budget rejects even the root query.
	zeroEng := NewEngine(newGraph(), 0)
	if zeroEng.budget.limit != 0 {
		t.Fatalf("zero engine limit = %d, want 0", zeroEng.budget.limit)
	}
	_, err = zeroEng.Resolve(context.Background(), k(KindName, "x."))
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("zero-budget err = %v, want ErrBudgetExhausted", err)
	}
}
