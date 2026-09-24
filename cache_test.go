package writeback

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

// fakeSource is an in-memory source with injectable failure hooks.
type fakeSource struct {
	mu      sync.Mutex
	ops     []string // ordered log: "set k=v" / "del k"
	data    map[string]string
	failOn  func(op string) bool // return true to fail this op
	gate    chan struct{}        // if non-nil, ops wait until it is closed
	entered chan string          // if non-nil, receives each op desc on arrival
}

func newFakeSource() *fakeSource {
	return &fakeSource{data: make(map[string]string)}
}

func (f *fakeSource) source() Source {
	return Source{
		Store: func(ctx context.Context, key string, value []byte) error {
			return f.apply("set", key, string(value))
		},
		Delete: func(ctx context.Context, key string) error {
			return f.apply("del", key, "")
		},
	}
}

func (f *fakeSource) apply(op, key, value string) error {
	desc := op + " " + key
	if value != "" {
		desc += "=" + value
	}
	f.mu.Lock()
	gate, entered := f.gate, f.entered
	f.mu.Unlock()
	if entered != nil {
		entered <- desc
	}
	if gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn != nil && f.failOn(desc) {
		return errors.New("injected failure: " + desc)
	}
	f.ops = append(f.ops, desc)
	if op == "set" {
		f.data[key] = value
	} else {
		delete(f.data, key)
	}
	return nil
}

func (f *fakeSource) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ops...)
}

// manualCache builds a cache with no background goroutine (FlushInterval 0).
func manualCache(src Source, opts ...func(*Options)) *Cache {
	o := Options{Source: src, FlushInterval: 0}
	for _, fn := range opts {
		fn(&o)
	}
	return New(o)
}

func TestOrderedFlushWithMerge(t *testing.T) {
	fs := newFakeSource()
	c := manualCache(fs.source())
	defer c.Shutdown()

	c.Set("k1", []byte("v1"))
	c.Set("k2", []byte("v2"))
	c.Set("k3", []byte("v3"))
	c.Delete("k2")             // merges into k2's slot, keeps position
	c.Set("k1", []byte("v1b")) // merges into k1's slot, keeps position

	if got := c.Pending(); got != 3 {
		t.Fatalf("pending = %d, want 3 (merged)", got)
	}
	n, err := c.Flush(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("Flush = (%d, %v), want (3, nil)", n, err)
	}
	want := []string{"set k1=v1b", "del k2", "set k3=v3"}
	if got := fs.log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("source log = %v, want %v", got, want)
	}
	// Merged ops share their queue slot's seq, so seqs 1..3 were assigned;
	// queue drained => watermark == 3 and everything is durable.
	if got := c.Watermark(); got != 3 {
		t.Fatalf("watermark = %d, want 3", got)
	}
}

func TestFailureKeepsDirtyAndRetryPreservesOrder(t *testing.T) {
	fs := newFakeSource()
	// Fail the first attempt at deleting b.
	failOnce := true
	fs.failOn = func(op string) bool {
		if op == "del b" && failOnce {
			failOnce = false
			return true
		}
		return false
	}
	c := manualCache(fs.source())
	defer c.Shutdown()

	c.Set("a", []byte("1"))
	c.Delete("b")
	c.Set("c", []byte("3"))

	n, err := c.Flush(context.Background())
	if err == nil {
		t.Fatal("Flush should fail on del b")
	}
	if n != 1 {
		t.Fatalf("committed = %d, want 1 (only set a)", n)
	}
	// Dirty state must survive the failure.
	if got := c.Pending(); got != 2 {
		t.Fatalf("pending after failure = %d, want 2", got)
	}
	if got := c.Unflushed(); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("unflushed = %v, want [b c]", got)
	}
	// Watermark covers only seq 1 (set a).
	if got := c.Watermark(); got != 1 {
		t.Fatalf("watermark = %d, want 1", got)
	}

	// Retry: order b then c must be preserved.
	if _, err := c.Flush(context.Background()); err != nil {
		t.Fatalf("retry Flush: %v", err)
	}
	want := []string{"set a=1", "del b", "set c=3"}
	if got := fs.log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("source log = %v, want %v", got, want)
	}
	if got := c.Watermark(); got != 3 {
		t.Fatalf("watermark = %d, want 3", got)
	}
}

