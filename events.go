package cardinalguard

import (
	"encoding/json"
	"fmt"
	"os"
)

// EventType identifies a replayable telemetry event. Events are synthesized
// locally (see synth.go) or loaded from JSON fixtures; no external
// monitoring platform is involved.
type EventType string

const (
	EventSample     EventType = "sample"
	EventReap       EventType = "reap"
	EventCorrect    EventType = "correct"
	EventCheckpoint EventType = "checkpoint"
)

// Event is one replayable record. Labels/NewLabels carry the label
// combinations for sample/correct events; Expect carries the state a
// checkpoint asserts.
type Event struct {
	Seq       int64        `json:"seq"`
	Type      EventType    `json:"type"`
	Tick      int64        `json:"tick"`
	Tenant    string       `json:"tenant,omitempty"`
	Metric    string       `json:"metric,omitempty"`
	Labels    LabelSet     `json:"labels,omitempty"`
	NewLabels LabelSet     `json:"new_labels,omitempty"`
	Bytes     int64        `json:"bytes,omitempty"`
	Expect    *Expectation `json:"expect,omitempty"`
}

// Expectation is the full limiter state a checkpoint asserts. All fields
// are compared; fixtures must state them completely.
type Expectation struct {
	Received      int64 `json:"received"`
	ReceivedBytes int64 `json:"received_bytes"`
	Live          int64 `json:"live"`
	LiveBytes     int64 `json:"live_bytes"`
	LiveSeries    int   `json:"live_series"`
	Overflow      int64 `json:"overflow"`
	Late          int64 `json:"late"`
	LateBytes     int64 `json:"late_bytes"`
	Expired       int64 `json:"expired"`
	ExpiredBytes  int64 `json:"expired_bytes"`
	Folded        int64 `json:"folded"`
	Promoted      int64 `json:"promoted"`
	Corrected     int64 `json:"corrected"`
}

// LoadFixture reads a JSON event log from disk.
func LoadFixture(path string) ([]Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("parse fixture %s: %w", path, err)
	}
	return events, nil
}

// SaveFixture writes events as indented JSON, so synthesized workloads can
// be captured and replayed later.
func SaveFixture(path string, events []Event) error {
	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Replay applies events to the limiter in order. After every event it
// checks the safety invariants; at checkpoints it additionally compares the
// snapshot against the expectation. It returns one problem string per
// violation found (empty means the replay is fully consistent).
func Replay(l *Limiter, events []Event) ([]string, error) {
	var problems []string
	for _, ev := range events {
		var err error
		switch ev.Type {
		case EventSample:
			err = l.Sample(ev.Tenant, ev.Metric, ev.Labels, ev.Tick, ev.Bytes)
		case EventReap:
			l.Reap(ev.Tick)
		case EventCorrect:
			err = l.Correct(ev.Tenant, ev.Metric, ev.Labels, ev.NewLabels, ev.Tick)
		case EventCheckpoint:
			if ev.Expect != nil {
				problems = append(problems, checkExpectation(ev.Seq, l.Snapshot(), ev.Expect)...)
			}
		default:
			return problems, fmt.Errorf("event %d: unknown type %q", ev.Seq, ev.Type)
		}
		if err != nil {
			return problems, fmt.Errorf("event %d (%s): %w", ev.Seq, ev.Type, err)
		}
		for _, v := range l.CheckInvariants() {
			problems = append(problems, fmt.Sprintf("after event %d (%s): %s", ev.Seq, ev.Type, v))
		}
	}
	return problems, nil
}

func checkExpectation(seq int64, snap Snapshot, want *Expectation) []string {
	var problems []string
	check := func(name string, got, want int64) {
		if got != want {
			problems = append(problems, fmt.Sprintf("checkpoint %d: %s = %d, want %d", seq, name, got, want))
		}
	}
	check("received", snap.Received, want.Received)
	check("received_bytes", snap.ReceivedBytes, want.ReceivedBytes)
	check("live", snap.Live, want.Live)
	check("live_bytes", snap.LiveBytes, want.LiveBytes)
	check("live_series", int64(snap.LiveSeries), int64(want.LiveSeries))
	check("overflow", snap.Overflow, want.Overflow)
	check("late", snap.Late, want.Late)
	check("late_bytes", snap.LateBytes, want.LateBytes)
	check("expired", snap.Expired, want.Expired)
	check("expired_bytes", snap.ExpiredBytes, want.ExpiredBytes)
	check("folded", snap.Folded, want.Folded)
	check("promoted", snap.Promoted, want.Promoted)
	check("corrected", snap.Corrected, want.Corrected)
	return problems
}
