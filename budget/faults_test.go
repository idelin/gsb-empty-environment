package budget

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestShortWriteRecoverable proves a transient short write is retried and the
// full payload is still staged exactly once.
func TestShortWriteRecoverable(t *testing.T) {
	tracker := &sinkTracker{}
	cfg := unfoldConfig{newSink: func() sink {
		fs := &faultySink{shortN: 3, shortOnce: true}
		tracker.mu.Lock()
		tracker.sinks = append(tracker.sinks, fs)
		tracker.mu.Unlock()
		return &trackingSink{inner: fs, tracker: tracker}
	}}

	root := bytesLeaf("x", []byte("0123456789")) // 10 bytes, first sink write takes 3
	res, err := unfold(context.Background(), stdLimits(), root, cfg)
	if err != nil {
		t.Fatalf("recoverable short write should succeed: %v", err)
	}
	if got := string(res.Leaves[0].Data); got != "0123456789" {
		t.Fatalf("staged data = %q, want full payload", got)
	}
	if res.TotalBytes != 10 {
		t.Fatalf("TotalBytes = %d, want 10", res.TotalBytes)
	}
}

// TestShortWritePermanent proves a sink making zero progress is surfaced as
// ErrShortWrite, nothing is published and every buffer is reclaimed.
func TestShortWritePermanent(t *testing.T) {
	tracker := &sinkTracker{}
	cfg := unfoldConfig{newSink: func() sink {
		fs := &faultySink{zero: true}
		tracker.mu.Lock()
		tracker.sinks = append(tracker.sinks, fs)
		tracker.mu.Unlock()
		return &trackingSink{inner: fs, tracker: tracker}
	}}

	var published atomic.Bool
	cfg.publish = func(context.Context, *Result) error { published.Store(true); return nil }

	root := bytesLeaf("x", []byte("abcdef"))
	res, err := unfold(context.Background(), stdLimits(), root, cfg)
	if !errors.Is(err, ErrShortWrite) {
		t.Fatalf("err = %v, want ErrShortWrite", err)
	}
	if res != nil {
		t.Fatalf("no result on failure, got %+v", res)
	}
	if published.Load() {
		t.Fatal("publish must never run on failure")
	}
	if !tracker.allReleased() {
		t.Fatal("staging buffers were not all reclaimed")
	}
}

// TestSinkHardError propagates an injected sink error and reclaims buffers.
func TestSinkHardError(t *testing.T) {
	injected := errors.New("disk on fire")
	tracker := &sinkTracker{}
	cfg := unfoldConfig{newSink: func() sink {
		fs := &faultySink{faultErr: injected}
		tracker.mu.Lock()
		tracker.sinks = append(tracker.sinks, fs)
		tracker.mu.Unlock()
		return &trackingSink{inner: fs, tracker: tracker}
	}}

	res, err := unfold(context.Background(), stdLimits(), bytesLeaf("x", []byte("data")), cfg)
	if !errors.Is(err, injected) {
		t.Fatalf("err = %v, want injected error", err)
	}
	if res != nil {
		t.Fatal("no result on failure")
	}
	if !tracker.allReleased() {
		t.Fatal("staging buffers leaked after sink error")
	}
}

// TestExternalCancellation proves caller cancellation aborts a long decode,
// publishes nothing and reclaims all staged buffers.
func TestExternalCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tracker := &sinkTracker{}

	started := make(chan struct{})
	root := &Node{
		Name: "slow",
		Decode: func(ctx context.Context, w io.Writer) error {
			close(started)
			if _, err := w.Write([]byte("partial")); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}

	cfg := unfoldConfig{
		newSink: tracker.factory(),
		publish: func(context.Context, *Result) error { t.Fatal("publish must not run"); return nil },
	}

	done := make(chan struct{})
	var (
		res *Result
		err error
	)
	go func() {
		res, err = unfold(ctx, stdLimits(), root, cfg)
		close(done)
	}()

	<-started
	cancel()
	<-done

	if err == nil {
		t.Fatal("expected cancellation error, got nil")
	}
	if res != nil {
		t.Fatal("no result after cancellation")
	}
	if !tracker.allReleased() {
		t.Fatal("partial staging buffer leaked after cancellation")
	}
}