func TestDeleteWriteOrderAcrossFailure(t *testing.T) {
	fs := newFakeSource()
	c := manualCache(fs.source())
	defer c.Shutdown()

	// Interleave writes and deletes of the same key across flushes.
	c.Set("x", []byte("1"))
	if _, err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Delete("x")
	c.Set("y", []byte("2"))
	if _, err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"set x=1", "del x", "set y=2"}
	if got := fs.log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("source log = %v, want %v", got, want)
	}
	if _, ok := fs.data["x"]; ok {
		t.Fatal("x should be deleted in source")
	}
}

func TestSameKeyMergeCollapsesToLatestWrite(t *testing.T) {
	fs := newFakeSource()
	c := manualCache(fs.source())
	defer c.Shutdown()

	for i := 0; i < 10; i++ {
		c.Set("hot", []byte(fmt.Sprintf("v%d", i)))
	}
	if got := c.Pending(); got != 1 {
		t.Fatalf("pending = %d, want 1 (all merged)", got)
	}
	if _, err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"set hot=v9"}
	if got := fs.log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("source log = %v, want %v", got, want)
	}
	// All 10 updates merged into the single slot with seq 1; after the
	// drain the watermark covers it and every merged update is durable.
	if got := c.Watermark(); got != 1 {
		t.Fatalf("watermark = %d, want 1", got)
	}
}

func TestRedirtyDuringFlushStaysDirty(t *testing.T) {
	fs := newFakeSource()
	fs.gate = make(chan struct{})
	fs.entered = make(chan string, 4)
	c := manualCache(fs.source())
	defer c.Shutdown()

	c.Set("k", []byte("old"))

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Flush(context.Background())
	}()

	// Wait until Store("k", "old") is blocked inside the source, then
	// re-dirty the key and release the gate.
	if op := <-fs.entered; op != "set k=old" {
		t.Fatalf("source entered with %q, want set k=old", op)
	}
	c.Set("k", []byte("new"))
	close(fs.gate)
	<-done

	// The entry was re-dirtied while its snapshot was in flight. If commit
	// had dropped it, the source would hold "old" and the log would lack
	// the second write; instead the same Flush call re-flushes it.
	want := []string{"set k=old", "set k=new"}
	if got := fs.log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("source log = %v, want %v", got, want)
	}
	if fs.data["k"] != "new" {
		t.Fatalf("source k = %q, want new", fs.data["k"])
	}
}

func TestMaxBatch(t *testing.T) {
	fs := newFakeSource()
	c := manualCache(fs.source(), func(o *Options) { o.MaxBatch = 2 })
	defer c.Shutdown()

	for i := 0; i < 5; i++ {
		c.Set(fmt.Sprintf("k%d", i), []byte(fmt.Sprintf("v%d", i)))
	}
	n, err := c.Flush(context.Background())
	if err != nil || n != 5 {
		t.Fatalf("Flush = (%d, %v), want (5, nil)", n, err)
	}
	want := []string{"set k0=v0", "set k1=v1", "set k2=v2", "set k3=v3", "set k4=v4"}
	if got := fs.log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("source log = %v, want %v", got, want)
	}
}

