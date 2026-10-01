package agg

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
)

// Op is one replayable operation in a fixture.
type Op struct {
	Kind   string  `json:"kind"` // "ingest", "correct" or "delete"
	Start  int64   `json:"start,omitempty"`
	End    int64   `json:"end,omitempty"`
	Events []Event `json:"events,omitempty"`
}

// Fixture is a replayable scenario: a bucket width plus an ordered list of
// operations. Telemetry is synthesized locally (see Synth) and can be
// persisted and replayed without any monitoring platform.
type Fixture struct {
	BucketWidth int64 `json:"bucketWidth"`
	Ops         []Op  `json:"ops"`
}

// Replay executes a fixture and returns the resulting store plus the audit
// records produced by its corrections and deletions.
func Replay(f Fixture) (*Store, []CorrectionRecord) {
	s := NewStore(f.BucketWidth)
	var recs []CorrectionRecord
	for _, op := range f.Ops {
		switch op.Kind {
		case "ingest":
			s.Ingest(op.Events)
		case "correct":
			recs = append(recs, s.Correct(op.Start, op.End, op.Events))
		case "delete":
			recs = append(recs, s.Delete(op.Start, op.End))
		default:
			panic(fmt.Sprintf("agg: unknown fixture op %q", op.Kind))
		}
	}
	return s, recs
}

// Save writes the fixture as JSON.
func (f Fixture) Save(path string) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadFixture reads a JSON fixture.
func LoadFixture(path string) (Fixture, error) {
	var f Fixture
	data, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(data, &f)
}

// Synth deterministically generates n telemetry events from a seed. Values
// are multiples of 0.5 so aggregates stay exactly representable; about 1 in
// 20 events is a spike. Timestamps jitter within one step.
func Synth(seed int64, n int, startTS, step int64) []Event {
	r := rand.New(rand.NewSource(seed))
	events := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		v := float64(r.Intn(1000)) / 2
		if r.Intn(20) == 0 {
			v += 5000
		}
		ts := startTS + int64(i)*step
		if step > 1 {
			ts += int64(r.Intn(int(step)))
		}
		events = append(events, Event{
			ID:    fmt.Sprintf("e%05d", i),
			TS:    ts,
			Value: v,
		})
	}
	return events
}
