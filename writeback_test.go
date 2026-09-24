package writeback

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

var errInjected = errors.New("injected sink failure")

// recorder is an injectable backing source with injectable failures.
type recorder struct {
	mu        sync.Mutex
	batches   [][]Op
	failTimes int // fail the next N calls
	alwaysErr error
}

func (r *recorder) sink(_ context.Context, batch []Op) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failTimes > 0 {
		r.failTimes--
		return errInjected
	}
	if r.alwaysErr != nil {
		return r.alwaysErr
	}
	r.batches = append(r.batches, append([]Op(nil), batch...))
	return nil
}

func (r *recorder) ops() []Op {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Op
	for _, b := range r.batches {
		out = append(out, b...)
	}
	return out
}

func opKinds(ops []Op) []OpKind {
	kinds := make([]OpKind, len(ops))
	for i, op := range ops {
		kinds[i] = op.Kind
	}
	return kinds
}

func opKeys(ops []Op) []string {
	keys := make([]string, len(ops))
	for i, op := range ops {
		keys[i] = op.Key
	}
	return keys
}

func testOptions(sink Sink) Options {
	return Options{
		Sink:          sink,
		BatchSize:     64,
		FlushInterval: time.Millisecond,
		RetryBackoff:  time.Millisecond,
	}
}

func TestFlushPreservesOrder(t *testing.T) {
	rec := &recorder{}
	c := newCache(testOptions(rec.sink))
	defer c.Close(context.Background())

	_ = c.Set("a", []byte("1"))
	_ = c.Set("b", []byte("2"))
	_ = c.Delete("gone")
	_ = c.Set("c", []byte("3"))

	if err := c.flushOnce(); err != nil {
		t.Fatalf("flushOnce: %v", err)
	}
	got := rec.ops()
	if want := []string{"a", "b", "gone", "c"}; !reflect.DeepEqual(opKeys(got), want) {
		t.Fatalf("flush order = %v, want %v", opKeys(got), want)
	}
	if want := []OpKind{OpSet, OpSet, OpDelete, OpSet}; !reflect.DeepEqual(opKinds(got), want) {
		t.Fatalf("flush kinds = %v, want %v", opKinds(got), want)
	}
	if wm := c.Watermark(); wm != 4 {
		t.Fatalf("watermark = %d, want 4", wm)
	}
	if n := c.DirtyLen(); n != 0 {
		t.Fatalf("dirty len = %d, want 0", n)
	}
}

func TestMergeSameKeyKeepsWatermark(t *testing.T) {
	rec := &recorder{}
	opts := testOptions(rec.sink)
	opts.BatchSize = 1
	c := newCache(opts)
	defer c.Close(context.Background())

	_ = c.Set("a", []byte("1")) // seq 1
	_ = c.Set("b", []byte("2")) // seq 2
	_ = c.Set("a", []byte("3")) // seq 3, merges into a's slot

	if n := c.DirtyLen(); n != 2 {
		t.Fatalf("dirty len after merge = %d, want 2", n)
	}

	// First batch flushes only "a" (carrying seq 3). "b" (seq 2) is still
	// dirty, so the watermark must not jump to 3.
	if err := c.flushOnce(); err != nil {
		t.Fatalf("flushOnce: %v", err)
	}
	if wm := c.Watermark(); wm != 1 {
		t.Fatalf("watermark after partial flush = %d, want 1", wm)
	}

	if err := c.flushOnce(); err != nil {
		t.Fatalf("flushOnce: %v", err)
	}
	if wm := c.Watermark(); wm != 3 {
		t.Fatalf("watermark after drain = %d, want 3", wm)
	}

	// The sink saw the merged value, not the stale one.
	got := rec.ops()
	if len(got) != 2 || got[0].Key != "a" || string(got[0].Value) != "3" {
		t.Fatalf("merged flush = %+v, want a=3 first", got)
	}
}

