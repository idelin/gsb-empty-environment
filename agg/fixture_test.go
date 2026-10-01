package agg

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSynthDeterministic(t *testing.T) {
	a := Synth(42, 50, 1000, 3)
	b := Synth(42, 50, 1000, 3)
	if len(a) != 50 {
		t.Fatalf("want 50 events, got %d", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("event %d differs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestFixtureRoundTrip(t *testing.T) {
	events := Synth(9, 40, 500, 2)
	f := Fixture{
		BucketWidth: 10,
		Ops: []Op{
			{Kind: "ingest", Events: events},
			{Kind: "ingest", Events: events[20:]}, // overlapping redelivery
			{Kind: "correct", Start: 520, End: 540, Events: []Event{ev("fix", 521, 777)}},
			{Kind: "delete", Start: 560, End: 570},
		},
	}
	path := filepath.Join(t.TempDir(), "scenario.json")
	if err := f.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	s1, audit1 := Replay(f)
	s2, audit2 := Replay(loaded)
	if len(audit1) != 2 || len(audit2) != 2 || !reflect.DeepEqual(audit1, audit2) {
		t.Fatalf("audit mismatch: %+v vs %+v", audit1, audit2)
	}
	for v := 0; v <= s1.Version(); v++ {
		r1 := s1.QueryAt(500, 600, v)
		r2 := s2.QueryAt(500, 600, v)
		if err := compareSummary(r1.Summary, r2.Summary); err != nil {
			t.Fatalf("version %d: replay mismatch: %v", v, err)
		}
	}
}

func TestReplayScenario(t *testing.T) {
	f := Fixture{
		BucketWidth: 10,
		Ops: []Op{
			{Kind: "ingest", Events: []Event{
				ev("a", 1, 5), ev("peak", 2, 100), ev("b", 3, 7), ev("c", 15, 1),
			}},
			{Kind: "delete", Start: 2, End: 3}, // retract the peak
		},
	}
	s, audit := Replay(f)
	if len(audit) != 1 || audit[0].Removed != 1 {
		t.Fatalf("audit: %+v", audit)
	}
	latest := s.Query(0, 20)
	if latest.Max.ID != "b" || latest.Max.Value != 7 {
		t.Fatalf("peak after retraction: %+v", latest.Max)
	}
	snap := s.QueryAt(0, 20, 1)
	if snap.Max.ID != "peak" || snap.Sum != 113 {
		t.Fatalf("snapshot: max=%+v sum=%v", snap.Max, snap.Sum)
	}
	if err := s.Validate(latest); err != nil {
		t.Fatal(err)
	}
}
