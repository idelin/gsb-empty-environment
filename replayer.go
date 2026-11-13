package netsec

import (
	"context"
	"sync"
)

// readBatch caps how many historical events one Log.Read pulls at a time,
// keeping replay memory bounded regardless of backlog size.
const readBatch = 64

// DefaultMaxConcurrentReplays is the per-source replay cap applied when the
// configuration leaves it unset.
const DefaultMaxConcurrentReplays = 2

// ReplayConfig configures a Manager.
type ReplayConfig struct {
	// MaxConcurrentPerSource limits simultaneous historical replays opened by
	// one source address. Zero or negative selects DefaultMaxConcurrentReplays.
	MaxConcurrentPerSource int
	// NewLimiter builds an independent rate Limiter for every accepted replay.
	// A nil factory means replay delivery is not throttled.
	NewLimiter LimiterFactory
}

// Manager runs subscription handshakes and replays on top of a Log, enforcing
// cursor validation outcomes, per-source replay concurrency, and rate limits.
type Manager struct {
	log        *Log
	newLimiter LimiterFactory

	mu     sync.Mutex
	active map[string]int
	maxPer int
}

// NewManager creates a Manager backed by log.
func NewManager(log *Log, cfg ReplayConfig) *Manager {
	max := cfg.MaxConcurrentPerSource
	if max <= 0 {
		max = DefaultMaxConcurrentReplays
	}
	return &Manager{
		log:        log,
		newLimiter: cfg.NewLimiter,
		active:     make(map[string]int),
		maxPer:     max,
	}
}

// Active returns how many replays the source currently has in flight.
func (m *Manager) Active(source string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[source]
}

// acquire takes one per-source replay slot. It returns false when the source
// is already at its concurrency cap.
func (m *Manager) acquire(source string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[source] >= m.maxPer {
		return false
	}
	m.active[source]++
	return true
}

// release returns a slot acquired with acquire.
func (m *Manager) release(source string) {
	m.mu.Lock()
	n := m.active[source]
	if n <= 1 {
		delete(m.active, source)
	} else {
		m.active[source] = n - 1
	}
	m.mu.Unlock()
}

// Handshake serves one subscription from its validated cursor until ctx is
// canceled or a terminal error occurs. The cursor must come from ParseCursor;
// Handshake enforces window and concurrency policy:
//
//   - cursor newer than the newest event: a FutureCursor error frame is
//     written, the sink closed, and ErrFutureCursor returned;
//   - cursor older than the retained window: an OutdatedCursor info frame is
//     written once, then replay starts at the earliest retained sequence;
//   - source at its replay cap: a ReplayLimit error frame is written, the
//     sink closed, and ErrReplayLimit returned;
//   - otherwise events are delivered in strictly increasing sequence order,
//     throttled by the configured limiter, with a gap-free handoff from
//     historical replay to the live edge.
//
// The sink is always closed exactly once before Handshake returns.
func (m *Manager) Handshake(ctx context.Context, source string, cursor uint64, sink FrameWriter) error {
	bounds := m.log.Bounds()
	if bounds.Newest != 0 && cursor > bounds.Newest {
		frame := errorFrame(ErrFutureCursor, cursor, bounds.Earliest, bounds.Newest)
		_ = sink.WriteFrame(ctx, frame)
		_ = sink.Close()
		return ErrFutureCursor
	}
	if !m.acquire(source) {
		frame := errorFrame(ErrReplayLimit, cursor, bounds.Earliest, bounds.Newest)
		_ = sink.WriteFrame(ctx, frame)
		_ = sink.Close()
		return ErrReplayLimit
	}

	var limiter Limiter
	if m.newLimiter != nil {
		limiter = m.newLimiter()
	}

	var live *LiveCursor
	defer func() {
		if live != nil {
			live.Cancel()
		}
		m.release(source)
		_ = sink.Close()
	}()

	start := cursor
	if bounds.Earliest != 0 && cursor < bounds.Earliest {
		info := Frame{
			Kind:     FrameOutdatedCursor,
			Seq:      cursor,
			Earliest: bounds.Earliest,
			Newest:   bounds.Newest,
		}
		if err := sink.WriteFrame(ctx, info); err != nil {
			return err
		}
		start = bounds.Earliest
	}

	next, err := m.replayHistory(ctx, start, limiter, sink)
	if err != nil {
		m.failSink(ctx, sink, err, next)
		return err
	}

	// Handoff: OpenCursor snapshots, under the log lock, every retained event
	// at or above next and registers for future appends atomically, so the
	// sequence stream across the boundary is neither gapped nor duplicated.
	live = m.log.OpenCursor(next)
	current := m.log.Bounds()
	if current.Earliest != 0 && next < current.Earliest {
		err = ErrWindowRolled
		m.failSink(ctx, sink, err, next)
		return err
	}

	for {
		ev, err := live.Next(ctx)
		if err != nil {
			return err
		}
		if ev.Seq != next {
			err = ErrWindowRolled
			m.failSink(ctx, sink, err, next)
			return err
		}
		if err := sink.WriteFrame(ctx, Frame{Kind: FrameEvent, Seq: ev.Seq, Payload: ev.Payload}); err != nil {
			return err
		}
		next = ev.Seq + 1
	}
}

// replayHistory drains retained events starting at start in batches until it
// catches up with the live edge, returning the sequence the live handoff must
// begin at. Every delivered event is preceded by a limiter token when a
// limiter is configured.
func (m *Manager) replayHistory(ctx context.Context, start uint64, limiter Limiter, sink FrameWriter) (uint64, error) {
	next := start
	for {
		// Re-check the retention window on every batch: a trim racing with
		// replay must fail the stream instead of silently delivering a
		// truncated backlog.
		if bounds := m.log.Bounds(); bounds.Earliest != 0 && next < bounds.Earliest {
			return next, ErrWindowRolled
		}
		batch, err := m.log.Read(next, next+readBatch-1, readBatch)
		if err != nil {
			return next, err
		}
		if len(batch) == 0 {
			return next, nil
		}
		for _, ev := range batch {
			// Defensive continuity check: the log is gap-free by contract, so
			// any mismatch means retention invalidated the replay.
			if ev.Seq != next {
				return next, ErrWindowRolled
			}
			if limiter != nil {
				if err := limiter.Wait(ctx); err != nil {
					return next, err
				}
			}
			if err := sink.WriteFrame(ctx, Frame{Kind: FrameEvent, Seq: ev.Seq, Payload: ev.Payload}); err != nil {
				return next, err
			}
			next = ev.Seq + 1
		}
	}
}

// failSink emits a terminal error frame for a replay failure. Sink closure is
// left to Handshake's deferred cleanup.
func (m *Manager) failSink(ctx context.Context, sink FrameWriter, err error, ref uint64) {
	bounds := m.log.Bounds()
	_ = sink.WriteFrame(ctx, errorFrame(err, ref, bounds.Earliest, bounds.Newest))
}
