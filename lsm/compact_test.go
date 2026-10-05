package lsm

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func put(key string, seq uint64, val string) Record {
	return Record{Key: key, Seq: seq, Val: []byte(val)}
}

func del(key string, seq uint64) Record {
	return Record{Key: key, Seq: seq, Del: true}
}

// mkSeg sorts records into canonical (key asc, seq desc) order and
// builds a segment, so fixtures can list records in any order.
func mkSeg(t *testing.T, id, level int, recs ...Record) *Segment {
	t.Helper()
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Key != recs[j].Key {
			return recs[i].Key < recs[j].Key
		}
		return recs[i].Seq > recs[j].Seq
	})
	s, err := NewSegment(id, level, recs)
	if err != nil {
		t.Fatalf("NewSegment: %v", err)
	}
	return s
}

func flat(t *testing.T, groups [][]Record) []Record {
	t.Helper()
	var out []Record
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func assertNonOverlapping(t *testing.T, groups [][]Record) {
	t.Helper()
	for i := 1; i < len(groups); i++ {
		prev, cur := groups[i-1], groups[i]
		if prev[len(prev)-1].Key >= cur[0].Key {
			t.Fatalf("output segments overlap: %q vs %q", prev[len(prev)-1].Key, cur[0].Key)
		}
	}
}

func mustGet(t *testing.T, r *Reader, snap uint64, key string) (string, bool) {
	t.Helper()
	var st Stats
	val, found, err := r.Get(snap, key, &st)
	if err != nil {
		t.Fatalf("Get(%q@%d): %v", key, snap, err)
	}
	return string(val), found
}

// An old reader holding a snapshot must still see the pre-delete value
// after a compaction that merges the tombstone in.
func TestOldReaderSpansDeletion(t *testing.T) {
	m := NewManifest()
	if _, err := m.AddSegment([]Record{put("a", 1, "v1"), put("b", 1, "x")}); err != nil {
		t.Fatal(err)
	}
	vBefore := m.Current()
	if _, err := m.AddSegment([]Record{del("a", 2)}); err != nil {
		t.Fatal(err)
	}

	plan := SelectCompaction(m.Current(), 0)
	if plan == nil {
		t.Fatal("no plan selected")
	}
	outs, err := m.RunCompaction(m.Current(), plan, []uint64{1}, ExactCoverage{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	got := flatRecords(outs)
	want := []Record{del("a", 2), put("a", 1, "v1"), put("b", 1, "x")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compaction output = %v, want %v", got, want)
	}
	if err := m.Commit(plan, outs); err != nil {
		t.Fatal(err)
	}

	oldR := NewReader(vBefore)
	if v, ok := mustGet(t, oldR, 1, "a"); !ok || v != "v1" {
		t.Fatalf("old reader lost pre-delete value: %q,%v", v, ok)
	}
	newR := NewReader(m.Current())
	if v, ok := mustGet(t, newR, 1, "a"); !ok || v != "v1" {
		t.Fatalf("snapshot 1 after compaction: %q,%v", v, ok)
	}
	if _, ok := mustGet(t, newR, 2, "a"); ok {
		t.Fatal("snapshot 2 should observe the deletion")
	}
	if v, ok := mustGet(t, newR, 1, "b"); !ok || v != "x" {
		t.Fatalf("untouched key: %q,%v", v, ok)
	}
}

// A newer put above an older tombstone: the tombstone must stay while a
// pinned snapshot still sits between them, and vanish once unpinned.
func TestNewWriteMergesWithTombstone(t *testing.T) {
	lower := mkSeg(t, 1, 1, del("k", 5))
	upper := mkSeg(t, 2, 0, put("k", 7, "new"))

	got := flat(t, mustCompact(t, []*Segment{lower, upper}, []uint64{6}, ExactCoverage{}, 100))
	want := []Record{put("k", 7, "new"), del("k", 5)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("with pinned snapshot 6: got %v want %v", got, want)
	}

	// No pinned snapshot and nothing below: tombstone is reclaimable.
	got = flat(t, mustCompact(t, []*Segment{lower, upper}, nil, ExactCoverage{}, 100))
	want = []Record{put("k", 7, "new")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unpinned: got %v want %v", got, want)
	}

	v := &Version{Levels: [][]*Segment{{upper}, {lower}}}
	r := NewReader(v)
	if _, ok := mustGet(t, r, 6, "k"); ok {
		t.Fatal("snapshot 6 must see the tombstone")
	}
	if val, ok := mustGet(t, r, 8, "k"); !ok || val != "new" {
		t.Fatalf("snapshot 8: %q,%v", val, ok)
	}
}

// Segments whose ranges overlap but share no actual keys must merge
// cleanly, and range-only coverage must conservatively keep tombstones
// that exact coverage would reclaim.
func TestRangeOverlapWithoutSharedKeys(t *testing.T) {
	s1 := mkSeg(t, 1, 0, put("a", 3, "1"), put("c", 2, "2"), del("m", 4), put("z", 1, "3"))
	s2 := mkSeg(t, 2, 0, put("b", 5, "4"), put("d", 1, "5"))
	if !s1.RangeOverlaps(s2.MinKey(), s2.MaxKey()) {
		t.Fatal("fixture ranges should overlap")
	}

	// Lower segment's range covers "m" but it holds no "m" record.
	lowerSeg := mkSeg(t, 3, 2, put("l", 1, "x"), put("n", 1, "y"))
	if !lowerSeg.ContainsRange("m") || lowerSeg.HasKey("m") {
		t.Fatal("fixture: range covers m but key absent")
	}

	// Conservative range coverage: tombstone for m must survive.
	got := flat(t, mustCompact(t, []*Segment{s1, s2}, nil, RangeCoverage{Segments: []*Segment{lowerSeg}}, 100))
	if !containsRec(got, del("m", 4)) {
		t.Fatalf("range coverage must keep tombstone, got %v", got)
	}
	// Exact coverage proves absence: tombstone dropped.
	got = flat(t, mustCompact(t, []*Segment{s1, s2}, nil, ExactCoverage{Segments: []*Segment{lowerSeg}}, 100))
	if containsRec(got, del("m", 4)) {
		t.Fatalf("exact coverage should drop tombstone, got %v", got)
	}
	wantKeys := []string{"a", "b", "c", "d", "z"}
	var gotKeys []string
	for _, r := range got {
		gotKeys = append(gotKeys, r.Key)
	}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("merged keys = %v, want %v", gotKeys, wantKeys)
	}

	// Reads must match the MVCC reference under both outcomes.
	v := &Version{Levels: [][]*Segment{{s1, s2}, {}, {lowerSeg}}}
	for _, snap := range []uint64{0, 1, 3, 5, 100} {
		for _, k := range []string{"a", "b", "c", "d", "m", "z", "q"} {
			assertMatchesReference(t, v, snap, k)
		}
	}
}

// Same key and sequence with different content is corruption; identical
// duplicates (e.g. re-compacted overlap) dedupe silently.
func TestSequenceConflict(t *testing.T) {
	good1 := mkSeg(t, 1, 0, put("a", 3, "x"))
	good2 := mkSeg(t, 2, 0, put("a", 3, "x"))
	got := flat(t, mustCompact(t, []*Segment{good1, good2}, nil, ExactCoverage{}, 100))
	if len(got) != 1 {
		t.Fatalf("identical duplicates should dedupe to 1 record, got %v", got)
	}

	bad1 := mkSeg(t, 3, 0, put("a", 3, "x"))
	bad2 := mkSeg(t, 4, 0, put("a", 3, "y"))
	if _, err := Compact([]*Segment{bad1, bad2}, nil, ExactCoverage{}, 100); !errors.Is(err, ErrCorruption) {
		t.Fatalf("value conflict: err = %v, want ErrCorruption", err)
	}

	bad3 := mkSeg(t, 5, 0, put("a", 3, "x"))
	bad4 := mkSeg(t, 6, 0, del("a", 3))
	if _, err := Compact([]*Segment{bad3, bad4}, nil, ExactCoverage{}, 100); !errors.Is(err, ErrCorruption) {
		t.Fatalf("put/tombstone conflict: err = %v, want ErrCorruption", err)
	}
}

// A tombstone that is the newest record must NOT be dropped while lower
// levels may still hold an older value.
func TestTombstoneNotDroppedJustBecauseNewest(t *testing.T) {
	upper := mkSeg(t, 1, 1, del("k", 9))
	lower := mkSeg(t, 2, 2, put("k", 3, "old"))

	got := flat(t, mustCompact(t, []*Segment{upper}, nil, ExactCoverage{Segments: []*Segment{lower}}, 100))
	if !reflect.DeepEqual(got, []Record{del("k", 9)}) {
		t.Fatalf("tombstone with lower value must survive, got %v", got)
	}

	// Once the lower value is gone and no snapshot pins it, it goes.
	got = flat(t, mustCompact(t, []*Segment{upper}, nil, ExactCoverage{}, 100))
	if len(got) != 0 {
		t.Fatalf("reclaimable tombstone should vanish, got %v", got)
	}

	// A pinned snapshot at/above the tombstone seq also retains it.
	got = flat(t, mustCompact(t, []*Segment{upper}, []uint64{9}, ExactCoverage{}, 100))
	if !reflect.DeepEqual(got, []Record{del("k", 9)}) {
		t.Fatalf("snapshot-pinned tombstone must survive, got %v", got)
	}
}

// Selection and publication are separate steps: running a compaction
// changes nothing, a failed commit changes nothing, and old versions
// stay readable throughout.
func TestPublishFailureKeepsOldVersion(t *testing.T) {
	m := NewManifest()
	m.AddSegment([]Record{put("a", 1, "v1")})
	m.AddSegment([]Record{put("a", 2, "v2"), del("b", 2)})
	v0 := m.Current()

	plan := SelectCompaction(v0, 0)
	outs, err := m.RunCompaction(v0, plan, nil, ExactCoverage{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if m.Current() != v0 {
		t.Fatal("RunCompaction must not publish anything")
	}

	boom := errors.New("injected publish failure")
	m.SetCommitHook(func() error { return boom })
	if err := m.Commit(plan, outs); !errors.Is(err, boom) {
		t.Fatalf("commit err = %v, want injected failure", err)
	}
	if m.Current() != v0 {
		t.Fatal("failed commit must leave the old version in place")
	}
	// Old readers are unaffected by the failed publish.
	r := NewReader(m.Current())
	if v, ok := mustGet(t, r, 2, "a"); !ok || v != "v2" {
		t.Fatalf("after failed commit: %q,%v", v, ok)
	}

	m.SetCommitHook(nil)
	if err := m.Commit(plan, outs); err != nil {
		t.Fatalf("retry commit: %v", err)
	}
	if m.Current() == v0 {
		t.Fatal("successful commit must swap the version")
	}
	// New version reflects the merge; the old one is still intact.
	if v, ok := mustGet(t, NewReader(m.Current()), 2, "a"); !ok || v != "v2" {
		t.Fatalf("new version: %q,%v", v, ok)
	}
	if _, ok := mustGet(t, NewReader(m.Current()), 2, "b"); ok {
		t.Fatal("new version: b should be deleted")
	}
	if v, ok := mustGet(t, NewReader(v0), 1, "a"); !ok || v != "v1" {
		t.Fatalf("old version after commit: %q,%v", v, ok)
	}
}

// Output segments never share keys, even when many keys force splits.
func TestOutputSegmentsNonOverlapping(t *testing.T) {
	var recs []Record
	for i := 0; i < 50; i++ {
		recs = append(recs, put(fmt.Sprintf("k%03d", i), 1, "v"))
	}
	s := mkSeg(t, 1, 0, recs...)
	groups := mustCompact(t, []*Segment{s}, nil, ExactCoverage{}, 7)
	if len(groups) < 5 {
		t.Fatalf("expected several output segments, got %d", len(groups))
	}
	assertNonOverlapping(t, groups)
	var n int
	for _, g := range groups {
		n += len(g)
	}
	if n != 50 {
		t.Fatalf("records lost in split: %d", n)
	}
}

// mustCompact runs Compact and fails the test on error.
func mustCompact(t *testing.T, inputs []*Segment, snaps []uint64, lower Coverage, maxRecs int) [][]Record {
	t.Helper()
	groups, err := Compact(inputs, snaps, lower, maxRecs)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	assertNonOverlapping(t, groups)
	return groups
}

func flatRecords(segs []*Segment) []Record {
	var out []Record
	for _, s := range segs {
		out = append(out, s.Records...)
	}
	return out
}

func containsRec(recs []Record, want Record) bool {
	for _, r := range recs {
		if r.Key == want.Key && r.Seq == want.Seq && sameContent(r, want) {
			return true
		}
	}
	return false
}

// assertMatchesReference compares the indexed reader against the
// full-scan MVCC reference and checks the read-count bound.
func assertMatchesReference(t *testing.T, v *Version, snap uint64, key string) {
	t.Helper()
	r := NewReader(v)
	var st Stats
	val, found, err := r.Get(snap, key, &st)
	if err != nil {
		t.Fatalf("Get(%q@%d): %v", key, snap, err)
	}
	refVal, refFound, refRead, err := ReferenceGet(v.AllSegments(), snap, key)
	if err != nil {
		t.Fatalf("ReferenceGet(%q@%d): %v", key, snap, err)
	}
	if found != refFound || string(val) != string(refVal) {
		t.Fatalf("mismatch for %q@%d: got (%q,%v), reference (%q,%v)",
			key, snap, val, found, refVal, refFound)
	}
	if st.RecordsRead > refRead {
		t.Fatalf("indexed read touched %d records, reference only %d", st.RecordsRead, refRead)
	}
}
