package netsec_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q-019"
)

func emit(data []byte) netsec.DecodeFunc {
	return func(_ context.Context, w io.Writer) error {
		_, err := w.Write(data)
		return err
	}
}

type trackedStage struct {
	tracker *stageTracker
	label   string
	short   int
	buf     bytes.Buffer
}

func (s *trackedStage) Write(p []byte) (int, error) {
	n := len(p)
	if s.short > 0 && n > s.short {
		n = s.short
	}
	_, _ = s.buf.Write(p[:n])
	s.tracker.add(n)
	return n, nil
}

func (s *trackedStage) Bytes() []byte { return s.buf.Bytes() }

func (s *trackedStage) Reset() {
	n := s.buf.Len()
	s.buf.Reset()
	s.tracker.reset(n)
}

type stageTracker struct {
	mu      sync.Mutex
	live    int64
	peak    int64
	created int
	resets  int
	writes  int
	short   map[string]int
}

func newStageTracker() *stageTracker {
	return &stageTracker{short: make(map[string]int)}
}

func (t *stageTracker) factory(label string) netsec.Stage {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.created++
	return &trackedStage{tracker: t, label: label, short: t.short[label]}
}

func (t *stageTracker) add(n int) {
	t.mu.Lock()
	t.live += int64(n)
	if t.live > t.peak {
		t.peak = t.live
	}
	t.writes++
	t.mu.Unlock()
}

func (t *stageTracker) reset(n int) {
	t.mu.Lock()
	t.live -= int64(n)
	t.resets++
	t.mu.Unlock()
}

func (t *stageTracker) snapshot() (live, peak int64, created, resets, writes int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.live, t.peak, t.created, t.resets, t.writes
}

func requireReclaimed(t *testing.T, tracker *stageTracker) {
	t.Helper()
	live, _, created, resets, _ := tracker.snapshot()
	if live != 0 {
		t.Fatalf("staging still live after failure: %d bytes", live)
	}
	if resets != created {
		t.Fatalf("reclaimed %d stages, created %d", resets, created)
	}
}

func waitStarted(t *testing.T, started <-chan struct{}, n int) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for i := 0; i < n; i++ {
		select {
		case <-started:
		case <-timer.C:
			t.Fatalf("only %d of %d decoders reached the concurrency barrier", i, n)
		}
	}
}

func TestExpandSuccessAndStats(t *testing.T) {
	root := netsec.Container("root",
		netsec.Stream("a", 1, emit([]byte("hello"))),
		netsec.Container("sub",
			netsec.Stream("b", 999, emit([]byte("world"))),
		),
		netsec.Container("empty"),
	)

	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 100,
		MaxDepth:        3,
		MaxLeaves:       5,
		MaxLiveBuffer:   100,
	})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if res == nil || len(res.Roots) != 1 {
		t.Fatalf("unexpected result: %#v", res)
	}
	got := res.Roots[0]
	if got.Name != "root" || got.Kind != netsec.KindContainer || len(got.Children) != 3 {
		t.Fatalf("unexpected root: %#v", got)
	}
	if string(got.Children[0].Data) != "hello" {
		t.Fatalf("stream a = %q", got.Children[0].Data)
	}
	sub := got.Children[1]
	if len(sub.Children) != 1 || string(sub.Children[0].Data) != "world" {
		t.Fatalf("unexpected sub: %#v", sub)
	}
	if got.Children[2].Kind != netsec.KindContainer || len(got.Children[2].Children) != 0 {
		t.Fatalf("unexpected empty container: %#v", got.Children[2])
	}
	if res.Stats.DecodedBytes != 10 || res.Stats.Leaves != 3 ||
		res.Stats.MaxDepth != 3 || res.Stats.PeakLiveBytes != 10 {
		t.Fatalf("unexpected stats: %+v", res.Stats)
	}
}

