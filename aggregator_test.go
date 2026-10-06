package aggregator

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// scriptSink is a fully scriptable Sink: every WriteBatch call is handed to
// the test over the calls channel and blocks until the test sends a response.
// Tests drive it step by step, so no real waiting is involved.
type scriptSink struct {
	calls     chan [][]byte
	responses chan step
}

type step struct {
	n   int
	err error
}

func newScriptSink() *scriptSink {
	return &scriptSink{
		calls:     make(chan [][]byte),
		responses: make(chan step),
	}
}

func (s *scriptSink) WriteBatch(_ context.Context, items [][]byte) (int, error) {
	s.calls <- items
	st := <-s.responses
	return st.n, st.err
}

// expectBatch blocks until the sink receives a batch and asserts its payload.
func (s *scriptSink) expectBatch(t *testing.T, want ...string) {
	t.Helper()
	got := <-s.calls
	strs := make([]string, len(got))
	for i, b := range got {
		strs[i] = string(b)
	}
	if !reflect.DeepEqual(strs, want) {
		t.Fatalf("batch = %v, want %v", strs, want)
	}
}

// respond answers the pending WriteBatch call.
func (s *scriptSink) respond(n int, err error) {
	s.responses <- step{n: n, err: err}
}

// expectNoBatch fails if a flush is in flight. Only safe when the aggregator
// is quiescent, i.e. all flushes are synchronous with the calls under test.
func (s *scriptSink) expectNoBatch(t *testing.T) {
	t.Helper()
	select {
	case b := <-s.calls:
		t.Fatalf("unexpected flush of %d items", len(b))
	default:
	}
}

// submitAsync runs Submit in a goroutine and reports the returned receipt
// channel once Submit unblocks.
func submitAsync(a *Aggregator, data string) chan (<-chan error) {
	ch := make(chan (<-chan error), 1)
	go func() { ch <- a.Submit([]byte(data)) }()
	return ch
}

func recvReceipt(t *testing.T, name string, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	default:
		t.Fatalf("receipt %s not resolved", name)
		return nil
	}
}

func TestCountFlushBoundaries(t *testing.T) {
	sink := newScriptSink()
	agg := New(sink, 2)

	r1 := agg.Submit([]byte("a"))
	sink.expectNoBatch(t) // below the count threshold: nothing flushed

	r2ch := submitAsync(agg, "b") // completes the batch, blocks on the sink
	sink.expectBatch(t, "a", "b")
	sink.respond(2, nil)
	r2 := <-r2ch

	if err := recvReceipt(t, "a", r1); err != nil {
		t.Fatalf("receipt a = %v", err)
	}
	if err := recvReceipt(t, "b", r2); err != nil {
		t.Fatalf("receipt b = %v", err)
	}

	// The next batch starts empty; a lone item stays pending until a barrier.
	r3 := agg.Submit([]byte("c"))
	sink.expectNoBatch(t)

	barrierDone := make(chan error, 1)
	go func() { barrierDone <- agg.Barrier(context.Background()) }()
	sink.expectBatch(t, "c")
	sink.respond(1, nil)
	if err := <-barrierDone; err != nil {
		t.Fatalf("barrier = %v", err)
	}
	if err := recvReceipt(t, "c", r3); err != nil {
		t.Fatalf("receipt c = %v", err)
	}
}

func TestShortWritePartialReceipts(t *testing.T) {
	sink := newScriptSink()
	agg := New(sink, 3)
	boom := errors.New("disk full")

	r1 := agg.Submit([]byte("a"))
	r2 := agg.Submit([]byte("b"))
	r3ch := submitAsync(agg, "c")
	sink.expectBatch(t, "a", "b", "c")
	sink.respond(1, boom) // only the prefix "a" was written
	r3 := <-r3ch

	if err := recvReceipt(t, "a", r1); err != nil {
		t.Fatalf("receipt a = %v, want nil", err)
	}
	if err := recvReceipt(t, "b", r2); !errors.Is(err, boom) {
		t.Fatalf("receipt b = %v, want %v", err, boom)
	}
	if err := recvReceipt(t, "c", r3); !errors.Is(err, boom) {
		t.Fatalf("receipt c = %v, want %v", err, boom)
	}
}

