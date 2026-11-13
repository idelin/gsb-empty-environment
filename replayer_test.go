package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// testSink collects frames and records closure.
type testSink struct {
	mu       sync.Mutex
	frames   []Frame
	closed   bool
	writeErr error
}

func (s *testSink) WriteFrame(_ context.Context, f Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	s.frames = append(s.frames, f)
	return nil
}

func (s *testSink) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *testSink) snapshot() []Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Frame, len(s.frames))
	copy(out, s.frames)
	return out
}

func (s *testSink) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *testSink) eventSeqs() []uint64 {
	frames := s.snapshot()
	seqs := make([]uint64, 0, len(frames))
	for _, f := range frames {
		if f.Kind == FrameEvent {
			seqs = append(seqs, f.Seq)
		}
	}
	return seqs
}

// countLimiter records how many tokens each replay consumed.
type countLimiter struct {
	mu    sync.Mutex
	waits int
}

func (l *countLimiter) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	l.mu.Lock()
	l.waits++
	l.mu.Unlock()
	return nil
}

func (l *countLimiter) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.waits
}

// gateLimiter blocks every Wait until release is closed, letting tests pin
// replays mid-delivery deterministically.
type gateLimiter struct {
	release chan struct{}
	once    sync.Once
}

func newGateLimiter() *gateLimiter {
	return &gateLimiter{release: make(chan struct{})}
}