func TestDeleteAndWriteNeverReordered(t *testing.T) {
	t.Run("merged set then delete flushes delete only", func(t *testing.T) {
		rec := &recorder{}
		c := newCache(testOptions(rec.sink))
		defer c.Close(context.Background())

		_ = c.Set("k", []byte("1"))
		_ = c.Delete("k")
		if n := c.DirtyLen(); n != 1 {
			t.Fatalf("dirty len = %d, want 1 (merged)", n)
		}
		if err := c.flushOnce(); err != nil {
			t.Fatalf("flushOnce: %v", err)
		}
		got := rec.ops()
		if len(got) != 1 || got[0].Kind != OpDelete || got[0].Key != "k" {
			t.Fatalf("sink ops = %+v, want single DELETE k", got)
		}
	})

	t.Run("across batches delete follows write", func(t *testing.T) {
		rec := &recorder{}
		c := newCache(testOptions(rec.sink))
		defer c.Close(context.Background())

		_ = c.Set("k", []byte("1"))
		if err := c.flushOnce(); err != nil {
			t.Fatalf("flushOnce: %v", err)
		}
		_ = c.Delete("k")
		if err := c.flushOnce(); err != nil {
			t.Fatalf("flushOnce: %v", err)
		}
		got := rec.ops()
		if want := []OpKind{OpSet, OpDelete}; !reflect.DeepEqual(opKinds(got), want) {
			t.Fatalf("sink kinds = %v, want %v", opKinds(got), want)
		}
	})

	t.Run("merged delete then set flushes set only", func(t *testing.T) {
		rec := &recorder{}
		c := newCache(testOptions(rec.sink))
		defer c.Close(context.Background())

		_ = c.Delete("k")
		_ = c.Set("k", []byte("2"))
		if err := c.flushOnce(); err != nil {
			t.Fatalf("flushOnce: %v", err)
		}
		got := rec.ops()
		if len(got) != 1 || got[0].Kind != OpSet || string(got[0].Value) != "2" {
			t.Fatalf("sink ops = %+v, want single SET k=2", got)
		}
	})
}