func TestBackgroundFlushAndCloseDrains(t *testing.T) {
	fs := newFakeSource()
	c := New(Options{Source: fs.source(), FlushInterval: 10 * time.Millisecond})

	for i := 0; i < 3; i++ {
		c.Set(fmt.Sprintf("k%d", i), []byte("v"))
	}
	deadline := time.Now().Add(2 * time.Second)
	for c.Pending() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := c.Pending(); got != 0 {
		t.Fatalf("background flush did not drain, pending = %d", got)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := len(fs.log()); got != 3 {
		t.Fatalf("source ops = %d, want 3", got)
	}
}

func TestCloseRetriesUntilDrain(t *testing.T) {
	fs := newFakeSource()
	remaining := 3
	fs.failOn = func(op string) bool {
		if remaining > 0 {
			remaining--
			return true
		}
		return false
	}
	c := New(Options{
		Source:        fs.source(),
		FlushInterval: 0, // manual; Close drives the retries
		Backoff:       func(int) time.Duration { return time.Millisecond },
	})

	c.Set("a", []byte("1"))
	c.Set("b", []byte("2"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close should drain after retries: %v", err)
	}
	if got := c.Pending(); got != 0 {
		t.Fatalf("pending = %d, want 0", got)
	}
	want := []string{"set a=1", "set b=2"}
	if got := fs.log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("source log = %v, want %v", got, want)
	}
}

func TestCloseContextExpiryLeavesUnflushed(t *testing.T) {
	fs := newFakeSource()
	fs.failOn = func(string) bool { return true } // always fail
	c := New(Options{
		Source:        fs.source(),
		FlushInterval: 0,
		Backoff:       func(int) time.Duration { return 5 * time.Millisecond },
	})

	c.Set("a", []byte("1"))
	c.Set("b", []byte("2"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := c.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close err = %v, want deadline exceeded", err)
	}
	if got := c.Unflushed(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("unflushed = %v, want [a b]", got)
	}
	// Dirty flags survive the failed close.
	if got := c.Pending(); got != 2 {
		t.Fatalf("pending = %d, want 2", got)
	}
}

func TestShutdownReturnsUnflushedInOrder(t *testing.T) {
	fs := newFakeSource()
	fs.failOn = func(string) bool { return true }
	c := New(Options{Source: fs.source(), FlushInterval: 0})

	c.Set("k1", []byte("1"))
	c.Delete("k2")
	c.Set("k3", []byte("3"))

	got := c.Shutdown()
	want := []string{"k1", "k2", "k3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Shutdown = %v, want %v", got, want)
	}
	if err := c.Set("k4", []byte("4")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Set after Shutdown = %v, want ErrClosed", err)
	}
	if err := c.Delete("k1"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Delete after Shutdown = %v, want ErrClosed", err)
	}
	// Shutdown is idempotent.
	if got := c.Shutdown(); !reflect.DeepEqual(got, want) {
		t.Fatalf("second Shutdown = %v, want %v", got, want)
	}
}

func TestResourceRelease(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		fs := newFakeSource()
		c := New(Options{Source: fs.source(), FlushInterval: time.Millisecond})
		c.Set("k", []byte("v"))
		if err := c.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutines leaked: before=%d after=%d", before, got)
	}
}

func TestInjectedClock(t *testing.T) {
	fs := newFakeSource()
	fakeTime := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	c := manualCache(fs.source(), func(o *Options) {
		o.Now = func() time.Time { return fakeTime }
	})
	defer c.Shutdown()

	c.Set("k", []byte("v"))
	if _, err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.LastFlush(); !got.Equal(fakeTime) {
		t.Fatalf("LastFlush = %v, want injected clock %v", got, fakeTime)
	}
}

func TestGetServesCacheOnly(t *testing.T) {
	fs := newFakeSource()
	c := manualCache(fs.source())
	defer c.Shutdown()

	c.Set("k", []byte("v"))
	if v, ok := c.Get("k"); !ok || string(v) != "v" {
		t.Fatalf("Get = (%q, %v), want (v, true)", v, ok)
	}
	c.Delete("k")
	if _, ok := c.Get("k"); ok {
		t.Fatal("Get after Delete should miss")
	}
	if got := len(fs.log()); got != 0 {
		t.Fatalf("Get must not touch source, ops = %v", fs.log())
	}
}