func (l *gateLimiter) Wait(ctx context.Context) error {
	select {
	case <-l.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *gateLimiter) open() { l.once.Do(func() { close(l.release) }) }

func seedLog(t *testing.T, n int) *Log {
	t.Helper()
	log := NewLog()
	for i := 0; i < n; i++ {
		log.Append([]byte{byte('a' + i%26)})
	}
	return log
}

func assertContiguous(t *testing.T, seqs []uint64, from uint64) {
	t.Helper()
	for i, seq := range seqs {
		if seq != from+uint64(i) {
			t.Fatalf("discontinuity at index %d: got %d, want %d", i, seq, from+uint64(i))
		}
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !ok() {
		select {
		case <-deadline:
			t.Fatal("condition not met before timeout")
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func TestHandshakeMalformedCursorRejectedUpstream(t *testing.T) {
	// ParseCursor is the handshake entry point: malformed input must never
	// reach the manager as a usable cursor.
	log := seedLog(t, 3)
	mgr := NewManager(log, ReplayConfig{})
	for _, raw := range []string{"-1", "+1", "00", "0x2", "abc", "", " 2", "99999999999999999999"} {
		cursor, err := ParseCursor(raw)
		if !errors.Is(err, ErrMalformedCursor) {
			t.Fatalf("ParseCursor(%q) = %d, %v; want ErrMalformedCursor", raw, cursor, err)
		}
	}
	if mgr.Active("src") != 0 {
		t.Fatal("malformed cursors must not consume replay slots")
	}
}

func TestHandshakeFutureCursorErrorsAndCloses(t *testing.T) {
	log := seedLog(t, 5)
	mgr := NewManager(log, ReplayConfig{})
	sink := &testSink{}

	err := mgr.Handshake(context.Background(), "src", 6, sink)
	if !errors.Is(err, ErrFutureCursor) {
		t.Fatalf("Handshake err = %v, want ErrFutureCursor", err)
	}
	frames := sink.snapshot()
	if len(frames) != 1 || frames[0].Kind != FrameFutureCursor {
		t.Fatalf("frames = %+v, want one FutureCursor frame", frames)
	}
	if frames[0].Seq != 6 || frames[0].Newest != 5 {
		t.Fatalf("future frame refs = %+v, want req 6 newest 5", frames[0])
	}
	if !sink.isClosed() {
		t.Fatal("future-cursor stream must be closed")
	}
	if mgr.Active("src") != 0 {
		t.Fatal("failed handshake must release its replay slot")
	}
}

func TestHandshakeOutdatedCursorInfoThenReplayFromEarliest(t *testing.T) {
	log := seedLog(t, 10)
	log.TrimThrough(4) // window is now 5..10
	mgr := NewManager(log, ReplayConfig{})
	sink := &testSink{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Handshake(ctx, "src", 2, sink) }()

	// Wait for the full historical window to be replayed, then stop the
	// subscription at the live edge.
	waitFor(t, func() bool { return len(sink.eventSeqs()) == 6 })
	cancel()
	err := <-done
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Handshake exit err = %v, want context.Canceled", err)
	}

	frames := sink.snapshot()
	if len(frames) < 2 || frames[0].Kind != FrameOutdatedCursor {
		t.Fatalf("first frame = %+v, want OutdatedCursor info", frames)
	}
	info := frames[0]
	if info.Seq != 2 || info.Earliest != 5 || info.Newest != 10 {
		t.Fatalf("outdated frame = %+v, want req 2 earliest 5 newest 10", info)
	}
	if frames[1].Kind != FrameEvent || frames[1].Seq != 5 {
		t.Fatalf("second frame = %+v, want first event at seq 5", frames[1])
	}

	seqs := sink.eventSeqs()
	assertContiguous(t, seqs, 5)
	if len(seqs) != 6 || seqs[5] != 10 {
		t.Fatalf("replayed seqs = %v, want 5..10", seqs)
	}
}

func TestHandshakeInWindowCursorReplaysExactTail(t *testing.T) {
	log := seedLog(t, 8)
	mgr := NewManager(log, ReplayConfig{})
	sink := &testSink{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Handshake(ctx, "src", 6, sink) }()
	waitFor(t, func() bool { return len(sink.eventSeqs()) == 3 })
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Handshake exit err = %v, want context.Canceled", err)
	}
	seqs := sink.eventSeqs()
	assertContiguous(t, seqs, 6)
	if len(seqs) != 3 {
		t.Fatalf("seqs = %v, want 6..8", seqs)
	}
	for _, f := range sink.snapshot() {
		if f.Kind != FrameEvent {
			t.Fatalf("unexpected diagnostic frame: %+v", f)
		}
	}
}

// TestHandshakeHandoffWithConcurrentWrites verifies the replay/live boundary
// under writers that keep appending through the handoff: no gaps, no dupes.
func TestHandshakeHandoffWithConcurrentWrites(t *testing.T) {
	log := seedLog(t, 40)
	mgr := NewManager(log, ReplayConfig{})
	sink := &testSink{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- mgr.Handshake(ctx, "src", 1, sink) }()

	var writerWG sync.WaitGroup
	for w := 0; w < 3; w++ {
		writerWG.Add(1)
		go func() {
			defer writerWG.Done()
			for i := 0; i < 40; i++ {
				log.Append(nil)
				time.Sleep(time.Millisecond)
			}
		}()
	}
	writerWG.Wait() // seq 41..160 now exist

	deadline := time.After(5 * time.Second)
	for {
		seqs := sink.eventSeqs()
		if len(seqs) >= 160 {
			assertContiguous(t, seqs, 1)
			if seqs[len(seqs)-1] != 160 {
				t.Fatalf("last seq = %d, want 160", seqs[len(seqs)-1])
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out with %d events; have %v", len(seqs), seqs)
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Handshake exit err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handshake did not stop after cancel")
	}
	if !sink.isClosed() {
		t.Fatal("sink must be closed when handshake ends")
	}
}

// TestHandshakeThirdConcurrentReplayRejected enforces the per-source cap of
// two while confirming another source is unaffected.
func TestHandshakeThirdConcurrentReplayRejected(t *testing.T) {
	log := seedLog(t, 60)
	gate := newGateLimiter()
	mgr := NewManager(log, ReplayConfig{NewLimiter: func() Limiter { return gate }})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	run := func(source string, cursor uint64) (*testSink, chan error) {
		sink := &testSink{}
		errc := make(chan error, 1)
		go func() { errc <- mgr.Handshake(ctx, source, cursor, sink) }()
		return sink, errc
	}

	sink1, _ := run("attacker", 1)
	sink2, _ := run("attacker", 10)

	waitFor(t, func() bool { return mgr.Active("attacker") == 2 })

	sink3 := &testSink{}
	rejectErr := mgr.Handshake(ctx, "attacker", 20, sink3)
	if !errors.Is(rejectErr, ErrReplayLimit) {
		t.Fatalf("third replay err = %v, want ErrReplayLimit", rejectErr)
	}
	frames := sink3.snapshot()
	if len(frames) != 1 || frames[0].Kind != FrameReplayLimit {
		t.Fatalf("third replay frames = %+v, want one ReplayLimit frame", frames)
	}
	if !sink3.isClosed() {
		t.Fatal("rejected stream must be closed")
	}
	if mgr.Active("attacker") != 2 {
		t.Fatal("rejected replay must not consume a slot")
	}

	// A different source has its own independent budget.
	sinkOther, _ := run("victim", 30)
	waitFor(t, func() bool { return mgr.Active("victim") == 1 })

	gate.open()
	cancel()
	waitFor(t, func() bool { return mgr.Active("attacker") == 0 && mgr.Active("victim") == 0 })
	if !sink1.isClosed() || !sink2.isClosed() || !sinkOther.isClosed() {
		t.Fatal("all accepted streams must be closed on cancel")
	}
}

func TestHandshakeRateLimitAppliedPerEvent(t *testing.T) {
	log := seedLog(t, 25)
	var buckets []*countLimiter
	var mu sync.Mutex
	mgr := NewManager(log, ReplayConfig{NewLimiter: func() Limiter {
		l := &countLimiter{}
		mu.Lock()
		buckets = append(buckets, l)
		mu.Unlock()
		return l
	}})
	sink := &testSink{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Handshake(ctx, "src", 11, sink) }()
	waitFor(t, func() bool {
		mu.Lock()
		ready := len(buckets) == 1 && buckets[0].count() == 15
		mu.Unlock()
		return ready
	})
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Handshake exit err = %v, want context.Canceled", err)
	}
	if len(buckets) != 1 {
		t.Fatalf("limiter instances = %d, want one per replay", len(buckets))
	}
	// Historical replay delivers seq 11..25: exactly one token per event.
	if got := buckets[0].count(); got != 15 {
		t.Fatalf("limiter waits = %d, want 15", got)
	}
	assertContiguous(t, sink.eventSeqs(), 11)
}

// TestHandshakeRealTokenBucketThrottlesBacklog uses the production
// TokenBucket with a real clock to prove a caller-supplied rate cap actually
// stretches a backlog replay instead of bursting it instantly.
func TestHandshakeRealTokenBucketThrottlesBacklog(t *testing.T) {
	log := seedLog(t, 12)
	// 100 events/sec, burst 1: after the first event, each further event
	// needs ~10ms, so 12 events take at least 100ms.
	mgr := NewManager(log, ReplayConfig{NewLimiter: func() Limiter {
		return NewTokenBucket(100, 1, nil)
	}})
	sink := &testSink{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- mgr.Handshake(ctx, "src", 1, sink) }()
	waitFor(t, func() bool { return len(sink.eventSeqs()) == 12 })
	elapsed := time.Since(start)
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Handshake exit err = %v", err)
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("backlog delivered in %v, rate limiter did not throttle", elapsed)
	}
	assertContiguous(t, sink.eventSeqs(), 1)
}

func TestHandshakeWindowRollsDuringReplay(t *testing.T) {
	log := seedLog(t, 100)
	gate := newGateLimiter()
	mgr := NewManager(log, ReplayConfig{NewLimiter: func() Limiter { return gate }})
	sink := &testSink{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- mgr.Handshake(ctx, "src", 1, sink) }()

	waitFor(t, func() bool { return mgr.Active("src") == 1 })
	// Pin the replay before its first event, then trim past its cursor so the
	// next historical Read observes the rolled window.
	log.TrimThrough(70)
	gate.open()
	log.Append(nil) // wakes the live cursor so it observes the roll

	select {
	case err := <-done:
		if !errors.Is(err, ErrWindowRolled) {
			t.Fatalf("Handshake err = %v, want ErrWindowRolled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replay did not fail after window rolled")
	}
	cancel()

	frames := sink.snapshot()
	last := frames[len(frames)-1]
	if last.Kind != FrameReplayFailed {
		t.Fatalf("last frame = %+v, want ReplayFailed", last)
	}
	if !sink.isClosed() {
		t.Fatal("failed stream must be closed")
	}
}
