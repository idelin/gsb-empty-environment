package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustAdd(t *testing.T, c *Container, name string, fn CloseFunc, deps ...string) {
	t.Helper()
	if err := c.Add(name, fn, deps...); err != nil {
		t.Fatalf("Add(%q): %v", name, err)
	}
}

// recorder returns a CloseFunc that appends name to order when it runs.
func recorder(order *[]string, mu *sync.Mutex, name string) CloseFunc {
	return func(context.Context) error {
		mu.Lock()
		*order = append(*order, name)
		mu.Unlock()
		return nil
	}
}

func TestCloseReverseDependencyOrder(t *testing.T) {
	c := New()
	var mu sync.Mutex
	var order []string

	mustAdd(t, c, "db", recorder(&order, &mu, "db"))
	mustAdd(t, c, "cache", recorder(&order, &mu, "cache"), "db")
	mustAdd(t, c, "server", recorder(&order, &mu, "server"), "cache")

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := []string{"server", "cache", "db"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("close order = %v, want %v", order, want)
	}
}

func TestCloseDiamondDependencies(t *testing.T) {
	c := New()
	var mu sync.Mutex
	var order []string

	mustAdd(t, c, "db", recorder(&order, &mu, "db"))
	mustAdd(t, c, "users", recorder(&order, &mu, "users"), "db")
	mustAdd(t, c, "orders", recorder(&order, &mu, "orders"), "db")
	mustAdd(t, c, "api", recorder(&order, &mu, "api"), "users", "orders")

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(order) != 4 {
		t.Fatalf("closed %d resources, want 4", len(order))
	}
	if order[0] != "api" {
		t.Fatalf("first closed = %q, want api", order[0])
	}
	if order[3] != "db" {
		t.Fatalf("last closed = %q, want db", order[3])
	}
}

