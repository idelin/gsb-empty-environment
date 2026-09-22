package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor blocks until cond is true or the deadline expires.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting: " + msg)
}

func servedOf(t *testing.T, stats []TenantStats, id string) int64 {
	for _, st := range stats {
		if st.ID == id {
			return st.Served
		}
	}
	t.Fatalf("tenant %s missing from stats", id)
	return 0
}

// With both tenants continuously saturated and a 1:2 weight, long-run service
// counts must approach that ratio regardless of submission interleaving.
func TestSaturatedRatio(t *testing.T) {
	s, err := New(3, map[string]int{"a": 1, "b": 2})
	if err != nil {
		t.Fatal(err)
	}

	const n = 600
	gate := make(chan struct{})
	work := func(context.Context) error {
		<-gate
		return nil
	}

	// Interleave submissions: 3 of a, 6 of b per round, matching the weights.
	for i := 0; i < n; i += 9 {
		for j := 0; j < 3 && i+j < n; j++ {
			if _, err := s.Submit("a", work); err != nil {
				t.Fatal(err)
			}
		}
		for j := 0; j < 6 && i+3+j < n; j++ {
			if _, err := s.Submit("b", work); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Let the first tasks start, then everything proceeds from full queues.
	waitFor(t, func() bool {
		stats, err := s.Stats()
		return err == nil && servedOf(t, stats, "a")+servedOf(t, stats, "b") == int64(s.concurrency)
	}, "first tasks to start")
	close(gate)

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	a, b := servedOf(t, stats, "a"), servedOf(t, stats, "b")
	ratio := float64(b) / float64(a)
	t.Logf("served a=%d b=%d ratio b/a=%.3f", a, b, ratio)
	if a+b != n {
		t.Fatalf("expected %d tasks served, got %d", n, a+b)
	}
	if diff := ratio - 2.0; diff > 0.1 || diff < -0.1 {
		t.Fatalf("ratio %.3f too far from 2.0", ratio)
	}
}

// Tasks of one tenant must start strictly in FIFO order.
func TestFIFOPerTenant(t *testing.T) {
	s, err := New(1, map[string]int{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	var mu sync.Mutex
	var order []int
	started := make([]chan struct{}, 8)
	release := make([]chan struct{}, 8)
	for i := range started {
		started[i] = make(chan struct{})
		release[i] = make(chan struct{})
	}
	for i := range started {
		idx := i
		if _, err := s.Submit("a", func(context.Context) error {
			mu.Lock()
			order = append(order, idx)
			mu.Unlock()
			close(started[idx])
			<-release[idx]
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range started {
		waitFor(t, func() bool {
			select {
			case <-started[i]:
				return true
			default:
				return false
			}
		}, fmt.Sprintf("task %d to start in order", i))
		mu.Lock()
		got := len(order)
		mu.Unlock()
		if got != i+1 {
			t.Fatalf("expected exactly %d started, got %d", i+1, got)
		}
		close(release[i])
	}
}

// Never more than the configured number of tasks run simultaneously.
func TestGlobalConcurrencyLimit(t *testing.T) {
	const limit = 3
	s, err := New(limit, map[string]int{"a": 1, "b": 1, "c": 1})
	if err != nil {
		t.Fatal(err)
	}

	var active, maxActive int64
	enter := make(chan struct{}, 16)
	release := make(chan struct{})
	work := func(context.Context) error {
		cur := atomic.AddInt64(&active, 1)
		for {
			m := atomic.LoadInt64(&maxActive)
			if cur <= m || atomic.CompareAndSwapInt64(&maxActive, m, cur) {
				break
			}
		}
		enter <- struct{}{}
		<-release
		atomic.AddInt64(&active, -1)
		return nil
	}
	for i := 0; i < 5; i++ {
		for _, id := range []string{"a", "b", "c"} {
			if _, err := s.Submit(id, work); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 0; i < limit; i++ {
		<-enter
	}
	if got := atomic.LoadInt64(&maxActive); got != limit {
		t.Fatalf("expected %d concurrent, got %d", limit, got)
	}
	// Nothing else starts while the slots are held.
	select {
	case <-enter:
		t.Fatal("more than limit tasks started")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&maxActive); got > limit {
		t.Fatalf("concurrency exceeded limit: %d", got)
	}
}

// A tenant with no work must not block other tenants from using every slot.
func TestIdleTenantDoesNotWasteSlots(t *testing.T) {
	const limit = 4
	s, err := New(limit, map[string]int{"a": 1, "b": 1, "idle": 100})
	if err != nil {
		t.Fatal(err)
	}

	var active int64
	enter := make(chan struct{}, 16)
	release := make(chan struct{})
	work := func(context.Context) error {
		atomic.AddInt64(&active, 1)
		enter <- struct{}{}
		<-release
		atomic.AddInt64(&active, -1)
		return nil
	}
	for i := 0; i < limit; i++ {
		if _, err := s.Submit("a", work); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < limit; i++ {
		<-enter
	}
	if got := atomic.LoadInt64(&active); got != limit {
		t.Fatalf("expected all %d slots used despite idle tenant, got %d", limit, got)
	}
	// Once a slot frees, b uses it even though a is also queued.
	for i := 0; i < limit; i++ {
		if _, err := s.Submit("b", work); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if a, b := servedOf(t, stats, "a"), servedOf(t, stats, "b"); a != limit || b != limit {
		t.Fatalf("expected a=%d b=%d, got a=%d b=%d", limit, limit, a, b)
	}
}

// Canceling a waiting task removes it without ever consuming a slot/quota.
func TestCancelWaitingDoesNotConsumeQuota(t *testing.T) {
	s, err := New(1, map[string]int{"a": 1, "b": 1})
	if err != nil {
		t.Fatal(err)
	}

	releaseHead := make(chan struct{})
	headStarted := make(chan struct{})
	if _, err := s.Submit("a", func(context.Context) error {
		close(headStarted)
		<-releaseHead
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	<-headStarted

	var ranB int64
	tasks := make([]*Task, 3)
	for i := range tasks {
		tk, err := s.Submit("b", func(context.Context) error {
			atomic.AddInt64(&ranB, 1)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		tasks[i] = tk
	}

	if !s.Cancel(tasks[1]) {
		t.Fatal("expected cancel of waiting task to succeed")
	}
	if !tasks[1].IsCanceled() {
		t.Fatal("canceled task must report IsCanceled")
	}
	if !s.Cancel(tasks[0]) {
		t.Fatal("second cancel attempt should still remove a waiting task")
	}
	// Cancel of a non-waiting task returns false.
	if s.Cancel(tasks[0]) {
		t.Fatal("cancel of an already-removed task must return false")
	}

	stats, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if w := tenantWaiting(t, stats, "b"); w != 1 {
		t.Fatalf("expected 1 waiting task for b, got %d", w)
	}
	if v := servedOf(t, stats, "b"); v != 0 {
		t.Fatalf("canceled waiting tasks must not consume quota, served=%d", v)
	}

	close(releaseHead)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ran := atomic.LoadInt64(&ranB); ran != 1 {
		t.Fatalf("exactly one b task should have run, got %d", ran)
	}
}

func tenantWaiting(t *testing.T, stats []TenantStats, id string) int {
	for _, st := range stats {
		if st.ID == id {
			return st.Waiting
		}
	}
	t.Fatalf("tenant %s missing", id)
	return 0
}

// A failing task must release its global slot so other work can proceed.
func TestFailureReleasesSlot(t *testing.T) {
	s, err := New(1, map[string]int{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("boom")
	firstDone := make(chan struct{})
	t1, err := s.Submit("a", func(context.Context) error {
		close(firstDone)
		return injected
	})
	if err != nil {
		t.Fatal(err)
	}
	<-firstDone
	secondDone := make(chan struct{})
	t2, err := s.Submit("a", func(context.Context) error {
		close(secondDone)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-secondDone
	if err := t1.Wait(); !errors.Is(err, injected) {
		t.Fatalf("expected injected failure, got %v", err)
	}
	if err := t2.Wait(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if servedOf(t, stats, "a") != 2 {
		t.Fatal("failed task must release its slot")
	}
}

// After Shutdown no new tasks are accepted; queued tasks still drain.
func TestShutdownDrainsAndRejects(t *testing.T) {
	s, err := New(2, map[string]int{"a": 1})
	if err != nil {
		t.Fatal(err)
	}

	var completed int64
	gate := make(chan struct{})
	const n = 10
	tasks := make([]*Task, n)
	for i := 0; i < n; i++ {
		tk, err := s.Submit("a", func(context.Context) error {
			<-gate
			atomic.AddInt64(&completed, 1)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		tasks[i] = tk
	}

	shutdownReturned := make(chan struct{})
	go func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		close(shutdownReturned)
	}()

	select {
	case <-shutdownReturned:
		t.Fatal("shutdown must not return while tasks are blocked")
	case <-time.After(30 * time.Millisecond):
	}

	if _, err := s.Submit("a", func(context.Context) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
	if err := s.SetWeight("a", 5); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed from SetWeight, got %v", err)
	}
	if err := s.AddTenant("z", 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed from AddTenant, got %v", err)
	}

	close(gate)
	<-shutdownReturned
	if got := atomic.LoadInt64(&completed); got != n {
		t.Fatalf("expected all %d tasks drained, got %d", n, got)
	}
	for _, tk := range tasks {
		<-tk.Done()
	}
	// Final stats remain readable after drain.
	stats, err := s.Stats()
	if err != nil || servedOf(t, stats, "a") != n {
		t.Fatalf("post-shutdown stats wrong: %v %v", stats, err)
	}
	// Repeated shutdown is a no-op.
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Shutdown honors context cancellation while tasks are still running.
func TestShutdownContextTimeout(t *testing.T) {
	s, err := New(1, map[string]int{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	if _, err := s.Submit("a", func(context.Context) error {
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
	close(release)
}

// pickTenant must react to a weight change from the next decision onward.
func TestPickTenantDynamicWeights(t *testing.T) {
	d := &dstate{tenants: map[string]*tenant{
		"a": {id: "a", weight: 1},
		"b": {id: "b", weight: 2},
	}}
	for range 9 {
		d.tenants["a"].queue = append(d.tenants["a"].queue, &Task{})
		d.tenants["b"].queue = append(d.tenants["b"].queue, &Task{})
	}
	var picks []byte
	for range 9 {
		tt := d.pickTenant()
		picks = append(picks, tt.id[0])
		tt.dequeue()
		tt.served++
		if string(picks) == "abbabbab" {
			// Change weights to a:3, b:1 halfway; subsequent picks change.
			d.tenants["a"].weight = 3
			d.tenants["b"].weight = 1
		}
	}
	if got := string(picks); got != "abbabbaba" {
		t.Fatalf("dynamic weight selection = %q, want %q", got, "abbabbaba")
	}
}

// End-to-end: with one slot the start order is deterministic. While pick 9
// (still selected under a:1/b:2) runs, the weights switch to a:3/b:1; the
// switch influences only later picks.
func TestSetWeightAffectsFutureSelections(t *testing.T) {
	s, err := New(1, map[string]int{"a": 1, "b": 2})
	if err != nil {
		t.Fatal(err)
	}

	const total = 18
	signals := make(chan struct {
		id      string
		release chan struct{}
	}, total*2)
	work := func(id string) TaskFunc {
		return func(context.Context) error {
			release := make(chan struct{}, 1)
			signals <- struct {
				id      string
				release chan struct{}
			}{id, release}
			<-release
			return nil
		}
	}
	for range total / 2 {
		if _, err := s.Submit("a", work("a")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Submit("b", work("b")); err != nil {
			t.Fatal(err)
		}
	}

	want := "abbabbabb" + "aaaaaa" + "bbb"
	var gotOrder string
	for i := range want {
		select {
		case sig := <-signals:
			gotOrder += sig.id
			if sig.id != string(want[i]) {
				t.Fatalf("pick %d: expected %s, got %s (order %q)", i+1, string(want[i]), sig.id, gotOrder)
			}
			if i == 8 {
				if err := s.SetWeight("a", 3); err != nil {
					t.Fatal(err)
				}
			}
			close(sig.release) // the running task is confirmed blocked here
		case <-time.After(2 * time.Second):
			t.Fatalf("pick %d: expected %s (order %q)", i+1, string(want[i]), gotOrder)
		}
	}

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Logf("dynamic-weight pick order: %q", gotOrder)
	stats, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if a, b := servedOf(t, stats, "a"), servedOf(t, stats, "b"); a != 9 || b != 9 {
		t.Fatalf("expected a=9 b=9, got a=%d b=%d", a, b)
	}
}

func TestValidationAndUnknownTenant(t *testing.T) {
	if _, err := New(0, map[string]int{"a": 1}); !errors.Is(err, ErrInvalidConcurrency) {
		t.Fatalf("got %v", err)
	}
	if _, err := New(1, map[string]int{"a": 0}); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("got %v", err)
	}
	s, err := New(1, map[string]int{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit("ghost", func(context.Context) error { return nil }); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := s.SetWeight("ghost", 2); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := s.SetWeight("a", 0); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("got %v", err)
	}
	if err := s.AddTenant("a", 1); !errors.Is(err, ErrTenantExists) {
		t.Fatalf("got %v", err)
	}
	if err := s.AddTenant("c", -1); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("got %v", err)
	}
	if err := s.AddTenant("c", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Concurrent stress for the race detector.
func TestConcurrentStress(t *testing.T) {
	s, err := New(4, map[string]int{"a": 1, "b": 2, "c": 3})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				tk, err := s.Submit(id, func(context.Context) error { return nil })
				if err != nil {
					return
				}
				tk.Wait()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 20 {
			if err := s.SetWeight("a", 1+(time.Now().Nanosecond()%4)); err != nil {
				return
			}
			time.Sleep(time.Microsecond)
		}
	}()
	wg.Wait()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