func TestShortWriteWithoutError(t *testing.T) {
	sink := newScriptSink()
	agg := New(sink, 2)

	r1 := agg.Submit([]byte("a"))
	r2ch := submitAsync(agg, "b")
	sink.expectBatch(t, "a", "b")
	sink.respond(1, nil) // short write, no error reported by the sink
	r2 := <-r2ch

	if err := recvReceipt(t, "a", r1); err != nil {
		t.Fatalf("receipt a = %v, want nil", err)
	}
	if err := recvReceipt(t, "b", r2); !errors.Is(err, ErrShortWrite) {
		t.Fatalf("receipt b = %v, want ErrShortWrite", err)
	}
}

func TestFailedBatchFailsNextBarrier(t *testing.T) {
	sink := newScriptSink()
	agg := New(sink, 2)
	boom := errors.New("sink exploded")

	// A size-triggered batch fails.
	r1 := agg.Submit([]byte("a"))
	r2ch := submitAsync(agg, "b")
	sink.expectBatch(t, "a", "b")
	sink.respond(0, boom)
	r2 := <-r2ch
	if err := recvReceipt(t, "a", r1); !errors.Is(err, boom) {
		t.Fatalf("receipt a = %v, want %v", err, boom)
	}
	if err := recvReceipt(t, "b", r2); !errors.Is(err, boom) {
		t.Fatalf("receipt b = %v, want %v", err, boom)
	}

	// The next barrier flushes a healthy batch, yet must still report the
	// earlier failure instead of a false success.
	r3 := agg.Submit([]byte("c"))
	barrierDone := make(chan error, 1)
	go func() { barrierDone <- agg.Barrier(context.Background()) }()
	sink.expectBatch(t, "c")
	sink.respond(1, nil)
	if err := <-barrierDone; !errors.Is(err, boom) {
		t.Fatalf("barrier = %v, want %v", err, boom)
	}
	if err := recvReceipt(t, "c", r3); err != nil {
		t.Fatalf("receipt c = %v", err)
	}

	// The barrier consumed the failure; the next one starts clean.
	if err := agg.Barrier(context.Background()); err != nil {
		t.Fatalf("second barrier = %v, want nil", err)
	}
	sink.expectNoBatch(t)
}

func TestCloseStopsAcceptingAndFlushes(t *testing.T) {
	sink := newScriptSink()
	agg := New(sink, 10)

	r1 := agg.Submit([]byte("a"))
	r2 := agg.Submit([]byte("b"))
	sink.expectNoBatch(t)

	closeDone := make(chan error, 1)
	go func() { closeDone <- agg.Close(context.Background()) }()
	sink.expectBatch(t, "a", "b")

	// While Close is flushing, new submissions are rejected immediately.
	r3 := agg.Submit([]byte("c"))
	if err := recvReceipt(t, "c", r3); !errors.Is(err, ErrClosed) {
		t.Fatalf("receipt c = %v, want ErrClosed", err)
	}

	sink.respond(2, nil)
	if err := <-closeDone; err != nil {
		t.Fatalf("close = %v", err)
	}
	if err := recvReceipt(t, "a", r1); err != nil {
		t.Fatalf("receipt a = %v", err)
	}
	if err := recvReceipt(t, "b", r2); err != nil {
		t.Fatalf("receipt b = %v", err)
	}
}

func TestCloseReportsAccumulatedFailure(t *testing.T) {
	sink := newScriptSink()
	agg := New(sink, 2)
	boom := errors.New("write failed")

	agg.Submit([]byte("a"))
	r2ch := submitAsync(agg, "b")
	sink.expectBatch(t, "a", "b")
	sink.respond(0, boom)
	<-r2ch

	// Close flushes the remainder and surfaces the earlier failure once.
	agg.Submit([]byte("c"))
	closeDone := make(chan error, 1)
	go func() { closeDone <- agg.Close(context.Background()) }()
	sink.expectBatch(t, "c")
	sink.respond(1, nil)
	if err := <-closeDone; !errors.Is(err, boom) {
		t.Fatalf("close = %v, want %v", err, boom)
	}

	if err := agg.Close(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("second close = %v, want ErrClosed", err)
	}
}

