package tailsampler

import (
	"sort"
	"time"
)

// Config tunes the sampler. Zero values get safe defaults from NewSampler.
type Config struct {
	// MaxBufferedTraces bounds how many undecided traces are held at once.
	MaxBufferedTraces int
	// MaxBufferedSpans bounds the total number of buffered spans across all
	// undecided traces. Exceeding either bound force-decides the oldest trace.
	MaxBufferedSpans int
	// TraceTimeout is how long an incomplete trace may stay buffered before
	// the timeout sweep decides it with the timeout policy.
	TraceTimeout time.Duration
	// DecisionTTL is how long a made decision is remembered so late spans of
	// the same trace follow the same result.
	DecisionTTL time.Duration
	// BudgetPerSecond caps kept traces per second (token bucket).
	BudgetPerSecond float64
	// LatencyThreshold keeps completed traces at least this slow.
	LatencyThreshold time.Duration
	// Clock is optional; defaults to the system clock.
	Clock Clock
}

// Stats counts decisions by outcome and reason.
type Stats struct {
	Kept     int
	Dropped  int
	ByReason map[string]int
}

// DecidedTrace pairs a trace ID with the decision made for it.
type DecidedTrace struct {
	TraceID  string
	Decision Decision
}

type traceState struct {
	spans     []Span
	hasError  bool
	complete  bool
	minStart  time.Time
	maxEnd    time.Time
	firstSeen time.Time
}

type decisionRecord struct {
	decision Decision
	expires  time.Time
}

// Sampler is a tail sampler: it buffers spans per trace until the trace
// completes, times out, or is evicted by cache pressure, then decides.
type Sampler struct {
	cfg    Config
	clock  Clock
	policy CompositePolicy
	budget *tokenBucket

	traces    map[string]*traceState
	order     []string // FIFO of undecided trace IDs, for eviction
	spanCount int
	decided   map[uint64]decisionRecord // keyed by DecisionKey(traceID)

	Stats Stats
}

func NewSampler(cfg Config) *Sampler {
	if cfg.MaxBufferedTraces <= 0 {
		cfg.MaxBufferedTraces = 1024
	}
	if cfg.MaxBufferedSpans <= 0 {
		cfg.MaxBufferedSpans = 16384
	}
	if cfg.TraceTimeout <= 0 {
		cfg.TraceTimeout = 30 * time.Second
	}
	if cfg.DecisionTTL <= 0 {
		cfg.DecisionTTL = 5 * time.Minute
	}
	if cfg.BudgetPerSecond <= 0 {
		cfg.BudgetPerSecond = 100
	}
	clock := cfg.Clock
	if clock == nil {
		clock = realClock{}
	}
	return &Sampler{
		cfg:     cfg,
		clock:   clock,
		policy:  CompositePolicy{LatencyThreshold: cfg.LatencyThreshold, TimeoutPolicy: TimeoutKeepPolicy{LatencyThreshold: cfg.LatencyThreshold}},
		budget:  newTokenBucket(cfg.BudgetPerSecond, clock.Now()),
		traces:  make(map[string]*traceState),
		decided: make(map[uint64]decisionRecord),
		Stats:   Stats{ByReason: map[string]int{}},
	}
}

// OnSpan ingests one span. It returns the trace's decision and true when this
// span settles the outcome (trace completion, or a late span matching a
// cached decision); it returns DecisionPending and false while buffering.
func (s *Sampler) OnSpan(sp Span) (Decision, bool) {
	now := s.clock.Now()
	key := DecisionKey(sp.TraceID)

	// Late span for an already-decided trace: follow the same result.
	if rec, ok := s.decided[key]; ok {
		if now.Before(rec.expires) {
			return rec.decision, true
		}
		delete(s.decided, key)
	}

	st, ok := s.traces[sp.TraceID]
	if !ok {
		st = &traceState{firstSeen: now, minStart: sp.Start, maxEnd: sp.End}
		s.traces[sp.TraceID] = st
		s.order = append(s.order, sp.TraceID)
	}
	st.spans = append(st.spans, sp)
	s.spanCount++
	st.hasError = st.hasError || sp.Error
	if sp.Start.Before(st.minStart) {
		st.minStart = sp.Start
	}
	if sp.End.After(st.maxEnd) {
		st.maxEnd = sp.End
	}
	if sp.IsRoot() {
		st.complete = true
	}

	// Bounded cache: force-decide oldest traces when over the limits.
	for s.spanCount > s.cfg.MaxBufferedSpans || len(s.traces) > s.cfg.MaxBufferedTraces {
		if !s.evictOldest() {
			break
		}
	}

	if st, ok := s.traces[sp.TraceID]; ok && st.complete {
		return s.finalize(sp.TraceID, false, "")
	}
	return DecisionPending, false
}

// Tick advances housekeeping: it decides traces that exceeded TraceTimeout
// with the timeout policy and expires old cached decisions. Call it
// periodically (the fixture replayer calls it on "tick" events).
func (s *Sampler) Tick() []DecidedTrace {
	now := s.clock.Now()
	var timedOut []string
	for id, st := range s.traces {
		if now.Sub(st.firstSeen) >= s.cfg.TraceTimeout {
			timedOut = append(timedOut, id)
		}
	}
	sort.Strings(timedOut) // deterministic order
	var out []DecidedTrace
	for _, id := range timedOut {
		if d, ok := s.finalize(id, true, ""); ok {
			out = append(out, DecidedTrace{TraceID: id, Decision: d})
		}
	}
	for key, rec := range s.decided {
		if !now.Before(rec.expires) {
			delete(s.decided, key)
		}
	}
	return out
}

// BufferedSpans reports the current buffered (undecided) span count.
func (s *Sampler) BufferedSpans() int { return s.spanCount }

// BufferedTraces reports the current undecided trace count.
func (s *Sampler) BufferedTraces() int { return len(s.traces) }

func (s *Sampler) evictOldest() bool {
	if len(s.order) == 0 {
		return false
	}
	oldest := s.order[0]
	s.finalize(oldest, true, "cache_pressure_")
	return true
}

func (s *Sampler) finalize(id string, timedOut bool, reasonPrefix string) (Decision, bool) {
	st, ok := s.traces[id]
	if !ok {
		return DecisionPending, false
	}
	view := TraceView{
		TraceID:  id,
		Spans:    st.spans,
		HasError: st.hasError,
		Duration: st.maxEnd.Sub(st.minStart),
		Complete: st.complete && !timedOut,
		TimedOut: timedOut,
	}
	d := s.policy.Evaluate(view)
	d.Reason = reasonPrefix + d.Reason
	if d.Keep && !s.budget.allow(s.clock.Now()) {
		// Budget exhausted: drop immediately rather than caching the trace
		// while waiting for tokens.
		d = drop("budget_exhausted")
	}
	s.record(id, d)
	return d, true
}

func (s *Sampler) record(id string, d Decision) {
	st := s.traces[id]
	delete(s.traces, id)
	s.spanCount -= len(st.spans)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.decided[DecisionKey(id)] = decisionRecord{
		decision: d,
		expires:  s.clock.Now().Add(s.cfg.DecisionTTL),
	}
	if d.Keep {
		s.Stats.Kept++
	} else {
		s.Stats.Dropped++
	}
	s.Stats.ByReason[d.Reason]++
}
