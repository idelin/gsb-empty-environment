package taskgraph_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"taskgraph"
)

// probe is a controllable constructor: it counts invocations, can
// block until released, and can be toggled between failing and
// succeeding so tests script each construction attempt.
type probe struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{} // buffered; receives one signal per build
	release chan struct{} // if non-nil, builds block until it is closed
	fail    bool
	value   interface{}
}

func (p *probe) build(ctx context.Context) (interface{}, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if p.entered != nil {
		p.entered <- struct{}{}
	}
	if p.release != nil {
		<-p.release
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return nil, errors.New("construction failed")
	}
	return p.value, nil
}

func (p *probe) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// do runs fn in a goroutine and fails the test if it does not return
// within the timeout, guarding against future-wait deadlocks.
func do(t *testing.T, fn func() (interface{}, error)) (interface{}, error) {
	t.Helper()
	type outcome struct {
		v   interface{}
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		v, err := fn()
		ch <- outcome{v, err}
	}()
	select {
	case o := <-ch:
		return o.v, o.err
	case <-time.After(5 * time.Second):
		t.Fatal("Do did not return: possible deadlock waiting on a future")
		return nil, nil
	}
}

// Diamond: A depends on B and C (resolved concurrently), and both B
// and C depend on D. D must be constructed exactly once and its result
// shared by both branches.
func TestDiamondSharing(t *testing.T) {
	r := taskgraph.New()
	d := &probe{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		value:   "D",
	}

	get := func(ctx context.Context, key string, build taskgraph.Constructor) (interface{}, error) {
		return r.Do(ctx, key, build)
	}
	buildD := d.build
	buildB := func(ctx context.Context) (interface{}, error) {
		v, err := get(ctx, "D", buildD)
		if err != nil {
			return nil, err
		}
		return "B(" + v.(string) + ")", nil
	}
	buildC := func(ctx context.Context) (interface{}, error) {
		v, err := get(ctx, "D", buildD)
		if err != nil {
			return nil, err
		}
		return "C(" + v.(string) + ")", nil
	}
	buildA := func(ctx context.Context) (interface{}, error) {
		var wg sync.WaitGroup
		var bv, cv interface{}
		var berr, cerr error
		wg.Add(2)
		go func() { defer wg.Done(); bv, berr = get(ctx, "B", buildB) }()
		go func() { defer wg.Done(); cv, cerr = get(ctx, "C", buildC) }()
		wg.Wait()
		if berr != nil {
			return nil, berr
		}
		if cerr != nil {
			return nil, cerr
		}
		return "A(" + bv.(string) + "," + cv.(string) + ")", nil
	}

	resultCh := make(chan interface{}, 1)
	errCh := make(chan error, 1)
	go func() {
		v, err := r.Do(context.Background(), "A", buildA)
		resultCh <- v
		errCh <- err
	}()

	// D's construction has started; give both branches a chance to
	// pile onto the same future before releasing it.
	<-d.entered
	time.Sleep(50 * time.Millisecond)
	close(d.release)

	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := <-resultCh, "A(B(D),C(D))"; got != want {
		t.Fatalf("result = %v, want %v", got, want)
	}
	if n := d.callCount(); n != 1 {
		t.Fatalf("D constructed %d times, want 1 (shared across the diamond)", n)
	}

	// An independent later request for D reuses the memoized result.
	v, err := do(t, func() (interface{}, error) {
		return r.Do(context.Background(), "D", d.build)
	})
	if err != nil || v != "D" {
		t.Fatalf("independent request = %v, %v; want D, nil", v, err)
	}
	if n := d.callCount(); n != 1 {
		t.Fatalf("D reconstructed for an independent request; calls = %d", n)
	}
}