// TestCloseContention races submissions against Close. Every receipt must
// resolve to either nil (accepted and flushed) or ErrClosed (rejected);
// none may be lost or left hanging.
func TestCloseContention(t *testing.T) {
	sink := newScriptSink()
	agg := New(sink, 4)

	// The responder acknowledges every batch in full until told to stop.
	stop := make(chan struct{})
	var mu sync.Mutex
	var flushed []string
	go func() {
		for {
			select {
			case batch := <-sink.calls:
				mu.Lock()
				for _, b := range batch {
					flushed = append(flushed, string(b))
				}
				mu.Unlock()
				sink.responses <- step{n: len(batch)}
			case <-stop:
				return
			}
		}
	}()

	const submitters = 8
	var resultsMu sync.Mutex
	var results []error
	var wg sync.WaitGroup
	for i := 0; i < submitters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; ; j++ {
				data := fmt.Sprintf("w%d-%d", id, j)
				err := <-agg.Submit([]byte(data))
				resultsMu.Lock()
				results = append(results, err)
				resultsMu.Unlock()
				if errors.Is(err, ErrClosed) {
					return
				}
			}
		}(i)
	}

	// A few submissions are accepted synchronously, so some items are
	// guaranteed to be in flight when Close starts.
	var pre []<-chan error
	for i := 0; i < submitters; i++ {
		pre = append(pre, agg.Submit([]byte(fmt.Sprintf("pre-%d", i))))
	}

	// Close races the in-flight submitters from yet another goroutine.
	closeDone := make(chan error, 1)
	go func() { closeDone <- agg.Close(context.Background()) }()
	if err := <-closeDone; err != nil {
		t.Fatalf("close = %v", err)
	}
	wg.Wait()
	close(stop)

	for i, r := range pre {
		if err := <-r; err != nil {
			t.Fatalf("pre receipt %d = %v, want nil", i, err)
		}
	}

	var succeeded, rejected int
	resultsMu.Lock()
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrClosed):
			rejected++
		default:
			t.Fatalf("unexpected receipt error: %v", err)
		}
	}
	resultsMu.Unlock()
	if rejected != submitters {
		t.Fatalf("rejected = %d, want one ErrClosed per submitter (%d)", rejected, submitters)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != succeeded+len(pre) {
		t.Fatalf("flushed %d items but %d receipts succeeded", len(flushed), succeeded+len(pre))
	}
	// Every accepted item must have reached the sink exactly once.
	seen := make(map[string]int)
	for _, s := range flushed {
		seen[s]++
	}
	for s, n := range seen {
		if n != 1 {
			t.Fatalf("item %q flushed %d times", s, n)
		}
	}
	t.Logf("contention: %d succeeded, %d rejected, %d flushed", succeeded+len(pre), rejected, len(flushed))
}

func TestBarrierEmptyIsNoop(t *testing.T) {
	sink := newScriptSink()
	agg := New(sink, 2)
	if err := agg.Barrier(context.Background()); err != nil {
		t.Fatalf("barrier = %v, want nil", err)
	}
	sink.expectNoBatch(t)
}

func TestSubmitCopiesData(t *testing.T) {
	sink := newScriptSink()
	agg := New(sink, 2)

	buf := []byte("aa")
	agg.Submit(buf)
	buf[0] = 'x' // mutate after submit; the pending copy must be unaffected
	r2ch := submitAsync(agg, "b")
	sink.expectBatch(t, "aa", "b")
	sink.respond(2, nil)
	<-r2ch
}

func TestNewPanicsOnBadArgs(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for maxBatch < 1")
		}
	}()
	New(newScriptSink(), 0)
}

func TestReceiptErrorMessages(t *testing.T) {
	if !strings.Contains(ErrClosed.Error(), "closed") {
		t.Fatalf("ErrClosed message = %q", ErrClosed)
	}
	if !strings.Contains(ErrShortWrite.Error(), "short write") {
		t.Fatalf("ErrShortWrite message = %q", ErrShortWrite)
	}
}
