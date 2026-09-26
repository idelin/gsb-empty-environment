// Telemetry events are synthesized locally and can be replayed from
// fixture files. No external monitoring platform is involved.

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
)

// Event is one telemetry record carrying a cumulative histogram
// snapshot for a named metric.
type Event struct {
	Name   string    `json:"name"`
	Bounds []float64 `json:"bounds"`
	Counts []int64   `json:"counts"`
}

// Histogram converts the event payload to a Histogram.
func (e Event) Histogram() Histogram {
	return Histogram{Bounds: e.Bounds, Counts: e.Counts}
}

// Synthesize deterministically generates n events from a seed. The
// events exercise the three merge paths: identical bounds, subset
// bounds (exact conversion), and incompatible bounds (limits only).
func Synthesize(seed int64, n int) []Event {
	rng := rand.New(rand.NewSource(seed))
	events := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		switch i % 3 {
		case 0: // fine-grained latency histogram
			events = append(events, Event{
				Name:   "latency_ms",
				Bounds: []float64{5, 10, 20, 50, 100},
				Counts: cumulate(rng, 5),
			})
		case 1: // same metric, coarser bounds (subset of the fine one)
			events = append(events, Event{
				Name:   "latency_ms",
				Bounds: []float64{10, 50, 100},
				Counts: cumulate(rng, 3),
			})
		default: // different metric with incompatible boundaries
			events = append(events, Event{
				Name:   "payload_bytes",
				Bounds: []float64{64, 128, 256, 512},
				Counts: cumulate(rng, 4),
			})
		}
	}
	return events
}

// cumulate builds a random non-decreasing cumulative count slice.
func cumulate(rng *rand.Rand, n int) []int64 {
	counts := make([]int64, n)
	var acc int64
	for i := range counts {
		acc += rng.Int63n(10)
		counts[i] = acc
	}
	return counts
}

// WriteEvents encodes events as JSON lines.
func WriteEvents(w io.Writer, events []Event) error {
	enc := json.NewEncoder(w)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

// ReadEvents decodes JSON-lines events from r.
func ReadEvents(r io.Reader) ([]Event, error) {
	var events []Event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		events = append(events, e)
	}
	return events, sc.Err()
}

// ReplayResult holds the per-metric outcome of a replay.
type ReplayResult struct {
	Merged map[string]Histogram
	Ranges map[string]Range // populated when exact merge is impossible
	Errors map[string]error
}

// Replay folds events per metric name. Exact merges accumulate; when a
// new event is incompatible with the accumulated histogram, the merge
// is refused and per-bound limits over the union of both bound sets
// are recorded instead.
func Replay(events []Event) ReplayResult {
	res := ReplayResult{
		Merged: make(map[string]Histogram),
		Ranges: make(map[string]Range),
		Errors: make(map[string]error),
	}
	for _, e := range events {
		h := e.Histogram()
		if err := h.Validate(); err != nil {
			res.Errors[e.Name] = err
			continue
		}
		acc, ok := res.Merged[e.Name]
		if !ok {
			res.Merged[e.Name] = h
			continue
		}
		merged, err := Merge(acc, h)
		if err != nil {
			res.Errors[e.Name] = err
			target := unionBounds(acc.Bounds, h.Bounds)
			if r, lerr := Limits(acc, h, target); lerr == nil {
				res.Ranges[e.Name] = r
			}
			continue
		}
		res.Merged[e.Name] = merged
	}
	return res
}

// unionBounds merges two sorted bound slices into one sorted slice.
func unionBounds(a, b []float64) []float64 {
	out := make([]float64, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		case a[i] > b[j]:
			out = append(out, b[j])
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

// LoadFixture reads events from a JSON-lines fixture file.
func LoadFixture(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadEvents(f)
}