// Failure: concurrent callers share one failed attempt; the failure is
// not memoized, so a later request retries and succeeds, after which
// the success is memoized.
func TestRetryAfterFailure(t *testing.T) {
	r := taskgraph.New()
	p := &probe{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		fail:    true,
	}

	const waiters = 4
	var wg sync.WaitGroup
	errs := make([]error, waiters)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = r.Do(context.Background(), "k", p.build)
		}(i)
	}
	<-p.entered
	time.Sleep(50 * time.Millisecond) // let all waiters attach to the future
	close(p.release)
	wg.Wait()

	for i, err := range errs {
		if err == nil {
			t.Fatalf("waiter %d got nil error, want shared failure", i)
		}
	}
	if n := p.callCount(); n != 1 {
		t.Fatalf("failing constructor ran %d times for %d concurrent callers, want 1", n, waiters)
	}

	// The failure was evicted: the next request retries.
	p.mu.Lock()
	p.fail = false
	p.value = "recovered"
	p.mu.Unlock()
	v, err := do(t, func() (interface{}, error) {
		return r.Do(context.Background(), "k", p.build)
	})
	if err != nil || v != "recovered" {
		t.Fatalf("retry = %v, %v; want recovered, nil", v, err)
	}
	if n := p.callCount(); n != 2 {
		t.Fatalf("constructor ran %d times after retry, want 2", n)
	}

	// The success is memoized: no third construction.
	v, err = do(t, func() (interface{}, error) {
		return r.Do(context.Background(), "k", p.build)
	})
	if err != nil || v != "recovered" {
		t.Fatalf("memoized request = %v, %v; want recovered, nil", v, err)
	}
	if n := p.callCount(); n != 2 {
		t.Fatalf("success not memoized; constructor ran %d times", n)
	}
}

// Self-cycle: a task depending on itself must get a CycleError
// immediately instead of waiting on its own future.
func TestSelfCycle(t *testing.T) {
	r := taskgraph.New()
	var build taskgraph.Constructor
	build = func(ctx context.Context) (interface{}, error) {
		return r.Do(ctx, "A", build)
	}
	_, err := do(t, func() (interface{}, error) {
		return r.Do(context.Background(), "A", build)
	})
	c, ok := taskgraph.AsCycle(err)
	if !ok {
		t.Fatalf("err = %v, want CycleError", err)
	}
	if want := []string{"A", "A"}; !equalStrings(c.Path, want) {
		t.Fatalf("cycle path = %v, want %v", c.Path, want)
	}
}

// Ancestor cycle: A -> B -> A must be reported with the full path, and
// the failed constructions must be evicted so a later attempt retries
// (and reports the cycle again) rather than hanging.
func TestAncestorCycle(t *testing.T) {
	r := taskgraph.New()
	var buildA, buildB taskgraph.Constructor
	buildA = func(ctx context.Context) (interface{}, error) {
		return r.Do(ctx, "B", buildB)
	}
	buildB = func(ctx context.Context) (interface{}, error) {
		return r.Do(ctx, "A", buildA)
	}
	for attempt := 0; attempt < 2; attempt++ {
		_, err := do(t, func() (interface{}, error) {
			return r.Do(context.Background(), "A", buildA)
		})
		c, ok := taskgraph.AsCycle(err)
		if !ok {
			t.Fatalf("attempt %d: err = %v, want CycleError", attempt, err)
		}
		if want := []string{"A", "B", "A"}; !equalStrings(c.Path, want) {
			t.Fatalf("attempt %d: cycle path = %v, want %v", attempt, c.Path, want)
		}
	}
}

// Canonical encoding: parameter tuples that would collide under naive
// concatenation must produce distinct keys and independent tasks.
func TestParameterEncodingCollision(t *testing.T) {
	type pair struct{ X, Y string }

	cases := []struct {
		name string
		a, b string
	}{
		{"split boundary", taskgraph.Key("pair", pair{"a", "bc"}), taskgraph.Key("pair", pair{"ab", "c"})},
		{"name boundary", taskgraph.Key("ab", "c"), taskgraph.Key("a", "bc")},
		{"variadic boundary", taskgraph.Key("t", "a", "bc"), taskgraph.Key("t", "ab", "c")},
		{"type confusion", taskgraph.Key("t", 1), taskgraph.Key("t", "1")},
	}
	for _, tc := range cases {
		if tc.a == tc.b {
			t.Fatalf("%s: keys collide: %q", tc.name, tc.a)
		}
	}
	if taskgraph.Key("pair", pair{"a", "bc"}) != taskgraph.Key("pair", pair{"a", "bc"}) {
		t.Fatal("identical parameters produced different keys")
	}

	// Runner level: colliding-under-concat params build independently,
	// identical params share.
	r := taskgraph.New()
	p := &probe{value: "v"}
	k1 := taskgraph.Key("pair", pair{"a", "bc"})
	k2 := taskgraph.Key("pair", pair{"ab", "c"})
	for _, k := range []string{k1, k2, k1, k2} {
		if _, err := r.Do(context.Background(), k, p.build); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if n := p.callCount(); n != 2 {
		t.Fatalf("constructor ran %d times, want 2 (one per distinct parameter tuple)", n)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
