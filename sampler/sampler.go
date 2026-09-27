// Package sampler implements tail-based sampling for trace spans.
//
// Spans are buffered per trace until the trace completes (root span
// arrives), times out, or is evicted from the bounded in-flight cache.
// A trace is kept when any span carries an error, when its duration
// exceeds a latency threshold, or while the keep budget has tokens left.
// Incomplete traces are decided by a separate timeout policy. Spans that
// arrive after their trace was decided receive the same stored decision.
package sampler

import (
	"hash/fnv"
	"time"
)

// Reason explains why a trace was kept or dropped.
type Reason string

const (
	ReasonError   Reason = "error"   // at least one span carries an error
	ReasonLatency Reason = "latency" // trace duration exceeded the threshold
	ReasonBudget  Reason = "budget"  // kept within the keep budget
	ReasonTimeout Reason = "timeout" // trace never completed before its deadline
	ReasonEvicted Reason = "evicted" // pushed out of the bounded in-flight cache
	ReasonDrop    Reason = "drop"    // no keep rule matched
)

// Decision is the sampling outcome for a trace.
type Decision struct {
	TraceID string
	Keep    bool
	Reason  Reason
	Spans   int  // spans observed before the decision
	Late    bool // true when returned to a span arriving after the decision
}

// Span is a single telemetry span. A span with an empty ParentID is the
// root of its trace; the trace is considered complete when it arrives.
type Span struct {
	TraceID  string
	SpanID   string
	ParentID string
	Name     string
	Start    time.Time
	End      time.Time
	Err      bool
}

// IsRoot reports whether the span is the root of its trace.
func (s Span) IsRoot() bool { return s.ParentID == "" }

// TimeoutPolicy decides traces that never completed (deadline exceeded or
// evicted from the bounded cache).
type TimeoutPolicy int

const (
	// TimeoutDrop drops every incomplete trace.
	TimeoutDrop TimeoutPolicy = iota
	// TimeoutKeepAll keeps every incomplete trace.
	TimeoutKeepAll
	// TimeoutKeepError keeps incomplete traces that contain an error span.
	TimeoutKeepError
)

// Config tunes a Sampler. The zero values are replaced by defaults.
type Config struct {
	MaxTraces        int           // bound on in-flight (undecided) traces
	MaxDecisions     int           // bound on remembered decisions for late spans
	TraceTimeout     time.Duration // deadline for incomplete traces
	LatencyThreshold time.Duration // keep traces at least this slow
	BudgetPerWindow  int           // keep tokens refilled every BudgetWindow
	BudgetWindow     time.Duration
	NumShards        int // shard count used by ShardKey
	TimeoutPolicy    TimeoutPolicy
	Now              func() time.Time // clock, injectable for tests/replay
	OnDecision       func(Decision)   // optional hook for every new decision
}

const (
	defaultMaxTraces    = 1024
	defaultMaxDecisions = 4096
	defaultTraceTimeout = 30 * time.Second
	defaultBudgetWindow = time.Second
)