func TestDeclaredSizeIsNeverTrusted(t *testing.T) {
	t.Run("overrun is rejected before stage write", func(t *testing.T) {
		tracker := newStageTracker()
		root := netsec.Stream("liar", 1, emit(bytes.Repeat([]byte("x"), 11)))
		res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
			MaxDecodedBytes: 10,
			MaxDepth:        1,
			MaxLeaves:       1,
			MaxLiveBuffer:   100,
		}, netsec.WithStageFactory(tracker.factory))
		if err == nil {
			t.Fatal("expected an overrun error")
		}
		if res != nil {
			t.Fatalf("partial result published: %#v", res)
		}
		if !errors.Is(err, netsec.ErrBudgetExceeded) {
			t.Fatalf("error = %v, want budget error", err)
		}
		var budgetErr *netsec.BudgetError
		if !errors.As(err, &budgetErr) || budgetErr.Dimension != netsec.DimensionDecodedBytes {
			t.Fatalf("unexpected budget error: %#v", err)
		}
		_, _, _, _, writes := tracker.snapshot()
		if writes != 0 {
			t.Fatalf("rejected bytes reached staging: %d write calls", writes)
		}
		requireReclaimed(t, tracker)
	})

	t.Run("large truthful overdeclaration does not fail", func(t *testing.T) {
		root := netsec.Stream("modest", 1<<40, emit([]byte("ok")))
		res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
			MaxDecodedBytes: 10,
			MaxDepth:        1,
			MaxLeaves:       1,
			MaxLiveBuffer:   10,
		})
		if err != nil {
			t.Fatalf("Expand: %v", err)
		}
		if got := string(res.Roots[0].Data); got != "ok" {
			t.Fatalf("data = %q", got)
		}
		if res.Stats.DecodedBytes != 2 {
			t.Fatalf("decoded = %d", res.Stats.DecodedBytes)
		}
	})
}

func TestDepthLimitRejectsBeforeDecode(t *testing.T) {
	var decodes atomic.Int64
	leaf := netsec.Stream("leaf", 0, func(context.Context, io.Writer) error {
		decodes.Add(1)
		return nil
	})
	root := netsec.Container("root",
		netsec.Container("two",
			netsec.Container("three", leaf),
		),
	)

	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 100,
		MaxDepth:        3,
		MaxLeaves:       10,
		MaxLiveBuffer:   100,
	})
	if err == nil || !errors.Is(err, netsec.ErrDepthExceeded) {
		t.Fatalf("error = %v, want depth error", err)
	}
	if res != nil {
		t.Fatalf("partial result published: %#v", res)
	}
	if decodes.Load() != 0 {
		t.Fatalf("decoder ran %d times before depth rejection", decodes.Load())
	}
}

func TestLeafLimitCountsSharedReferences(t *testing.T) {
	var decodes atomic.Int64
	leaf := netsec.Stream("shared", 0, func(context.Context, io.Writer) error {
		decodes.Add(1)
		return nil
	})
	root := netsec.Container("root", leaf, leaf)

	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 100,
		MaxDepth:        2,
		MaxLeaves:       1,
		MaxLiveBuffer:   100,
	})
	if err == nil || !errors.Is(err, netsec.ErrLeavesExceeded) {
		t.Fatalf("error = %v, want leaf error", err)
	}
	if res != nil {
		t.Fatalf("partial result published: %#v", res)
	}
	if decodes.Load() != 0 {
		t.Fatalf("decoder ran %d times before leaf rejection", decodes.Load())
	}
}

func TestSharedReferenceConsumesBudgetEachTime(t *testing.T) {
	tracker := newStageTracker()
	var decodes atomic.Int64
	leaf := netsec.Stream("shared", 0, func(_ context.Context, w io.Writer) error {
		decodes.Add(1)
		_, err := w.Write([]byte("abcdef"))
		return err
	})
	root := netsec.Container("root", leaf, leaf)

	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 10,
		MaxDepth:        2,
		MaxLeaves:       2,
		MaxLiveBuffer:   100,
	}, netsec.WithConcurrency(1), netsec.WithStageFactory(tracker.factory))
	if err == nil || !errors.Is(err, netsec.ErrBudgetExceeded) {
		t.Fatalf("error = %v, want budget error", err)
	}
	if res != nil {
		t.Fatalf("partial result published: %#v", res)
	}
	if decodes.Load() != 2 {
		t.Fatalf("decoder ran %d times, want 2", decodes.Load())
	}
	_, _, _, _, _ = tracker.snapshot()
	requireReclaimed(t, tracker)
}

