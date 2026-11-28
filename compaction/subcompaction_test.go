package compaction

import "testing"

// bigVal inflates entry sizes so a compaction crosses the subcompaction
// threshold without hand-computing byte accounting.
var bigVal = string(make([]byte, 40))

func init() {
	b := make([]byte, 40)
	for i := range b {
		b[i] = 'v'
	}
	bigVal = string(b)
}

// TestRangeTombstoneClippedAcrossSubcompactions is the zero-resurrection
// acceptance: a range tombstone spanning several subcompaction spans must be
// clipped into every intersecting span, so old keys in later spans stay dead
// after a bottommost compaction.
func TestRangeTombstoneClippedAcrossSubcompactions(t *testing.T) {
	cfg := testConfig()
	cfg.NumLevels = 3
	cfg.LevelTargetBytes = []int64{0, 2000, 100}
	cfg.MaxSubcompactions = 3
	cfg.SubcompactionBytes = 50
	e := NewEngine(cfg)

	// L2 -> L3 maps onto the bottommost level (index 2).
	entries := []Entry{
		putEntry("a", 1, bigVal),
		putEntry("b", 2, bigVal),
		putEntry("c", 3, bigVal),
		putEntry("d", 4, bigVal),
		putEntry("e", 5, bigVal),
		putEntry("f", 6, bigVal),
	}
	// Tombstone [b, f) at seq 50 covers b..e across multiple spans.
	tomb := RangeTombstone{Start: "b", End: "f", Seq: 50}
	e.Ingest(2, entries, []RangeTombstone{tomb})

	job, err := e.PickCompaction()
	if err != nil {
		t.Fatalf("PickCompaction: %v", err)
	}
	if job.Level != 2 || !e.IsBottommost(job.OutputLevel) {
		t.Fatalf("expected bottommost L2 job, got level %d -> %d", job.Level, job.OutputLevel)
	}

	// Plan spans and assert the tombstone is clipped into more than one span.
	subs := e.PlanSubcompactions(job)
	if len(subs) < 2 {
		t.Fatalf("expected multiple subcompactions, got %d", len(subs))
	}
	intersecting := 0
	for _, sub := range subs {
		for _, ct := range sub.Tombstones {
			// Every clipped copy must stay inside its own span.
			if sub.Lo != "" && ct.Start < sub.Lo {
				t.Fatalf("clipped start %q escaped span lo %q", ct.Start, sub.Lo)
			}
			if sub.Hi != "" && ct.End > sub.Hi {
				t.Fatalf("clipped end %q escaped span hi %q", ct.End, sub.Hi)
			}
			if ct.Seq != tomb.Seq {
				t.Fatalf("clipped tombstone seq changed: %d", ct.Seq)
			}
			intersecting++
		}
	}
	if intersecting < 2 {
		t.Fatalf("tombstone reached only %d subcompaction(s); want >= 2", intersecting)
	}

	result, err := e.RunCompaction(job)
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}
	if result.Subcompactions < 2 {
		t.Fatalf("expected parallel subcompactions, got %d", result.Subcompactions)
	}

	// Zero resurrection: b..e (covered by the tombstone) must be physically
	// gone on the bottommost level; only a and f survive.
	var survivors []UserKey
	v := e.CurrentVersion()
	for _, en := range levelEntries(v, job.OutputLevel) {
		survivors = append(survivors, en.UserKey)
	}
	want := map[UserKey]bool{"a": true, "f": true}
	if len(survivors) != len(want) {
		t.Fatalf("survivors %v, want only %v", survivors, []UserKey{"a", "f"})
	}
	for _, k := range survivors {
		if !want[k] {
			t.Fatalf("key %q resurrected despite covering range tombstone", k)
		}
	}
	// Tombstones are consumed on the bottommost level.
	for _, f := range v.Files(job.OutputLevel) {
		if len(f.RangeDeletes) != 0 {
			t.Fatalf("bottommost output must not retain range tombstones, got %d", len(f.RangeDeletes))
		}
	}
}

// TestClipTombstoneUnit checks exact half-open boundary arithmetic.
func TestClipTombstoneUnit(t *testing.T) {
	tomb := RangeTombstone{Start: "b", End: "f", Seq: 7}
	cases := []struct {
		lo, hi UserKey
		wantOK bool
		wantLo UserKey
		wantHi UserKey
	}{
		{"a", "b", false, "", ""},  // touches only exclusive end
		{"a", "c", true, "b", "c"}, // left part
		{"c", "e", true, "c", "e"}, // fully inside
		{"e", "z", true, "e", "f"}, // right part, exclusive f
		{"f", "z", false, "", ""},  // at/after end
		{"a", "", true, "b", "f"},  // +infinity span
	}
	for _, tc := range cases {
		got, ok := clipTombstone(tomb, tc.lo, tc.hi)
		if ok != tc.wantOK {
			t.Fatalf("clip(%q,%q) ok=%v want %v", tc.lo, tc.hi, ok, tc.wantOK)
		}
		if ok && (got.Start != tc.wantLo || got.End != tc.wantHi) {
			t.Fatalf("clip(%q,%q)=[%q,%q) want [%q,%q)",
				tc.lo, tc.hi, got.Start, got.End, tc.wantLo, tc.wantHi)
		}
	}
}

// TestSubcompactionSpansDisjoint verifies spans tile the full job range as
// non-overlapping half-open intervals.
func TestSubcompactionSpansDisjoint(t *testing.T) {
	cfg := testConfig()
	cfg.SubcompactionBytes = 40
	cfg.MaxSubcompactions = 4
	cfg.LevelTargetBytes[1] = 60
	e := NewEngine(cfg)

	for i := 0; i < 8; i++ {
		e.Ingest(1, []Entry{
			putEntry(UserKey("k"+string(rune('a'+i))), uint64(i+1), bigVal),
		}, nil)
	}
	job, err := e.PickCompaction()
	if err != nil {
		t.Fatalf("PickCompaction: %v", err)
	}
	subs := e.PlanSubcompactions(job)
	if len(subs) > cfg.MaxSubcompactions {
		t.Fatalf("produced %d subcompactions, cap is %d", len(subs), cfg.MaxSubcompactions)
	}
	if len(subs) < 2 {
		t.Fatalf("expected a split, got %d span", len(subs))
	}
	for i := 1; i < len(subs); i++ {
		if subs[i].Lo != subs[i-1].Hi {
			t.Fatalf("span %d boundary mismatch: prev hi=%q next lo=%q",
				i, subs[i-1].Hi, subs[i].Lo)
		}
		if subs[i].Lo <= subs[i-1].Lo {
			t.Fatalf("spans not ordered at index %d", i)
		}
	}
	if subs[0].Lo != job.Range.Smallest {
		t.Fatalf("first span lo %q != job smallest %q", subs[0].Lo, job.Range.Smallest)
	}
	if last := subs[len(subs)-1]; last.Hi != "" {
		t.Fatalf("last span hi must be +infinity, got %q", last.Hi)
	}
}
