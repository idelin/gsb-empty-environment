package taskgraph

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// getConcurrently runs fn in n goroutines and collects the results.
func getConcurrently(n int, fn func() (interface{}, error)) ([]interface{}, []error) {
	vals := make([]interface{}, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vals[i], errs[i] = fn()
		}(i)
	}
	wg.Wait()
	return vals, errs
}

func TestDiamondSharing(t *testing.T) {
	r := NewRunner()
	var dCount int32
	releaseD := make(chan struct{})

	// D blocks until released so that B and C are both parked on its
	// future at the same time; only one construction may run.
	r.Register("D", func(ctx context.Context, r *Runner, spec Spec) (interface{}, error) {
		atomic.AddInt32(&dCount, 1)
		<-releaseD
		return "d-value", nil
	})
	r.Register("B", func(ctx context.Context, r *Runner, spec Spec) (interface{}, error) {
		return r.Get(ctx, Spec{Name: "D"})
	})
	r.Register("C", func(ctx context.Context, r *Runner, spec Spec) (interface{}, error) {
		return r.Get(ctx, Spec{Name: "D"})
	})
	r.Register("A", func(ctx context.Context, r *Runner, spec Spec) (interface{}, error) {
		bChan := make(chan interface{}, 1)
		cChan := make(chan interface{}, 1)
		errChan := make(chan error, 2)
		go func() {
			v, err := r.Get(ctx, Spec{Name: "B"})
			if err != nil {
				errChan <- err
				return
			}
			bChan <- v
		}()
		go func() {
			v, err := r.Get(ctx, Spec{Name: "C"})
			if err != nil {
				errChan <- err
				return
			}
			cChan <- v
		}()
		var out []interface{}
		for i := 0; i < 2; i++ {
			select {
			case v := <-bChan:
				out = append(out, v)
			case v := <-cChan:
				out = append(out, v)
			case err := <-errChan:
				return nil, err
			}
		}
		return out, nil
	})

	done := make(chan error, 1)
	go func() {
		v, err := r.Get(context.Background(), Spec{Name: "A"})
		if err == nil {
			out := v.([]interface{})
			if len(out) != 2 || out[0] != "d-value" || out[1] != "d-value" {
				err = errors.New("unexpected diamond values")
			}
		}
		done <- err
	}()

	// Give B and C time to both park on D's future, then release.
	time.Sleep(100 * time.Millisecond)
	close(releaseD)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("diamond Get failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("diamond Get timed out (possible deadlock)")
	}
	if got := atomic.LoadInt32(&dCount); got != 1 {
		t.Fatalf("D constructed %d times, want 1", got)
	}
}

func TestConcurrentSameKeySharesResult(t *testing.T) {
	r := NewRunner()
	var count int32
	release := make(chan struct{})
	r.Register("slow", func(ctx context.Context, r *Runner, spec Spec) (interface{}, error) {
		atomic.AddInt32(&count, 1)
		<-release
		return "shared", nil
	})

	const n = 8
	done := make(chan struct{})
	var vals []interface{}
	var errs []error
	go func() {
		vals, errs = getConcurrently(n, func() (interface{}, error) {
			return r.Get(context.Background(), Spec{Name: "slow", Params: map[string]interface{}{"x": 1}})
		})
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	close(release)
	<-done

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("request %d failed: %v", i, errs[i])
		}
		if vals[i] != "shared" {
			t.Fatalf("request %d got %v, want shared", i, vals[i])
		}
	}
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Fatalf("constructor ran %d times, want 1", got)
	}
}

func TestFailureIsSharedThenRetried(t *testing.T) {
	r := NewRunner()
	var count int32
	release := make(chan struct{})
	r.Register("flaky", func(ctx context.Context, r *Runner, spec Spec) (interface{}, error) {
		n := atomic.AddInt32(&count, 1)
		if n == 1 {
			<-release
			return nil, errors.New("boom")
		}
		return "recovered", nil
	})

	// Concurrent waiters share the single failed construction.
	const n = 4
	done := make(chan struct{})
	var errs []error
	go func() {
		_, errs = getConcurrently(n, func() (interface{}, error) {
			return r.Get(context.Background(), Spec{Name: "flaky"})
		})
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	close(release)
	<-done

	for i := 0; i < n; i++ {
		if errs[i] == nil || errs[i].Error() != "boom" {
			t.Fatalf("waiter %d err = %v, want boom", i, errs[i])
		}
	}
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Fatalf("constructor ran %d times for shared failure, want 1", got)
	}

	// Failure is evicted: the next request retries and succeeds.
	v, err := r.Get(context.Background(), Spec{Name: "flaky"})
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if v != "recovered" {
		t.Fatalf("retry got %v, want recovered", v)
	}
	if got := atomic.LoadInt32(&count); got != 2 {
		t.Fatalf("constructor ran %d times after retry, want 2", got)
	}

	// Success is cached: no further constructions.
	if _, err := r.Get(context.Background(), Spec{Name: "flaky"}); err != nil {
		t.Fatalf("cached get failed: %v", err)
	}
	if got := atomic.LoadInt32(&count); got != 2 {
		t.Fatalf("constructor ran %d times after cache hit, want 2", got)
	}
}

