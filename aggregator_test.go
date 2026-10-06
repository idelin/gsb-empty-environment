package batchagg

import (
	"errors"
	"reflect"
	"testing"
)

var errBoom = errors.New("boom")

type writeResult struct {
	n   int
	err error
}

// gateSink is a fully orchestrable Sink. Every WriteBatch call announces
// its batch on entered and then blocks until the test supplies the result
// on release. Tests drive the sink explicitly and never sleep.
type gateSink struct {
	entered chan []string
	release chan writeResult
}

func newGateSink() *gateSink {
	return &gateSink{
		entered: make(chan []string, 32),
		release: make(chan writeResult, 32),
	}
}

func (g *gateSink) WriteBatch(batch []string) (int, error) {
	cp := append([]string(nil), batch...)
	g.entered <- cp
	res := <-g.release
	return res.n, res.err
}

// enteredBatches drains the batches announced so far without blocking.
func (g *gateSink) enteredBatches() [][]string {
	var batches [][]string
	for {
		select {
		case b := <-g.entered:
			batches = append(batches, b)
		default:
			return batches
		}
	}
}

func submitAll(t *testing.T, agg *Aggregator[string], items ...string) []*Receipt {
	t.Helper()
	recs := make([]*Receipt, 0, len(items))
	for _, item := range items {
		r, err := agg.Submit(item)
		if err != nil {
			t.Fatalf("Submit(%q): %v", item, err)
		}
		recs = append(recs, r)
	}
	return recs
}

func wantReceiptErrs(t *testing.T, recs []*Receipt, want []error) {
	t.Helper()
	for i, r := range recs {
		err := r.Err()
		if !errors.Is(err, want[i]) {
			t.Fatalf("receipt %d: got %v, want %v", i, err, want[i])
		}
	}
}

// Count-triggered flushes happen exactly at MaxBatch items, never earlier.
func TestCountTriggerFlushesExactBatch(t *testing.T) {
	sink := newGateSink()
	agg := New[string](sink, Options{MaxBatch: 3})

	recs := submitAll(t, agg, "a", "b")
	if got := sink.enteredBatches(); len(got) != 0 {
		t.Fatalf("flush before MaxBatch reached: %v", got)
	}

	done := make(chan *Receipt, 1)
	go func() {
		r, err := agg.Submit("c")
		if err != nil {
			t.Errorf("Submit(c): %v", err)
		}
		done <- r
	}()

	batch := <-sink.entered
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(batch, want) {
		t.Fatalf("batch = %v, want %v", batch, want)
	}
	sink.release <- writeResult{n: 3}
	recs = append(recs, <-done)

	wantReceiptErrs(t, recs, []error{nil, nil, nil})
	if got := sink.enteredBatches(); len(got) != 0 {
		t.Fatalf("unexpected extra flush: %v", got)
	}
}

// A barrier flushes a partial batch.
func TestBarrierFlushesPartialBatch(t *testing.T) {
	sink := newGateSink()
	agg := New[string](sink, Options{MaxBatch: 10})

	recs := submitAll(t, agg, "a", "b")

	flushDone := make(chan error, 1)
	go func() { flushDone <- agg.Flush() }()

	batch := <-sink.entered
	if want := []string{"a", "b"}; !reflect.DeepEqual(batch, want) {
		t.Fatalf("batch = %v, want %v", batch, want)
	}
	sink.release <- writeResult{n: 2}

	if err := <-flushDone; err != nil {
		t.Fatalf("Flush: %v", err)
	}
	wantReceiptErrs(t, recs, []error{nil, nil})
}

// A short write with an error marks the written prefix as successful and
// fails the remainder with the sink's error.
func TestPartialSuccessMarksPrefixOnly(t *testing.T) {
	sink := newGateSink()
	agg := New[string](sink, Options{MaxBatch: 4})

	recs := submitAll(t, agg, "a", "b", "c")
	done := make(chan *Receipt, 1)
	go func() {
		r, err := agg.Submit("d")
		if err != nil {
			t.Errorf("Submit(d): %v", err)
		}
		done <- r
	}()

	if batch := <-sink.entered; len(batch) != 4 {
		t.Fatalf("batch = %v, want 4 items", batch)
	}
	sink.release <- writeResult{n: 2, err: errBoom}
	recs = append(recs, <-done)

	wantReceiptErrs(t, recs, []error{nil, nil, errBoom, errBoom})
}

// A short write without an error fails the remainder with ErrShortWrite.
func TestShortWriteWithoutError(t *testing.T) {
	sink := newGateSink()
	agg := New[string](sink, Options{MaxBatch: 3})

	recs := submitAll(t, agg, "a", "b")
	done := make(chan *Receipt, 1)
	go func() {
		r, err := agg.Submit("c")
		if err != nil {
			t.Errorf("Submit(c): %v", err)
		}
		done <- r
	}()

	<-sink.entered
	sink.release <- writeResult{n: 1}
	recs = append(recs, <-done)

	wantReceiptErrs(t, recs, []error{nil, ErrShortWrite, ErrShortWrite})
}