func TestRejectedWriterIsStickyAndNeverChargesAgain(t *testing.T) {
	tracker := newStageTracker()
	root := netsec.Stream("greedy", 0, func(_ context.Context, w io.Writer) error {
		chunk := bytes.Repeat([]byte("x"), 10)
		for i := 0; i < 3; i++ {
			_, _ = w.Write(chunk)
		}
		return nil
	})
	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 10,
		MaxDepth:        1,
		MaxLeaves:       1,
		MaxLiveBuffer:   100,
	}, netsec.WithConcurrency(1), netsec.WithStageFactory(tracker.factory))
	if err == nil || !errors.Is(err, netsec.ErrBudgetExceeded) {
		t.Fatalf("error = %v, want budget error", err)
	}
	if res != nil {
		t.Fatalf("partial result published: %#v", res)
	}
	_, _, _, _, writes := tracker.snapshot()
	if writes != 1 {
		t.Fatalf("stage accepted %d writes, want exactly 1", writes)
	}
	requireReclaimed(t, tracker)
}

func TestCyclesAreRejectedBeforeAnyDecode(t *testing.T) {
	var decodes atomic.Int64
	stream := netsec.Stream("stream", 0, func(context.Context, io.Writer) error {
		decodes.Add(1)
		return nil
	})

	self := netsec.Container("self")
	self.Children = []*netsec.Node{self}
	root := netsec.Container("root", stream, self)

	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 100,
		MaxDepth:        10,
		MaxLeaves:       10,
		MaxLiveBuffer:   100,
	})
	if err == nil || !errors.Is(err, netsec.ErrCycle) {
		t.Fatalf("error = %v, want cycle error", err)
	}
	if res != nil {
		t.Fatalf("partial result published: %#v", res)
	}
	if decodes.Load() != 0 {
		t.Fatalf("decoder ran %d times before cycle rejection", decodes.Load())
	}
}

func TestMutualCycleIsRejected(t *testing.T) {
	a := netsec.Container("a")
	b := netsec.Container("b", a)
	a.Children = []*netsec.Node{b}
	_, err := netsec.Expand(context.Background(), []*netsec.Node{a}, netsec.Budget{
		MaxDecodedBytes: 10,
		MaxDepth:        10,
		MaxLeaves:       10,
		MaxLiveBuffer:   10,
	})
	if err == nil || !errors.Is(err, netsec.ErrCycle) {
		t.Fatalf("error = %v, want cycle error", err)
	}
}

func TestConcurrentSubstreamsRespectMeasuredPeak(t *testing.T) {
	tracker := newStageTracker()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	remaining := int32(2)

	decoder := func(data []byte) netsec.DecodeFunc {
		return func(ctx context.Context, w io.Writer) error {
			started <- struct{}{}
			if atomic.AddInt32(&remaining, -1) == 0 {
				close(release)
			}
			<-release
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			_, err := w.Write(data)
			return err
		}
	}

	root := netsec.Container("root",
		netsec.Stream("left", 0, decoder([]byte("abcd"))),
		netsec.Stream("right", 0, decoder([]byte("wxyz"))),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		res *netsec.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := netsec.Expand(ctx, []*netsec.Node{root}, netsec.Budget{
			MaxDecodedBytes: 8,
			MaxDepth:        2,
			MaxLeaves:       2,
			MaxLiveBuffer:   8,
		}, netsec.WithConcurrency(2), netsec.WithStageFactory(tracker.factory))
		done <- outcome{res: res, err: err}
	}()

	waitStarted(t, started, 2)
	got := <-done
	if got.err != nil {
		t.Fatalf("Expand: %v", got.err)
	}
	if got.res == nil || len(got.res.Roots[0].Children) != 2 {
		t.Fatalf("unexpected result: %#v", got.res)
	}
	if string(got.res.Roots[0].Children[0].Data) != "abcd" ||
		string(got.res.Roots[0].Children[1].Data) != "wxyz" {
		t.Fatalf("unexpected data: %#v", got.res.Roots[0].Children)
	}
	_, gaugePeak, _, _, _ := tracker.snapshot()
	if gaugePeak > 8 {
		t.Fatalf("measured peak %d exceeds budget 8", gaugePeak)
	}
	if got.res.Stats.PeakLiveBytes != 8 {
		t.Fatalf("stats peak = %d, want 8", got.res.Stats.PeakLiveBytes)
	}
}

