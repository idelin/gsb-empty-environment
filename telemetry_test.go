package histmerge

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestReplayFixture(t *testing.T) {
	f, err := os.Open("fixtures/events.jsonl")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	events, err := LoadEvents(f)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	res, err := Replay(events)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if len(res.Snapshots) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(res.Snapshots))
	}
	// us observes 0.3/0.7/0.9 -> [0 1 3 3]; eu observes 0.2/4.9 -> [0 1 1 2].
	// Same bounds, added directly: [0 2 4 5].
	if want := []int64{0, 2, 4, 5}; !reflect.DeepEqual(res.Snapshots[0].Counts, want) {
		t.Fatalf("first snapshot counts = %v, want %v", res.Snapshots[0].Counts, want)
	}
	// Fine histogram projected onto [0.1 0.5 1 5] is [1 2 2 3]
	// (the value 9 was dropped), then added: [1 4 6 8].
	if want := []int64{1, 4, 6, 8}; !reflect.DeepEqual(res.Snapshots[1].Counts, want) {
		t.Fatalf("second snapshot counts = %v, want %v", res.Snapshots[1].Counts, want)
	}
	if res.Dropped["api_latency_fine"] != 1 {
		t.Fatalf("dropped = %v, want 1 for api_latency_fine", res.Dropped)
	}
}

func TestReplayRejectsIncompatibleFixture(t *testing.T) {
	f, err := os.Open("fixtures/incompatible.jsonl")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	events, err := LoadEvents(f)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, err = Replay(events)
	if err == nil {
		t.Fatal("expected replay to reject incompatible merge")
	}
	msg := err.Error()
	for _, want := range []string{"event 4", "[0.5 1 5]", "[0.5 2 5]"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestReplayRejectsUnknownHistogram(t *testing.T) {
	_, err := Replay([]Event{{Type: "observe", Name: "ghost", Value: 1}})
	if err == nil || !strings.Contains(err.Error(), "unknown histogram") {
		t.Fatalf("expected unknown-histogram error, got %v", err)
	}
}

func TestReplayRejectsBadDefine(t *testing.T) {
	_, err := Replay([]Event{{Type: "define", Name: "x", Bounds: []float64{2, 1}}})
	if err == nil || !strings.Contains(err.Error(), "event 0") {
		t.Fatalf("expected define validation error, got %v", err)
	}
}

func TestSynthesizeEventsRoundTrip(t *testing.T) {
	events := SynthesizeEvents(7, 200)
	res, err := Replay(events)
	if err != nil {
		t.Fatalf("replay of synthesized events failed: %v", err)
	}
	if len(res.Snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(res.Snapshots))
	}
	// Determinism: same seed, same result.
	again, err := Replay(SynthesizeEvents(7, 200))
	if err != nil {
		t.Fatalf("second replay failed: %v", err)
	}
	if !reflect.DeepEqual(res.Snapshots, again.Snapshots) {
		t.Fatal("synthesized replay is not deterministic")
	}
	// The merged coarse histogram must remain valid and cumulative.
	s := res.Snapshots[0]
	h := &Histogram{Bounds: s.Bounds, Counts: s.Counts}
	if err := h.Validate(); err != nil {
		t.Fatalf("merged snapshot invalid: %v", err)
	}
}
