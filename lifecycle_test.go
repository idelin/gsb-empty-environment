package lifecycle_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"lifecycle"
)

// recorder captures cleanup completion order, safe for concurrent use.
type recorder struct {
	mu    sync.Mutex
	names []string
}

func (r *recorder) record(name string) {
	r.mu.Lock()
	r.names = append(r.names, name)
	r.mu.Unlock()
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.names...)
}

// gate is a controllable cleanup: it signals when entered, then blocks
// until released (or ctx is cancelled), and finally returns err.
type gate struct {
	entered chan struct{}
	release chan struct{}
	err     error
}

func newGate(err error) *gate {
	return &gate{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		err:     err,
	}
}

func (g *gate) cleanup(rec *recorder, name string) lifecycle.Cleanup {
	return func(ctx context.Context) error {
		close(g.entered)
		select {
		case <-g.release:
		case <-ctx.Done():
		}
		rec.record(name)
		return g.err
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func indexOf(names []string, name string) int {
	for i, n := range names {
		if n == name {
			return i
		}
	}
	return -1
}

// Reverse dependency order: a chain A <- B <- C must close as C, B, A.
func TestCloseReverseDependencyOrder(t *testing.T) {
	c := lifecycle.New()
	var rec recorder

	mk := func(name string) lifecycle.Cleanup {
		return func(context.Context) error {
			rec.record(name)
			return nil
		}
	}
	a := c.Add(mk("A"), nil)
	b := c.Add(mk("B"), []lifecycle.Handle{a})
	c.Add(mk("C"), []lifecycle.Handle{b})

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := rec.snapshot()
	want := []string{"C", "B", "A"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("close order = %v, want %v", got, want)
	}
}

// Independent resources at the same dependency level close in parallel:
// both must enter their cleanup while each is still blocked.
func TestCloseParallelIndependentCleanup(t *testing.T) {
	c := lifecycle.New()
	var rec recorder

	root := c.Add(func(context.Context) error { rec.record("root"); return nil }, nil)
	gx, gy := newGate(nil), newGate(nil)
	c.Add(gx.cleanup(&rec, "X"), []lifecycle.Handle{root})
	c.Add(gy.cleanup(&rec, "Y"), []lifecycle.Handle{root})

	done := make(chan error, 1)
	go func() { done <- c.Close(context.Background()) }()

	// If X and Y closed serially, the second could never enter while the
	// first is still blocked on its gate.
	waitClosed(t, gx.entered, "X cleanup to start")
	waitClosed(t, gy.entered, "Y cleanup to start")

	close(gx.release)
	close(gy.release)
	if err := <-done; err != nil {
		t.Fatalf("Close: %v", err)
	}

	names := rec.snapshot()
	if indexOf(names, "root") < indexOf(names, "X") || indexOf(names, "root") < indexOf(names, "Y") {
		t.Fatalf("root closed before its dependents: %v", names)
	}
}

// A resource whose cleanup ignores its context and exceeds its timeout
// must not skip unrelated resources, nor its own dependents.
func TestTimeoutDoesNotSkipOtherResources(t *testing.T) {
	c := lifecycle.New()
	var rec recorder

	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	c.Add(func(ctx context.Context) error {
		<-stuck // deliberately ignores ctx
		return nil
	}, nil, lifecycle.WithName("stuck"), lifecycle.WithTimeout(50*time.Millisecond))

	independent := c.Add(func(context.Context) error { rec.record("independent"); return nil }, nil)
	_ = independent

	stuckH := lifecycle.Handle(0)
	c.Add(func(context.Context) error { rec.record("dependent"); return nil }, []lifecycle.Handle{stuckH})

	start := time.Now()
	err := c.Close(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, lifecycle.ErrCloseTimeout) {
		t.Fatalf("error %v does not wrap ErrCloseTimeout", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Close blocked on stuck resource for %v", elapsed)
	}
	names := rec.snapshot()
	if indexOf(names, "independent") < 0 {
		t.Fatalf("independent resource was skipped: %v", names)
	}
	if indexOf(names, "dependent") < 0 {
		t.Fatalf("dependent of timed-out resource was skipped: %v", names)
	}
}

// Multiple failures are aggregated stably by resource creation index.
func TestErrorAggregationStableByIndex(t *testing.T) {
	c := lifecycle.New()

	errThird := errors.New("third failed")
	errFirst := errors.New("first failed")
	noop := func(context.Context) error { return nil }

	c.Add(noop, nil, lifecycle.WithName("ok-0"))
	c.Add(func(context.Context) error { return errFirst }, nil, lifecycle.WithName("bad-1"))
	c.Add(noop, nil, lifecycle.WithName("ok-2"))
	c.Add(func(context.Context) error { return errThird }, nil, lifecycle.WithName("bad-3"))

	err := c.Close(context.Background())
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}

	var cerr *lifecycle.CloseError
	if !errors.As(err, &cerr) {
		t.Fatalf("error is %T, want *CloseError", err)
	}
	if len(cerr.Errors) != 2 {
		t.Fatalf("aggregated %d errors, want 2: %v", len(cerr.Errors), cerr)
	}
	if cerr.Errors[0].Index != 1 || cerr.Errors[1].Index != 3 {
		t.Fatalf("aggregation not stable by index: %+v", cerr.Errors)
	}
	if !errors.Is(err, errFirst) || !errors.Is(err, errThird) {
		t.Fatalf("aggregated error lost a cause: %v", err)
	}
	if strings.Index(err.Error(), "bad-1") > strings.Index(err.Error(), "bad-3") {
		t.Fatalf("error message not ordered by index: %v", err)
	}
}

// Repeated and concurrent Close calls share the first call's final
// result; cleanups run exactly once.
func TestRepeatedCloseSharesResult(t *testing.T) {
	c := lifecycle.New()

	var mu sync.Mutex
	calls := 0
	boom := errors.New("boom")
	c.Add(func(context.Context) error {
		mu.Lock()
		calls++
		mu.Unlock()
		return boom
	}, nil)

	const closers = 5
	errs := make([]error, closers)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = c.Close(context.Background())
		}(i)
	}
	wg.Wait()
	again := c.Close(context.Background())

	mu.Lock()
	if calls != 1 {
		t.Fatalf("cleanup ran %d times, want 1", calls)
	}
	mu.Unlock()
	for i, err := range errs {
		if !errors.Is(err, boom) {
			t.Fatalf("concurrent Close %d got %v, want boom", i, err)
		}
	}
	if !errors.Is(again, boom) {
		t.Fatalf("repeated Close got %v, want boom", again)
	}
	for i := 1; i < closers; i++ {
		if errs[i] != errs[0] {
			t.Fatalf("Close results not shared: %v vs %v", errs[i], errs[0])
		}
	}
	if again != errs[0] {
		t.Fatalf("repeated Close returned a different error value: %v vs %v", again, errs[0])
	}
}
