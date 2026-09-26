package tailsampler

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// FixtureEvent is one replayable telemetry event. Kind is "span" or "tick";
// AtMS is the event time in milliseconds relative to the replay start.
type FixtureEvent struct {
	AtMS int64        `json:"at_ms"`
	Kind string       `json:"kind"`
	Span *FixtureSpan `json:"span,omitempty"`
}

// FixtureSpan is the JSON form of a Span using millisecond offsets.
type FixtureSpan struct {
	TraceID  string `json:"trace_id"`
	SpanID   string `json:"span_id"`
	ParentID string `json:"parent_id,omitempty"`
	Name     string `json:"name,omitempty"`
	StartMS  int64  `json:"start_ms"`
	EndMS    int64  `json:"end_ms"`
	Error    bool   `json:"error,omitempty"`
}

// ReplayResult is one decision emitted during a replay.
type ReplayResult struct {
	AtMS     int64
	TraceID  string
	Decision Decision
}

// Replay feeds a JSON fixture of telemetry events into a fresh sampler driven
// by a manual clock, and returns every decision in emission order. This makes
// runs fully deterministic and reproducible from the fixture file alone.
func Replay(r io.Reader, cfg Config, start time.Time) ([]ReplayResult, error) {
	var events []FixtureEvent
	if err := json.NewDecoder(r).Decode(&events); err != nil {
		return nil, fmt.Errorf("decode fixture: %w", err)
	}
	clock := NewManualClock(start)
	cfg.Clock = clock
	s := NewSampler(cfg)

	var results []ReplayResult
	for i, ev := range events {
		clock.Set(start.Add(time.Duration(ev.AtMS) * time.Millisecond))
		switch ev.Kind {
		case "tick":
			for _, dt := range s.Tick() {
				results = append(results, ReplayResult{AtMS: ev.AtMS, TraceID: dt.TraceID, Decision: dt.Decision})
			}
		case "span":
			if ev.Span == nil {
				return nil, fmt.Errorf("event %d: span event without span", i)
			}
			fs := ev.Span
			sp := Span{
				TraceID:  fs.TraceID,
				SpanID:   fs.SpanID,
				ParentID: fs.ParentID,
				Name:     fs.Name,
				Start:    start.Add(time.Duration(fs.StartMS) * time.Millisecond),
				End:      start.Add(time.Duration(fs.EndMS) * time.Millisecond),
				Error:    fs.Error,
			}
			if d, ok := s.OnSpan(sp); ok {
				results = append(results, ReplayResult{AtMS: ev.AtMS, TraceID: sp.TraceID, Decision: d})
			}
		default:
			return nil, fmt.Errorf("event %d: unknown kind %q", i, ev.Kind)
		}
	}
	return results, nil
}
