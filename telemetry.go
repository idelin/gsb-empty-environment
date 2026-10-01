package main

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
)

// Telemetry event types. All events are synthesized locally by the engine;
// there is no external monitoring integration.
const (
	EventSeriesAdded      = "series_added"
	EventSeriesMerged     = "series_merged"
	EventSeriesSplit      = "series_split"
	EventConflictRejected = "conflict_rejected"
	EventRulesPublished   = "rules_published"
	EventRuleRetracted    = "rule_retracted"
	EventCacheStore       = "cache_store"
	EventCacheHit         = "cache_hit"
)

// Event is one local telemetry record.
type Event struct {
	Seq     uint64            `json:"seq"`
	Version uint64            `json:"version"`
	Type    string            `json:"type"`
	Detail  map[string]string `json:"detail,omitempty"`
}

// Telemetry is an in-process event log. Events can be dumped as JSONL and
// replayed later from a fixture.
type Telemetry struct {
	mu     sync.Mutex
	seq    uint64
	events []Event
}

func NewTelemetry() *Telemetry { return &Telemetry{} }

// Emit appends one event.
func (t *Telemetry) Emit(version uint64, typ string, detail map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	t.events = append(t.events, Event{Seq: t.seq, Version: version, Type: typ, Detail: detail})
}

// Events returns a copy of the recorded log.
func (t *Telemetry) Events() []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Event, len(t.events))
	copy(out, t.events)
	return out
}

// EncodeJSONL writes the log as JSON lines (fixture format).
func (t *Telemetry) EncodeJSONL(w io.Writer) error {
	enc := json.NewEncoder(w)
	for _, e := range t.Events() {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

// DecodeEvents reads a JSONL fixture back into events.
func DecodeEvents(r io.Reader) ([]Event, error) {
	var out []Event
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Projector folds events into counters. Replaying a fixture through the
// projector reconstructs the same statistics as the live run.
type Projector struct {
	Added      int
	Merged     int
	Split      int
	Conflicts  int
	Published  int
	Retracted  int
	CacheStore int
	CacheHit   int
}

// Apply folds one event.
func (p *Projector) Apply(e Event) {
	switch e.Type {
	case EventSeriesAdded:
		p.Added++
	case EventSeriesMerged:
		p.Merged++
	case EventSeriesSplit:
		p.Split++
	case EventConflictRejected:
		p.Conflicts++
	case EventRulesPublished:
		p.Published++
	case EventRuleRetracted:
		p.Retracted++
	case EventCacheStore:
		p.CacheStore++
	case EventCacheHit:
		p.CacheHit++
	}
}

// Replay folds a fixture of events into a fresh Projector.
func Replay(events []Event) *Projector {
	p := &Projector{}
	for _, e := range events {
		p.Apply(e)
	}
	return p
}