func TestSelfCycleReported(t *testing.T) {
	r := NewRunner()
	r.Register("self", func(ctx context.Context, r *Runner, spec Spec) (interface{}, error) {
		return r.Get(ctx, spec) // depends on itself
	})

	done := make(chan error, 1)
	go func() {
		_, err := r.Get(context.Background(), Spec{Name: "self"})
		done <- err
	}()
	select {
	case err := <-done:
		ce, ok := err.(*CycleError)
		if !ok {
			t.Fatalf("err = %v (%T), want *CycleError", err, err)
		}
		if len(ce.Path) != 2 || ce.Path[0] != ce.Path[1] {
			t.Fatalf("cycle path = %v, want [k k]", ce.Path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("self-cycle Get timed out: waited on its own future")
	}
}

func TestIndirectCycleReported(t *testing.T) {
	r := NewRunner()
	r.Register("A", func(ctx context.Context, r *Runner, spec Spec) (interface{}, error) {
		return r.Get(ctx, Spec{Name: "B"})
	})
	r.Register("B", func(ctx context.Context, r *Runner, spec Spec) (interface{}, error) {
		return r.Get(ctx, Spec{Name: "A"}) // back edge to ancestor
	})

	done := make(chan error, 1)
	go func() {
		_, err := r.Get(context.Background(), Spec{Name: "A"})
		done <- err
	}()
	select {
	case err := <-done:
		ce, ok := err.(*CycleError)
		if !ok {
			t.Fatalf("err = %v (%T), want *CycleError", err, err)
		}
		if len(ce.Path) != 3 {
			t.Fatalf("cycle path = %v, want 3 entries", ce.Path)
		}
		if !strings.Contains(ce.Error(), "cycle") {
			t.Fatalf("error message %q does not mention cycle", ce.Error())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("indirect-cycle Get timed out: waited on ancestor's future")
	}
}

func TestCanonicalKeyCollisions(t *testing.T) {
	// Pairs that a naive concatenation would collide must stay distinct.
	distinct := [][2]map[string]interface{}{
		{
			{"a": []interface{}{"ab", "c"}},
			{"a": []interface{}{"a", "bc"}},
		},
		{
			{"k": "x:y"},
			{"k:x": "y"},
		},
		{
			{"n": "1"},
			{"n": 1},
		},
	}
	for i, pair := range distinct {
		k1 := canonicalKey("t", pair[0])
		k2 := canonicalKey("t", pair[1])
		if k1 == k2 {
			t.Fatalf("case %d: distinct params share key %q", i, k1)
		}
	}

	// Map iteration order must not affect the key.
	a := canonicalKey("t", map[string]interface{}{"x": 1, "y": 2})
	b := canonicalKey("t", map[string]interface{}{"y": 2, "x": 1})
	if a != b {
		t.Fatalf("equal params got different keys: %q vs %q", a, b)
	}
	nested1 := canonicalKey("t", map[string]interface{}{
		"m": map[string]interface{}{"a": 1, "b": []interface{}{"p", "q"}},
	})
	nested2 := canonicalKey("t", map[string]interface{}{
		"m": map[string]interface{}{"b": []interface{}{"p", "q"}, "a": 1},
	})
	if nested1 != nested2 {
		t.Fatalf("equal nested params got different keys: %q vs %q", nested1, nested2)
	}

	// Distinct keys construct separately; equal keys dedupe.
	r := NewRunner()
	var count int32
	r.Register("t", func(ctx context.Context, r *Runner, spec Spec) (interface{}, error) {
		atomic.AddInt32(&count, 1)
		return spec.Key(), nil
	})
	specs := []Spec{
		{Name: "t", Params: map[string]interface{}{"a": []interface{}{"ab", "c"}}},
		{Name: "t", Params: map[string]interface{}{"a": []interface{}{"a", "bc"}}},
		{Name: "t", Params: map[string]interface{}{"x": 1, "y": 2}},
		{Name: "t", Params: map[string]interface{}{"y": 2, "x": 1}},
	}
	keys := make([]interface{}, len(specs))
	for i, s := range specs {
		v, err := r.Get(context.Background(), s)
		if err != nil {
			t.Fatalf("spec %d: %v", i, err)
		}
		keys[i] = v
	}
	if got := atomic.LoadInt32(&count); got != 3 {
		t.Fatalf("constructor ran %d times, want 3 (two distinct + one deduped pair)", got)
	}
	if keys[0] == keys[1] {
		t.Fatal("colliding-looking params produced the same key")
	}
	if keys[2] != keys[3] {
		t.Fatal("reordered params did not dedupe to the same key")
	}
}
