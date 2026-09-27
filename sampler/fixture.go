package sampler

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// Event is one synthetic span in a replay fixture. Timestamps are
// milliseconds relative to the fixture start.
type Event struct {
	AtMS     int64  `json:"at_ms"`    // arrival time, drives the replay clock
	TraceID  string `json:"trace_id"` // decision key, consistent across shards
	SpanID   string `json:"span_id"`
	ParentID string `json:"parent_id"` // empty means root span
	Name     string `json:"name"`
	StartMS  int64  `json:"start_ms"`
	EndMS    int64  `json:"end_ms"`
	Error    bool   `json:"error"`
}

func (e Event) span(base time.Time) Span {
	return Span{
		TraceID:  e.TraceID,
		SpanID:   e.SpanID,
		ParentID: e.ParentID,
		Name:     e.Name,
		Start:    base.Add(time.Duration(e.StartMS) * time.Millisecond),
		End:      base.Add(time.Duration(e.EndMS) * time.Millisecond),
		Err:      e.Error,
	}
}

// FixtureConfig mirrors Config with millisecond durations so fixtures
// are self-contained and replay deterministically.
type FixtureConfig struct {
	MaxTraces          int    `json:"max_traces"`
	MaxDecisions       int    `json:"max_decisions"`
	TraceTimeoutMS     int    `json:"trace_timeout_ms"`
	LatencyThresholdMS int    `json:"latency_threshold_ms"`
	BudgetPerWindow    int    `json:"budget_per_window"`
	BudgetWindowMS     int    `json:"budget_window_ms"`
	NumShards          int    `json:"num_shards"`
	TimeoutPolicy      string `json:"timeout_policy"` // drop | keep_all | keep_error
}

// ParseTimeoutPolicy parses a fixture timeout policy name.
func ParseTimeoutPolicy(s string) (TimeoutPolicy, error) {
	switch s {
	case "", "drop":
		return TimeoutDrop, nil
	case "keep_all":
		return TimeoutKeepAll, nil
	case "keep_error":
		return TimeoutKeepError, nil
	}
	return 0, fmt.Errorf("unknown timeout policy %q", s)
}

func (fc FixtureConfig) config(now func() time.Time) (Config, error) {
	policy, err := ParseTimeoutPolicy(fc.TimeoutPolicy)
	if err != nil {
		return Config{}, err
	}
	return Config{
		MaxTraces:        fc.MaxTraces,
		MaxDecisions:     fc.MaxDecisions,
		TraceTimeout:     time.Duration(fc.TraceTimeoutMS) * time.Millisecond,
		LatencyThreshold: time.Duration(fc.LatencyThresholdMS) * time.Millisecond,
		BudgetPerWindow:  fc.BudgetPerWindow,
		BudgetWindow:     time.Duration(fc.BudgetWindowMS) * time.Millisecond,
		NumShards:        fc.NumShards,
		TimeoutPolicy:    policy,
		Now:              now,
	}, nil
}

// Fixture is a replayable, locally synthesized telemetry stream.
type Fixture struct {
	Config FixtureConfig `json:"config"`
	Events []Event       `json:"events"`
}

// LoadFixture reads a fixture from a JSON file.
func LoadFixture(path string) (Fixture, error) {
	var f Fixture
	data, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}

// Record pairs a decision with the replay time it was made.
type Record struct {
	At       time.Time
	Decision Decision
}

// replayBase is a fixed origin so replays are fully deterministic.
var replayBase = time.Unix(1_700_000_000, 0).UTC()

// Replay feeds fixture events into a Sampler in arrival order, advancing
// an injected clock, and returns every decision in order. Incomplete
// traces still pending after the last event are flushed past their
// timeout so the run always ends with an empty cache.
func Replay(f Fixture) ([]Record, error) {
	now := replayBase
	cfg, err := f.Config.config(func() time.Time { return now })
	if err != nil {
		return nil, err
	}
	var recs []Record
	cfg.OnDecision = func(d Decision) {
		recs = append(recs, Record{At: now, Decision: d})
	}
	s := NewSampler(cfg)

	events := append([]Event(nil), f.Events...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].AtMS < events[j].AtMS })

	for _, e := range events {
		now = replayBase.Add(time.Duration(e.AtMS) * time.Millisecond)
		s.Flush(now)
		if d, decided := s.OnSpan(e.span(replayBase)); decided && d.Late {
			// Late spans are not re-decided, but the replay surfaces the
			// stored outcome they observe.
			recs = append(recs, Record{At: now, Decision: d})
		}
	}
	s.Flush(now.Add(s.cfg.TraceTimeout + time.Millisecond))
	return recs, nil
}