func (c Config) withDefaults() Config {
	if c.MaxTraces <= 0 {
		c.MaxTraces = defaultMaxTraces
	}
	if c.MaxDecisions <= 0 {
		c.MaxDecisions = defaultMaxDecisions
	}
	if c.TraceTimeout <= 0 {
		c.TraceTimeout = defaultTraceTimeout
	}
	if c.BudgetWindow <= 0 {
		c.BudgetWindow = defaultBudgetWindow
	}
	if c.NumShards <= 0 {
		c.NumShards = 1
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// ShardKey maps a trace ID to a shard. It depends only on the trace ID,
// so every shard routes spans of the same trace to the same decision key.
func ShardKey(traceID string, numShards int) uint32 {
	if numShards <= 0 {
		numShards = 1
	}
	h := fnv.New32a()
	h.Write([]byte(traceID))
	return h.Sum32() % uint32(numShards)
}

type traceBuf struct {
	spans  []Span
	first  time.Time
	hasErr bool
}

// Sampler buffers spans and makes tail-sampling decisions. It is not
// safe for concurrent use; guard it with a lock per shard if needed.
type Sampler struct {
	cfg Config

	pending map[string]*traceBuf
	order   []string // FIFO of pending trace IDs, for eviction

	decided      map[string]Decision
	decidedOrder []string // FIFO of decided trace IDs, for bounding

	tokens      int
	windowStart time.Time
}

// NewSampler returns a Sampler with defaults applied to cfg.
func NewSampler(cfg Config) *Sampler {
	return &Sampler{
		cfg:     cfg.withDefaults(),
		pending: make(map[string]*traceBuf),
		decided: make(map[string]Decision),
	}
}

// Shard returns the consistent shard for a trace ID under this sampler's
// configured shard count.
func (s *Sampler) Shard(traceID string) uint32 {
	return ShardKey(traceID, s.cfg.NumShards)
}

// Pending returns the number of in-flight (undecided) traces.
func (s *Sampler) Pending() int { return len(s.pending) }

// Decided returns the number of remembered decisions.
func (s *Sampler) Decided() int { return len(s.decided) }

// OnSpan ingests one span. It returns the trace decision and true when
// the span completes its trace or the trace was already decided (late
// span); otherwise it returns false and the span stays buffered.
func (s *Sampler) OnSpan(sp Span) (Decision, bool) {
	if sp.TraceID == "" {
		return Decision{}, false
	}
	if d, ok := s.decided[sp.TraceID]; ok {
		d.Late = true // same outcome, flagged as a late arrival
		return d, true
	}
	buf, ok := s.pending[sp.TraceID]
	if !ok {
		s.evictIfFull()
		buf = &traceBuf{first: s.cfg.Now()}
		s.pending[sp.TraceID] = buf
		s.order = append(s.order, sp.TraceID)
	}
	buf.spans = append(buf.spans, sp)
	if sp.Err {
		buf.hasErr = true
	}
	if sp.IsRoot() {
		return s.finalize(sp.TraceID, ""), true
	}
	return Decision{}, false
}

// Flush decides every pending trace whose deadline has passed at now,
// using the configured timeout policy, and returns those decisions.
func (s *Sampler) Flush(now time.Time) []Decision {
	var out []Decision
	for _, id := range append([]string(nil), s.order...) {
		buf, ok := s.pending[id]
		if !ok {
			continue
		}
		if now.Sub(buf.first) >= s.cfg.TraceTimeout {
			out = append(out, s.finalize(id, ReasonTimeout))
		}
	}
	return out
}

// evictIfFull keeps the in-flight cache bounded: the oldest pending
// traces are decided under the timeout policy until there is room.
func (s *Sampler) evictIfFull() {
	for len(s.pending) >= s.cfg.MaxTraces && len(s.order) > 0 {
		s.finalize(s.order[0], ReasonEvicted)
	}
}

// finalize decides a trace and removes it from the pending cache. An
// empty incomplete reason means the trace completed normally.
func (s *Sampler) finalize(traceID string, incomplete Reason) Decision {
	buf := s.pending[traceID]
	d := s.decide(traceID, buf, incomplete)
	delete(s.pending, traceID)
	for i, id := range s.order {
		if id == traceID {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.remember(d)
	if s.cfg.OnDecision != nil {
		s.cfg.OnDecision(d)
	}
	return d
}

func (s *Sampler) decide(traceID string, buf *traceBuf, incomplete Reason) Decision {
	d := Decision{TraceID: traceID, Spans: len(buf.spans)}
	if incomplete != "" {
		d.Reason = incomplete
		switch s.cfg.TimeoutPolicy {
		case TimeoutKeepAll:
			d.Keep = true
		case TimeoutKeepError:
			d.Keep = buf.hasErr
		}
		return d
	}
	if buf.hasErr {
		d.Keep, d.Reason = true, ReasonError
		return d
	}
	if s.cfg.LatencyThreshold > 0 && duration(buf.spans) >= s.cfg.LatencyThreshold {
		d.Keep, d.Reason = true, ReasonLatency
		return d
	}
	if s.allowBudget(s.cfg.Now()) {
		d.Keep, d.Reason = true, ReasonBudget
		return d
	}
	d.Reason = ReasonDrop
	return d
}

// duration spans the earliest start to the latest end across spans.
func duration(spans []Span) time.Duration {
	var lo, hi time.Time
	for i, sp := range spans {
		if i == 0 || sp.Start.Before(lo) {
			lo = sp.Start
		}
		if i == 0 || sp.End.After(hi) {
			hi = sp.End
		}
	}
	return hi.Sub(lo)
}

// allowBudget consumes one keep token, refilling once per window.
func (s *Sampler) allowBudget(now time.Time) bool {
	if s.cfg.BudgetPerWindow <= 0 {
		return false
	}
	if s.windowStart.IsZero() || now.Sub(s.windowStart) >= s.cfg.BudgetWindow {
		s.windowStart = now
		s.tokens = s.cfg.BudgetPerWindow
	}
	if s.tokens <= 0 {
		return false
	}
	s.tokens--
	return true
}

// remember stores a decision for late spans, bounded by MaxDecisions.
func (s *Sampler) remember(d Decision) {
	if _, ok := s.decided[d.TraceID]; ok {
		return
	}
	for len(s.decided) >= s.cfg.MaxDecisions && len(s.decidedOrder) > 0 {
		delete(s.decided, s.decidedOrder[0])
		s.decidedOrder = s.decidedOrder[1:]
	}
	s.decided[d.TraceID] = d
	s.decidedOrder = append(s.decidedOrder, d.TraceID)
}