// TestConcurrentSubstreamsRespectLiveBufferCap proves many parallel leaves are
// decoded concurrently yet the measured live-buffer peak never exceeds the cap.
func TestConcurrentSubstreamsRespectLiveBufferCap(t *testing.T) {
	const leafCount, capLive = 20, 3
	var inFlight, peakInFlight atomic.Int32

	children := make([]*Node, leafCount)
	for i := range children {
		children[i] = &Node{
			Name: fmt.Sprintf("leaf-%d", i),
			Decode: func(_ context.Context, w io.Writer) error {
				cur := inFlight.Add(1)
				for {
					recorded := peakInFlight.Load()
					if cur <= recorded || peakInFlight.CompareAndSwap(recorded, cur) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				inFlight.Add(-1)
				_, err := w.Write([]byte("abc"))
				return err
			},
		}
	}
	root := container("root", children...)

	limits := stdLimits()
	limits.MaxLeaves = leafCount
	limits.MaxLiveBuffers = capLive
	limits.MaxTotalBytes = leafCount * 3

	res, err := Unfold(context.Background(), limits, root)
	if err != nil {
		t.Fatalf("unfold: %v", err)
	}
	if len(res.Leaves) != leafCount {
		t.Fatalf("leaves = %d, want %d", len(res.Leaves), leafCount)
	}
	if got := peakInFlight.Load(); got > capLive || got <= 0 {
		t.Fatalf("observed concurrency %d, want within (0,%d]", got, capLive)
	}
	if res.PeakLiveBuffers > capLive || res.PeakLiveBuffers <= 0 {
		t.Fatalf("PeakLiveBuffers = %d, want within (0,%d]", res.PeakLiveBuffers, capLive)
	}
	if res.PeakBufferedBytes > limits.MaxTotalBytes {
		t.Fatalf("PeakBufferedBytes = %d exceeds budget %d", res.PeakBufferedBytes, limits.MaxTotalBytes)
	}
}

// TestNoPartialPublishOnLateFailure proves a failure on one leaf prevents
// publication even though sibling leaves decoded successfully first, and all
// sibling staging buffers are reclaimed.
func TestNoPartialPublishOnLateFailure(t *testing.T) {
	var publishCount atomic.Int32
	tracker := &sinkTracker{}

	good := bytesLeaf("good", []byte("payload"))
	bad := &Node{
		Name: "bad",
		Decode: func(_ context.Context, w io.Writer) error {
			if _, err := w.Write([]byte("x")); err != nil {
				return err
			}
			return errors.New("boom")
		},
	}
	root := container("root", good, good, bad)

	cfg := unfoldConfig{
		newSink: tracker.factory(),
		publish: func(context.Context, *Result) error {
			publishCount.Add(1)
			return nil
		},
	}

	res, err := unfold(context.Background(), stdLimits(), root, cfg)
	if err == nil {
		t.Fatal("expected failure, got nil")
	}
	if res != nil {
		t.Fatal("result must be nil when any leaf fails")
	}
	if got := publishCount.Load(); got != 0 {
		t.Fatalf("publish ran %d times, want 0", got)
	}
	if !tracker.allReleased() {
		t.Fatal("sibling staging buffers leaked after late failure")
	}
}

// TestBudgetViolationCancelsSiblings proves a byte-budget breach in one stream
// cancels the others rather than letting them keep decoding.
func TestBudgetViolationCancelsSiblings(t *testing.T) {
	var siblingSawCancel atomic.Bool
	var started atomic.Int32
	allStarted := make(chan struct{})
	closeOnce := sync.Once{}

	bomber := &Node{
		Name: "bomber",
		Decode: func(ctx context.Context, w io.Writer) error {
			if started.Add(1) == 2 {
				closeOnce.Do(func() { close(allStarted) })
			}
			// Wait until both streams are in flight so the sibling is able to
			// observe the cancellation triggered by the budget violation.
			select {
			case <-allStarted:
			case <-ctx.Done():
				return ctx.Err()
			}
			chunk := make([]byte, 64)
			for {
				if _, err := w.Write(chunk); err != nil {
					return err
				}
			}
		},
	}
	patient := &Node{
		Name: "patient",
		Decode: func(ctx context.Context, w io.Writer) error {
			if started.Add(1) == 2 {
				closeOnce.Do(func() { close(allStarted) })
			}
			select {
			case <-ctx.Done():
				siblingSawCancel.Store(true)
				return ctx.Err()
			case <-time.After(2 * time.Second):
				// Fallback safety; should normally be cancelled first.
				_, _ = w.Write([]byte("late"))
				return nil
			}
		},
	}

	limits := stdLimits()
	limits.MaxTotalBytes = 128
	limits.MaxLiveBuffers = 2
	root := container("root", bomber, patient)

	res, err := Unfold(context.Background(), limits, root)
	if !errors.Is(err, ErrTotalBytesExceeded) {
		t.Fatalf("err = %v, want ErrTotalBytesExceeded", err)
	}
	if res != nil {
		t.Fatal("no result after budget violation")
	}
	if !siblingSawCancel.Load() {
		t.Fatal("sibling stream was not cancelled after budget violation")
	}
}

// TestPublishOnlyOnceOnSuccess proves the publication seam runs exactly once
// and with the fully assembled result.
func TestPublishOnlyOnceOnSuccess(t *testing.T) {
	var once atomic.Int32
	var mu sync.Mutex
	var seen int
	cfg := unfoldConfig{
		publish: func(_ context.Context, r *Result) error {
			once.Add(1)
			mu.Lock()
			seen = len(r.Leaves)
			mu.Unlock()
			return nil
		},
	}
	root := container("root", bytesLeaf("a", []byte("1")), bytesLeaf("b", []byte("22")))
	res, err := unfold(context.Background(), stdLimits(), root, cfg)
	if err != nil {
		t.Fatalf("unfold: %v", err)
	}
	if once.Load() != 1 {
		t.Fatalf("publish ran %d times, want 1", once.Load())
	}
	if seen != 2 || len(res.Leaves) != 2 {
		t.Fatalf("published leaf count mismatch")
	}
}