func TestConcurrentLiveLimitFailsWithoutPartialPublish(t *testing.T) {
	tracker := newStageTracker()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	remaining := int32(2)

	decoder := func(data []byte) netsec.DecodeFunc {
		return func(ctx context.Context, w io.Writer) error {
			started <- struct{}{}
			if atomic.AddInt32(&remaining, -1) == 0 {
				close(release)
			}
			<-release
			_, err := w.Write(data)
			return err
		}
	}

	root := netsec.Container("root",
		netsec.Stream("left", 0, decoder(bytes.Repeat([]byte("a"), 6))),
		netsec.Stream("right", 0, decoder(bytes.Repeat([]byte("b"), 6))),
	)
	res, err := func() (*netsec.Result, error) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		return netsec.Expand(ctx, []*netsec.Node{root}, netsec.Budget{
			MaxDecodedBytes: 100,
			MaxDepth:        2,
			MaxLeaves:       2,
			MaxLiveBuffer:   8,
		}, netsec.WithConcurrency(2), netsec.WithStageFactory(tracker.factory))
	}()

	if err == nil || !errors.Is(err, netsec.ErrLiveExceeded) {
		t.Fatalf("error = %v, want live-buffer error", err)
	}
	if res != nil {
		t.Fatalf("partial result published: %#v", res)
	}
	live, gaugePeak, _, _, writes := tracker.snapshot()
	if live != 0 {
		t.Fatalf("staging still live: %d", live)
	}
	if gaugePeak > 8 {
		t.Fatalf("measured peak %d exceeds budget 8", gaugePeak)
	}
	if writes != 1 {
		t.Fatalf("stage accepted %d writes, want exactly 1 before rejection", writes)
	}
	requireReclaimed(t, tracker)
}

func TestInjectedShortWriteReclaimsAllStaging(t *testing.T) {
	tracker := newStageTracker()
	tracker.short["short"] = 2

	root := netsec.Container("root",
		netsec.Stream("ok", 0, emit([]byte("abc"))),
		netsec.Stream("short", 0, emit([]byte("abcde"))),
	)
	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 100,
		MaxDepth:        2,
		MaxLeaves:       2,
		MaxLiveBuffer:   100,
	}, netsec.WithConcurrency(1), netsec.WithStageFactory(tracker.factory))
	if err == nil || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error = %v, want io.ErrShortWrite", err)
	}
	if res != nil {
		t.Fatalf("partial result published: %#v", res)
	}
	_, _, _, _, _ = tracker.snapshot()
	requireReclaimed(t, tracker)
}

func TestBudgetFailureCancelsDescendants(t *testing.T) {
	tracker := newStageTracker()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	remaining := int32(2)
	var canceled atomic.Bool

	fast := netsec.Stream("fast", 0, func(ctx context.Context, w io.Writer) error {
		started <- struct{}{}
		if atomic.AddInt32(&remaining, -1) == 0 {
			close(release)
		}
		<-release
		_, err := w.Write(bytes.Repeat([]byte("x"), 11))
		return err
	})
	slow := netsec.Stream("slow", 0, func(ctx context.Context, _ io.Writer) error {
		started <- struct{}{}
		if atomic.AddInt32(&remaining, -1) == 0 {
			close(release)
		}
		<-release
		<-ctx.Done()
		canceled.Store(true)
		return ctx.Err()
	})
	root := netsec.Container("root", fast, slow)

	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 10,
		MaxDepth:        2,
		MaxLeaves:       2,
		MaxLiveBuffer:   100,
	}, netsec.WithConcurrency(2), netsec.WithStageFactory(tracker.factory))
	if err == nil || !errors.Is(err, netsec.ErrBudgetExceeded) {
		t.Fatalf("error = %v, want budget error", err)
	}
	if res != nil {
		t.Fatalf("partial result published: %#v", res)
	}
	if !canceled.Load() {
		t.Fatal("descendant decoder did not observe cancellation")
	}
	requireReclaimed(t, tracker)
}

func TestExternalCancellation(t *testing.T) {
	tracker := newStageTracker()
	started := make(chan struct{}, 2)
	ctx, cancel := context.WithCancel(context.Background())

	decoder := func(context.Context, io.Writer) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	root := netsec.Container("root",
		netsec.Stream("left", 0, decoder),
		netsec.Stream("right", 0, decoder),
	)

	type outcome struct {
		res *netsec.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := netsec.Expand(ctx, []*netsec.Node{root}, netsec.Budget{
			MaxDecodedBytes: 10,
			MaxDepth:        2,
			MaxLeaves:       2,
			MaxLiveBuffer:   10,
		}, netsec.WithConcurrency(2), netsec.WithStageFactory(tracker.factory))
		done <- outcome{res: res, err: err}
	}()

	waitStarted(t, started, 2)
	cancel()
	got := <-done
	if got.err == nil || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", got.err)
	}
	if got.res != nil {
		t.Fatalf("partial result published: %#v", got.res)
	}
	requireReclaimed(t, tracker)
}