func TestFlushFailureKeepsDirtyAndOrder(t *testing.T) {
	rec := &recorder{failTimes: 2}
	c := newCache(testOptions(rec.sink))
	defer c.Close(context.Background())

	_ = c.Set("a", []byte("1"))
	_ = c.Set("b", []byte("2"))

	for i := 0; i < 2; i++ {
		if err := c.flushOnce(); !errors.Is(err, errInjected) {
			t.Fatalf("flushOnce attempt %d err = %v, want %v", i+1, err, errInjected)
		}
		if n := c.DirtyLen(); n != 2 {
			t.Fatalf("dirty len after failed flush = %d, want 2 (dirty flags kept)", n)
		}
		if wm := c.Watermark(); wm != 0 {
			t.Fatalf("watermark after failed flush = %d, want 0", wm)
		}
	}

	if err := c.flushOnce(); err != nil {
		t.Fatalf("flushOnce retry: %v", err)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(opKeys(rec.ops()), want) {
		t.Fatalf("retry order = %v, want %v", opKeys(rec.ops()), want)
	}
	if wm := c.Watermark(); wm != 2 {
		t.Fatalf("watermark = %d, want 2", wm)
	}
}

func TestBatching(t *testing.T) {
	rec := &recorder{}
	opts := testOptions(rec.sink)
	opts.BatchSize = 2
	c := newCache(opts)
	defer c.Close(context.Background())

	for _, k := range []string{"k1", "k2", "k3", "k4", "k5"} {
		_ = c.Set(k, []byte("v"))
	}
	for i := 0; i < 3; i++ {
		if err := c.flushOnce(); err != nil {
			t.Fatalf("flushOnce: %v", err)
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var sizes []int
	for _, b := range rec.batches {
		sizes = append(sizes, len(b))
	}
	if want := []int{2, 2, 1}; !reflect.DeepEqual(sizes, want) {
		t.Fatalf("batch sizes = %v, want %v", sizes, want)
	}
}

func TestInjectedClock(t *testing.T) {
	rec := &recorder{}
	base := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	var tick int64
	opts := testOptions(rec.sink)
	opts.Now = func() time.Time {
		tick++
		return base.Add(time.Duration(tick) * time.Second)
	}
	c := newCache(opts)
	defer c.Close(context.Background())

	_ = c.Set("a", []byte("1"))
	_ = c.Set("b", []byte("2"))
	if err := c.flushOnce(); err != nil {
		t.Fatalf("flushOnce: %v", err)
	}
	got := rec.ops()
	if !got[0].At.Equal(base.Add(time.Second)) || !got[1].At.Equal(base.Add(2*time.Second)) {
		t.Fatalf("op timestamps = %v, %v; want injected clock values", got[0].At, got[1].At)
	}
}

func TestGetSeesUnflushedWrites(t *testing.T) {
	rec := &recorder{}
	c := newCache(testOptions(rec.sink))
	defer c.Close(context.Background())

	_ = c.Set("k", []byte("v"))
	if v, ok := c.Get("k"); !ok || string(v) != "v" {
		t.Fatalf("Get = %q, %v; want v, true", v, ok)
	}
	_ = c.Delete("k")
	if _, ok := c.Get("k"); ok {
		t.Fatal("Get after Delete = true, want false")
	}
}

func TestCloseDrainsAndReleasesResources(t *testing.T) {
	rec := &recorder{failTimes: 3} // exercise the background retry path
	c := New(testOptions(rec.sink))

	_ = c.Set("a", []byte("1"))
	_ = c.Set("b", []byte("2"))
	_ = c.Delete("a")
	_ = c.Set("a", []byte("3"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unflushed, err := c.Close(ctx)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(unflushed) != 0 {
		t.Fatalf("unflushed = %+v, want empty", unflushed)
	}
	if n := c.DirtyLen(); n != 0 {
		t.Fatalf("dirty len after Close = %d, want 0", n)
	}
	if wm := c.Watermark(); wm != 4 {
		t.Fatalf("watermark = %d, want 4", wm)
	}
	// Final source state and order.
	got := rec.ops()
	if want := []string{"a", "b"}; !reflect.DeepEqual(opKeys(got), want) {
		t.Fatalf("sink keys = %v, want %v", opKeys(got), want)
	}
	if want := []OpKind{OpSet, OpSet}; !reflect.DeepEqual(opKinds(got), want) {
		t.Fatalf("sink kinds = %v, want %v", opKinds(got), want)
	}
	if string(got[0].Value) != "3" {
		t.Fatalf("merged value = %q, want 3", got[0].Value)
	}
	// Resource release: the flusher goroutine exited before Close returned.
	select {
	case <-c.Done():
	default:
		t.Fatal("flusher goroutine still running after Close")
	}
	// Writes after Close are rejected.
	if err := c.Set("x", []byte("y")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Set after Close = %v, want ErrClosed", err)
	}
	if err := c.Delete("x"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Delete after Close = %v, want ErrClosed", err)
	}
}

func TestCloseReturnsUnflushed(t *testing.T) {
	rec := &recorder{alwaysErr: errInjected}
	c := New(testOptions(rec.sink))

	_ = c.Set("a", []byte("1"))
	_ = c.Set("b", []byte("2"))
	_ = c.Delete("c")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	unflushed, err := c.Close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close err = %v, want context.DeadlineExceeded", err)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(opKeys(unflushed), want) {
		t.Fatalf("unflushed keys = %v, want %v (order preserved)", opKeys(unflushed), want)
	}
	if n := c.DirtyLen(); n != 3 {
		t.Fatalf("dirty len = %d, want 3 (nothing lost)", n)
	}
	if wm := c.Watermark(); wm != 0 {
		t.Fatalf("watermark = %d, want 0", wm)
	}
	// Resource release even on the failure path.
	select {
	case <-c.Done():
	default:
		t.Fatal("flusher goroutine still running after failed Close")
	}
}