func TestIndependentResourcesCloseInParallel(t *testing.T) {
	c := New()
	started := make(chan string, 2)
	release := make(chan struct{})
	blocker := func(name string) CloseFunc {
		return func(context.Context) error {
			started <- name
			<-release // only proceeds once the test lets it
			return nil
		}
	}
	mustAdd(t, c, "a", blocker("a"))
	mustAdd(t, c, "b", blocker("b"))

	done := make(chan error, 1)
	go func() { done <- c.Close(context.Background()) }()

	// Both closers must be entered before either is released; if Close
	// ran them sequentially this would deadlock and hit the timeout.
	got := map[string]bool{}
	for len(got) < 2 {
		select {
		case name := <-started:
			got[name] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("independent resources did not close in parallel, started: %v", got)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestTimeoutDoesNotSkipOtherResources(t *testing.T) {
	c := New(WithCloseTimeout(50 * time.Millisecond))

	stuck := make(chan struct{})
	defer close(stuck) // release the leaked closer goroutine at test end
	mustAdd(t, c, "db", func(context.Context) error { <-stuck; return nil })

	// Depends on the stuck resource: must still be closed once db times out.
	dependentClosed := make(chan struct{})
	mustAdd(t, c, "cache", func(context.Context) error {
		close(dependentClosed)
		return nil
	}, "db")

	// Fully independent: must be closed promptly regardless of db.
	independentClosed := make(chan struct{})
	mustAdd(t, c, "metrics", func(context.Context) error {
		close(independentClosed)
		return nil
	})

	err := c.Close(context.Background())
	if err == nil {
		t.Fatal("Close returned nil error, want timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), `"db"`) {
		t.Fatalf("Close error = %v, want it to name the timed-out resource", err)
	}

	select {
	case <-dependentClosed:
	default:
		t.Fatal("dependent of the timed-out resource was skipped")
	}
	select {
	case <-independentClosed:
	default:
		t.Fatal("independent resource was skipped")
	}
}

func TestCloseErrorsAggregateInCreationOrder(t *testing.T) {
	c := New()
	errs := []error{
		errors.New("boom-0"),
		errors.New("boom-1"),
		errors.New("boom-2"),
	}
	// Independent resources failing in reverse completion order: #2 fails
	// immediately, #0 fails last. Aggregation must still follow creation
	// sequence, not completion order.
	delays := []time.Duration{60 * time.Millisecond, 30 * time.Millisecond, 0}
	for i := range errs {
		i := i
		mustAdd(t, c, fmt.Sprintf("res-%d", i), func(context.Context) error {
			time.Sleep(delays[i])
			return errs[i]
		})
	}

	err := c.Close(context.Background())
	if err == nil {
		t.Fatal("Close returned nil, want aggregated error")
	}
	for _, want := range errs {
		if !errors.Is(err, want) {
			t.Fatalf("aggregated error %v does not include %v", err, want)
		}
	}

	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("aggregated error %T does not expose Unwrap() []error", err)
	}
	parts := joined.Unwrap()
	if len(parts) != len(errs) {
		t.Fatalf("aggregated %d errors, want %d", len(parts), len(errs))
	}
	for i, want := range errs {
		if !errors.Is(parts[i], want) {
			t.Fatalf("aggregated error #%d = %v, want it to wrap %v (creation order)", i, parts[i], want)
		}
	}
}

func TestFailingResourceDoesNotSkipDependentsOrDependencies(t *testing.T) {
	c := New()
	closeErr := errors.New("cache close failed")

	var dbClosed, serverClosed atomic.Bool
	mustAdd(t, c, "db", func(context.Context) error { dbClosed.Store(true); return nil })
	mustAdd(t, c, "cache", func(context.Context) error { return closeErr }, "db")
	mustAdd(t, c, "server", func(context.Context) error { serverClosed.Store(true); return nil }, "cache")

	err := c.Close(context.Background())
	if !errors.Is(err, closeErr) {
		t.Fatalf("Close error = %v, want it to wrap %v", err, closeErr)
	}
	if !dbClosed.Load() {
		t.Fatal("dependency of a failing resource was skipped")
	}
	if !serverClosed.Load() {
		t.Fatal("dependent of a failing resource was skipped")
	}
}

func TestRepeatedCloseSharesResult(t *testing.T) {
	c := New()
	closeErr := errors.New("close failed")
	var calls atomic.Int32
	mustAdd(t, c, "r", func(context.Context) error {
		calls.Add(1)
		return closeErr
	})

	const callers = 8
	results := make([]error, callers)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = c.Close(context.Background())
		}(i)
	}
	wg.Wait()
	again := c.Close(context.Background())

	for i, err := range results {
		if !errors.Is(err, closeErr) {
			t.Fatalf("concurrent Close #%d = %v, want it to wrap %v", i, err, closeErr)
		}
		if err != again {
			t.Fatalf("repeated Close returned a different result: %v vs %v", err, again)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("close func ran %d times, want exactly 1", got)
	}
}

func TestRepeatedCloseSharesNilResult(t *testing.T) {
	c := New()
	var calls atomic.Int32
	mustAdd(t, c, "r", func(context.Context) error { calls.Add(1); return nil })

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("close func ran %d times, want exactly 1", got)
	}
}

func TestAddValidation(t *testing.T) {
	c := New()
	if err := c.Add("", func(context.Context) error { return nil }); err == nil {
		t.Fatal("Add with empty name succeeded")
	}
	if err := c.Add("nil-fn", nil); err == nil {
		t.Fatal("Add with nil close func succeeded")
	}
	if err := c.Add("bad-dep", func(context.Context) error { return nil }, "ghost"); err == nil {
		t.Fatal("Add with unknown dependency succeeded")
	}
	mustAdd(t, c, "a", func(context.Context) error { return nil })
	if err := c.Add("a", func(context.Context) error { return nil }); err == nil {
		t.Fatal("Add with duplicate name succeeded")
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Add("late", func(context.Context) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("Add after Close = %v, want ErrClosed", err)
	}
}
