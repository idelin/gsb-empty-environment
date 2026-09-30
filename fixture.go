package telemetry

import (
	"fmt"
	"math/rand"
)

// Generate synthesizes n deterministic telemetry events locally, starting
// at startTS and spaced roughly step apart (jittered by the seed). No
// external monitoring platform is involved; fixtures are replayable.
func Generate(seed int64, n int, startTS, step int64) []Event {
	r := rand.New(rand.NewSource(seed))
	events := make([]Event, n)
	for i := range events {
		jitter := int64(0)
		if step > 1 {
			jitter = int64(r.Intn(int(step)))
		}
		events[i] = Event{
			ID:    fmt.Sprintf("g%04d", i),
			TS:    startTS + int64(i)*step + jitter,
			Value: float64((i*37+int(seed)*11)%97) + r.Float64(),
		}
	}
	return events
}

// Op is one replayable script step.
type Op struct {
	Kind   string // "ingest" | "correct" | "delete"
	Start  int64
	End    int64
	Events []Event
}

// Replay applies a fixture script to a store in order.
func Replay(s *Store, ops []Op) {
	for _, op := range ops {
		switch op.Kind {
		case "ingest":
			s.Ingest(op.Events)
		case "correct":
			s.Correct(op.Start, op.End, op.Events)
		case "delete":
			s.Delete(op.Start, op.End)
		}
	}
}

// SampleScript builds the standard demo fixture: a base stream with one
// sharp peak, an overlapping (duplicate) redelivery, a versioned interval
// correction, and a deletion that retracts the peak.
func SampleScript() []Op {
	base := Generate(7, 40, 1_000, 10)
	peak := Event{ID: "peak", TS: 1_255, Value: 999}
	overlap := append([]Event{}, base[:12]...) // duplicate redelivery
	correction := []Event{
		{ID: "c1", TS: 1_105, Value: 42},
		{ID: "c2", TS: 1_115, Value: 43},
	}
	return []Op{
		{Kind: "ingest", Events: base},
		{Kind: "ingest", Events: append(overlap, peak)},
		{Kind: "correct", Start: 1_100, End: 1_130, Events: correction},
		{Kind: "delete", Start: 1_250, End: 1_260}, // retracts the peak
	}
}
