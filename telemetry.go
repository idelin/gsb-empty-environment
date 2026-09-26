package histmerge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
)

// Event is a locally synthesized telemetry event. Events are replayed by
// fixtures; nothing is sent to any monitoring platform.
type Event struct {
	Type   string    `json:"type"`             // define | observe | merge | snapshot
	Name   string    `json:"name"`             // target histogram
	Bounds []float64 `json:"bounds,omitempty"` // define
	Value  float64   `json:"value,omitempty"`  // observe
	From   string    `json:"from,omitempty"`   // merge: source histogram
}

// Snapshot is the recorded state of a histogram at a snapshot event.
type Snapshot struct {
	Name   string
	Bounds []float64
	Counts []int64
}

// ReplayResult holds everything captured while replaying events.
type ReplayResult struct {
	Snapshots []Snapshot
	Dropped   map[string]int64 // observations above the last bound, per histogram
}

// Replay processes events in order against a fresh set of histograms.
// Any invalid input (unknown histogram, decreasing cumulative counts surfaced
// by validation, incompatible merge bounds, overflow) aborts the replay
// with an error identifying the event index.
func Replay(events []Event) (*ReplayResult, error) {
	hists := make(map[string]*Histogram)
	res := &ReplayResult{Dropped: make(map[string]int64)}
	for i, ev := range events {
		switch ev.Type {
		case "define":
			h := New(ev.Bounds)
			if err := h.Validate(); err != nil {
				return nil, fmt.Errorf("event %d: define %q: %w", i, ev.Name, err)
			}
			hists[ev.Name] = h
		case "observe":
			h, ok := hists[ev.Name]
			if !ok {
				return nil, fmt.Errorf("event %d: observe on unknown histogram %q", i, ev.Name)
			}
			if !h.Observe(ev.Value) {
				res.Dropped[ev.Name]++
			}
		case "merge":
			dst, ok := hists[ev.Name]
			if !ok {
				return nil, fmt.Errorf("event %d: merge into unknown histogram %q", i, ev.Name)
			}
			src, ok := hists[ev.From]
			if !ok {
				return nil, fmt.Errorf("event %d: merge from unknown histogram %q", i, ev.From)
			}
			merged, err := Merge(dst, src)
			if err != nil {
				return nil, fmt.Errorf("event %d: merge %q += %q: %w", i, ev.Name, ev.From, err)
			}
			hists[ev.Name] = merged
		case "snapshot":
			h, ok := hists[ev.Name]
			if !ok {
				return nil, fmt.Errorf("event %d: snapshot of unknown histogram %q", i, ev.Name)
			}
			res.Snapshots = append(res.Snapshots, Snapshot{
				Name:   ev.Name,
				Bounds: append([]float64(nil), h.Bounds...),
				Counts: append([]int64(nil), h.Counts...),
			})
		default:
			return nil, fmt.Errorf("event %d: unknown event type %q", i, ev.Type)
		}
	}
	return res, nil
}

// LoadEvents reads newline-delimited JSON events (a replay fixture).
func LoadEvents(r io.Reader) ([]Event, error) {
	var events []Event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		if len(sc.Bytes()) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// SynthesizeEvents deterministically generates a local event stream for
// two histograms: one coarse, one fine (a superset of the coarse bounds),
// followed by a merge of the fine one into the coarse one and a snapshot.
func SynthesizeEvents(seed int64, observations int) []Event {
	coarse := []float64{0.1, 0.5, 1, 5}
	fine := []float64{0.05, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5}
	events := []Event{
		{Type: "define", Name: "latency_coarse", Bounds: coarse},
		{Type: "define", Name: "latency_fine", Bounds: fine},
	}
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < observations; i++ {
		v := rng.ExpFloat64()
		name := "latency_coarse"
		if i%2 == 1 {
			name = "latency_fine"
		}
		events = append(events, Event{Type: "observe", Name: name, Value: v})
	}
	events = append(events,
		Event{Type: "merge", Name: "latency_coarse", From: "latency_fine"},
		Event{Type: "snapshot", Name: "latency_coarse"},
	)
	return events
}