func TestWriterIsClosedAfterDecodeReturns(t *testing.T) {
	var captured io.Writer
	release := make(chan struct{})
	late := make(chan error, 1)
	root := netsec.Stream("rogue", 0, func(_ context.Context, w io.Writer) error {
		captured = w
		go func() {
			<-release
			_, err := w.Write([]byte("late"))
			late <- err
		}()
		return nil
	})

	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 10,
		MaxDepth:        1,
		MaxLeaves:       1,
		MaxLiveBuffer:   10,
	})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(res.Roots[0].Data) != 0 {
		t.Fatalf("unexpected data before late write: %q", res.Roots[0].Data)
	}
	close(release)
	if err := <-late; !errors.Is(err, netsec.ErrWriterClosed) {
		t.Fatalf("late write error = %v, want ErrWriterClosed", err)
	}
	if len(res.Roots[0].Data) != 0 {
		t.Fatalf("published data was mutated after expansion: %q", res.Roots[0].Data)
	}
	if captured == nil {
		t.Fatal("decoder did not receive a writer")
	}
	if res.Stats.DecodedBytes != 0 || res.Stats.PeakLiveBytes != 0 {
		t.Fatalf("late write consumed budget: %+v", res.Stats)
	}
}

func TestDecoderPanicFailsAndReclaims(t *testing.T) {
	tracker := newStageTracker()
	root := netsec.Stream("boom", 0, func(context.Context, io.Writer) error {
		panic("attacker panic")
	})
	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 10,
		MaxDepth:        1,
		MaxLeaves:       1,
		MaxLiveBuffer:   10,
	}, netsec.WithStageFactory(tracker.factory))
	if err == nil {
		t.Fatal("expected panic error")
	}
	if res != nil {
		t.Fatalf("partial result published: %#v", res)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("panicked")) {
		t.Fatalf("error does not report panic: %v", err)
	}
	requireReclaimed(t, tracker)
}

func TestDecoderGoexitFailsAndReclaims(t *testing.T) {
	tracker := newStageTracker()
	root := netsec.Stream("exit", 0, func(context.Context, io.Writer) error {
		runtime.Goexit()
		return nil
	})
	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 10,
		MaxDepth:        1,
		MaxLeaves:       1,
		MaxLiveBuffer:   10,
	}, netsec.WithStageFactory(tracker.factory))
	if err == nil {
		t.Fatal("expected a failure when a decoder calls runtime.Goexit")
	}
	if res != nil {
		t.Fatalf("partial result published: %#v", res)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("without returning")) {
		t.Fatalf("unexpected error: %v", err)
	}
	requireReclaimed(t, tracker)
}

func TestEmptyContainerConsumesLeafBudget(t *testing.T) {
	root := netsec.Container("root",
		netsec.Container("a"),
		netsec.Container("b"),
	)
	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 10,
		MaxDepth:        2,
		MaxLeaves:       1,
		MaxLiveBuffer:   10,
	})
	if err == nil || !errors.Is(err, netsec.ErrLeavesExceeded) {
		t.Fatalf("error = %v, want leaf error", err)
	}
	if res != nil {
		t.Fatalf("partial result published: %#v", res)
	}
}

func TestMalformedNodesAreRejected(t *testing.T) {
	bad := &netsec.Node{
		Name:     "both",
		Decode:   emit([]byte("x")),
		Children: []*netsec.Node{netsec.Container("child")},
	}
	_, err := netsec.Expand(context.Background(), []*netsec.Node{bad}, netsec.Budget{
		MaxDecodedBytes: 10,
		MaxDepth:        10,
		MaxLeaves:       10,
		MaxLiveBuffer:   10,
	})
	if err == nil || !errors.Is(err, netsec.ErrInvalidTree) {
		t.Fatalf("error = %v, want invalid-tree error", err)
	}
}

func TestZeroDepthAllowsNoNodes(t *testing.T) {
	var decodes atomic.Int64
	root := netsec.Stream("root", 0, func(context.Context, io.Writer) error {
		decodes.Add(1)
		return nil
	})
	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 10,
		MaxDepth:        0,
		MaxLeaves:       1,
		MaxLiveBuffer:   10,
	})
	if err == nil || !errors.Is(err, netsec.ErrDepthExceeded) {
		t.Fatalf("error = %v, want depth error", err)
	}
	if res != nil || decodes.Load() != 0 {
		t.Fatalf("unexpected execution: result=%v decodes=%d", res, decodes.Load())
	}
}

func TestCancellationBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var decodes atomic.Int64
	root := netsec.Stream("root", 0, func(context.Context, io.Writer) error {
		decodes.Add(1)
		return nil
	})
	res, err := netsec.Expand(ctx, []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 10,
		MaxDepth:        1,
		MaxLeaves:       1,
		MaxLiveBuffer:   10,
	})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if res != nil || decodes.Load() != 0 {
		t.Fatalf("unexpected execution: result=%v decodes=%d", res, decodes.Load())
	}
}

func TestInvalidConfigurationIsRejected(t *testing.T) {
	valid := netsec.Budget{MaxDecodedBytes: 1, MaxDepth: 1, MaxLeaves: 1, MaxLiveBuffer: 1}
	tests := []struct {
		name   string
		budget netsec.Budget
		opts   []netsec.Option
	}{
		{name: "negative bytes", budget: netsec.Budget{MaxDecodedBytes: -1, MaxDepth: 1}},
		{name: "negative depth", budget: netsec.Budget{MaxDepth: -1}},
		{name: "negative leaves", budget: netsec.Budget{MaxLeaves: -1}},
		{name: "negative live", budget: netsec.Budget{MaxLiveBuffer: -1}},
		{name: "zero concurrency", budget: valid, opts: []netsec.Option{netsec.WithConcurrency(0)}},
		{name: "nil stage", budget: valid, opts: []netsec.Option{netsec.WithStageFactory(nil)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := netsec.New(tt.budget, tt.opts...)
			if !errors.Is(err, netsec.ErrInvalidConfig) {
				t.Fatalf("error = %v, want invalid-config error", err)
			}
		})
	}
}

func TestNilNodeIsRejected(t *testing.T) {
	res, err := netsec.Expand(context.Background(), []*netsec.Node{nil}, netsec.Budget{
		MaxDecodedBytes: 1,
		MaxDepth:        1,
		MaxLeaves:       1,
		MaxLiveBuffer:   1,
	})
	if err == nil || !errors.Is(err, netsec.ErrInvalidTree) {
		t.Fatalf("error = %v, want invalid-tree error", err)
	}
	if res != nil {
		t.Fatalf("partial result published: %#v", res)
	}
}

func TestZeroByteStream(t *testing.T) {
	root := netsec.Stream("empty-stream", 123, func(context.Context, io.Writer) error { return nil })
	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 0,
		MaxDepth:        1,
		MaxLeaves:       1,
		MaxLiveBuffer:   0,
	})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if res.Roots[0].Kind != netsec.KindStream || len(res.Roots[0].Data) != 0 {
		t.Fatalf("unexpected result: %#v", res.Roots[0])
	}
}

func TestDeepTreeDoesNotUseRecursion(t *testing.T) {
	const depth = 20000
	leaf := netsec.Stream("leaf", 0, func(context.Context, io.Writer) error { return nil })
	root := netsec.Container("n0", leaf)
	for i := 1; i < depth; i++ {
		root = netsec.Container(fmt.Sprintf("n%d", i), root)
	}

	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 0,
		MaxDepth:        depth + 1,
		MaxLeaves:       1,
		MaxLiveBuffer:   0,
	})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if res.Stats.MaxDepth != depth+1 || res.Stats.Leaves != 1 {
		t.Fatalf("unexpected stats: %+v", res.Stats)
	}

	cur := res.Roots[0]
	levels := 0
	for cur.Kind == netsec.KindContainer {
		levels++
		if len(cur.Children) != 1 {
			t.Fatalf("level %d has %d children", levels, len(cur.Children))
		}
		cur = cur.Children[0]
	}
	if levels != depth || cur.Name != "leaf" {
		t.Fatalf("result depth = %d, leaf = %q", levels, cur.Name)
	}
}

func ExampleExpand() {
	root := netsec.Container("archive",
		netsec.Stream("readme.txt", 5, emit([]byte("hello"))),
	)
	res, err := netsec.Expand(context.Background(), []*netsec.Node{root}, netsec.Budget{
		MaxDecodedBytes: 1 << 20,
		MaxDepth:        4,
		MaxLeaves:       100,
		MaxLiveBuffer:   1 << 20,
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(res.Roots[0].Children[0].Data))
	// Output: hello
}