// A batch that failed before the barrier must still make the barrier
// report failure; once reported, later barriers see only new items.
func TestBarrierReportsEarlierBatchFailure(t *testing.T) {
	sink := newGateSink()
	agg := New[string](sink, Options{MaxBatch: 2})

	// First count-triggered flush fails entirely.
	sink.release <- writeResult{n: 0, err: errBoom}
	recs := submitAll(t, agg, "x", "y")
	wantReceiptErrs(t, recs, []error{errBoom, errBoom})

	// A later item flushes fine, but the barrier must not report success.
	sink.release <- writeResult{n: 1}
	recZ, err := agg.Submit("z")
	if err != nil {
		t.Fatalf("Submit(z): %v", err)
	}
	if err := agg.Flush(); !errors.Is(err, errBoom) {
		t.Fatalf("Flush after failed batch = %v, want errBoom", err)
	}
	if err := recZ.Err(); err != nil {
		t.Fatalf("receipt z: %v", err)
	}

	// The failure was reported once; a barrier over only healthy items
	// succeeds.
	sink.release <- writeResult{n: 1}
	if _, err := agg.Submit("w"); err != nil {
		t.Fatalf("Submit(w): %v", err)
	}
	if err := agg.Flush(); err != nil {
		t.Fatalf("Flush over healthy items = %v, want nil", err)
	}
}

// Close flushes the remaining buffer, completes outstanding receipts, and
// rejects further work.
func TestCloseFlushesAndStopsAccepting(t *testing.T) {
	sink := newGateSink()
	agg := New[string](sink, Options{MaxBatch: 10})

	recs := submitAll(t, agg, "a", "b")

	closeDone := make(chan error, 1)
	go func() { closeDone <- agg.Close() }()

	batch := <-sink.entered
	if want := []string{"a", "b"}; !reflect.DeepEqual(batch, want) {
		t.Fatalf("batch = %v, want %v", batch, want)
	}
	sink.release <- writeResult{n: 2}

	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
	wantReceiptErrs(t, recs, []error{nil, nil})

	if _, err := agg.Submit("c"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit after Close = %v, want ErrClosed", err)
	}
	if err := agg.Flush(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Flush after Close = %v, want ErrClosed", err)
	}
	if err := agg.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("second Close = %v, want ErrClosed", err)
	}
}

// Close contending with an in-flight count-triggered flush must wait for
// it; no receipt completes early and no item is lost.
func TestCloseWaitsForInflightFlush(t *testing.T) {
	sink := newGateSink()
	agg := New[string](sink, Options{MaxBatch: 2})

	recA, err := agg.Submit("a")
	if err != nil {
		t.Fatalf("Submit(a): %v", err)
	}

	submitDone := make(chan *Receipt, 1)
	go func() {
		r, err := agg.Submit("b")
		if err != nil {
			t.Errorf("Submit(b): %v", err)
		}
		submitDone <- r
	}()

	// The count-triggered flush is now blocked inside the sink.
	if batch := <-sink.entered; !reflect.DeepEqual(batch, []string{"a", "b"}) {
		t.Fatalf("batch = %v, want [a b]", batch)
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- agg.Close() }()

	// Close cannot finish while the flush is in flight: it needs the
	// aggregator lock, which the blocked Submit still holds.
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned %v while a flush was in flight", err)
	default:
	}

	sink.release <- writeResult{n: 2}
	recB := <-submitDone

	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
	wantReceiptErrs(t, []*Receipt{recA, recB}, []error{nil, nil})
	if got := sink.enteredBatches(); len(got) != 0 {
		t.Fatalf("Close flushed phantom batch: %v", got)
	}
	if _, err := agg.Submit("c"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit after Close = %v, want ErrClosed", err)
	}
}

// A failed final flush surfaces on Close and on the item receipts.
func TestCloseReturnsFlushError(t *testing.T) {
	sink := newGateSink()
	agg := New[string](sink, Options{MaxBatch: 10})

	recs := submitAll(t, agg, "a")

	closeDone := make(chan error, 1)
	go func() { closeDone <- agg.Close() }()

	<-sink.entered
	sink.release <- writeResult{n: 0, err: errBoom}

	if err := <-closeDone; !errors.Is(err, errBoom) {
		t.Fatalf("Close = %v, want errBoom", err)
	}
	wantReceiptErrs(t, recs, []error{errBoom})
}

// With MaxBatch disabled, only explicit barriers flush.
func TestNoCountTriggerWithoutMaxBatch(t *testing.T) {
	sink := newGateSink()
	agg := New[string](sink, Options{MaxBatch: 0})

	recs := submitAll(t, agg, "a", "b", "c", "d", "e")
	if got := sink.enteredBatches(); len(got) != 0 {
		t.Fatalf("flush without barrier: %v", got)
	}

	flushDone := make(chan error, 1)
	go func() { flushDone <- agg.Flush() }()

	batch := <-sink.entered
	if want := []string{"a", "b", "c", "d", "e"}; !reflect.DeepEqual(batch, want) {
		t.Fatalf("batch = %v, want %v", batch, want)
	}
	sink.release <- writeResult{n: 5}

	if err := <-flushDone; err != nil {
		t.Fatalf("Flush: %v", err)
	}
	wantReceiptErrs(t, recs, []error{nil, nil, nil, nil, nil})
}
