package lsm

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func put(key string, seq uint64, val string) Record {
	return Record{Key: key, Seq: seq, Value: []byte(val)}
}

func del(key string, seq uint64) Record {
	return Record{Key: key, Seq: seq, Tombstone: true}
}

func totalRecords(segs []*Segment) int {
	n := 0
	for _, s := range segs {
		n += len(s.Records)
	}
	return n
}

// checkReads verifies that reading `after` gives, for every key and every
// snapshot, exactly the result a full-scan MVCC reference over `before`
// produces. It also asserts the reference really scanned every record and
// reports how many records the range-aware reader touched.
func checkReads(t *testing.T, before, after []*Segment, keys []string, snaps []uint64) {
	t.Helper()
	refStats := &Stats{}
	ref := &ReferenceReader{Segments: before, Stats: refStats}
	optStats := &Stats{}
	opt := &Reader{Segments: after, Stats: optStats}

	for _, k := range keys {
		for _, snap := range snaps {
			rv, rf, rerr := ref.Get(k, snap)
			if rerr != nil {
				t.Fatalf("reference read (%q,%d): %v", k, snap, rerr)
			}
			ov, of := opt.Get(k, snap)
			if rf != of || !bytes.Equal(rv, ov) {
				t.Fatalf("read (%q,%d): reference=(%q,%v) optimized=(%q,%v)",
					k, snap, rv, rf, ov, of)
			}
		}
	}

	wantRefReads := totalRecords(before) * len(keys) * len(snaps)
	if refStats.RecordsRead != wantRefReads {
		t.Fatalf("reference read %d records, want full scan %d",
			refStats.RecordsRead, wantRefReads)
	}
	if optStats.RecordsRead > refStats.RecordsRead {
		t.Fatalf("optimized reader touched %d records, more than reference %d",
			optStats.RecordsRead, refStats.RecordsRead)
	}
	t.Logf("records read: reference=%d optimized=%d", refStats.RecordsRead, optStats.RecordsRead)
}

func findVersions(seg *Segment, key string) []Record {
	var out []Record
	for _, r := range seg.Records {
		if r.Key == key {
			out = append(out, r)
		}
	}
	return out
}

func assertNoOverlap(t *testing.T, segs []*Segment) {
	t.Helper()
	for i := 0; i < len(segs); i++ {
		for j := i + 1; j < len(segs); j++ {
			alo, ahi, _ := segs[i].Range()
			blo, bhi, _ := segs[j].Range()
			if alo <= bhi && blo <= ahi {
				t.Fatalf("output segments %d and %d overlap: [%q,%q] vs [%q,%q]",
					segs[i].ID, segs[j].ID, alo, ahi, blo, bhi)
			}
		}
	}
}

// Old readers must still see pre-deletion values after a tombstone is
// compacted in, and future readers must see the deletion.
func TestOldReaderSurvivesDeletion(t *testing.T) {
	alloc := &Allocator{}
	segL1 := NewSegment(alloc.Next(), 1, []Record{put("a", 5, "v1"), put("b", 5, "x")})
	segL0 := NewSegment(alloc.Next(), 0, []Record{del("a", 9)})
	before := []*Segment{segL1, segL0}

	c := &Compaction{
		Inputs:      []*Segment{segL0, segL1},
		Snapshots:   []uint64{6}, // pinned before the delete at seq 9
		OutputLevel: 1,
	}
	outs, err := c.Build(alloc)
	if err != nil {
		t.Fatal(err)
	}
	assertNoOverlap(t, outs)

	// Snapshot 6 sits between a@5 and a@9, so both versions must survive:
	// dropping the tombstone would resurrect "v1" for future readers.
	var aVersions []Record
	for _, o := range outs {
		aVersions = append(aVersions, findVersions(o, "a")...)
	}
	if len(aVersions) != 2 || !aVersions[0].Tombstone || aVersions[1].Tombstone {
		t.Fatalf("expected tombstone a@9 and value a@5 kept, got %+v", aVersions)
	}

	checkReads(t, before, outs,
		[]string{"a", "b", "missing"},
		[]uint64{4, 6, 9, 100})
}

// A newer write on top of a tombstone on top of an old value: the merge
// must keep the snapshot-visible old value and the newest write, while the
// unneeded tombstone in the middle is collected.
func TestNewWriteMergesWithTombstone(t *testing.T) {
	alloc := &Allocator{}
	segL2 := NewSegment(alloc.Next(), 2, []Record{put("a", 3, "old")})
	segL1 := NewSegment(alloc.Next(), 1, []Record{del("a", 7)})
	segL0 := NewSegment(alloc.Next(), 0, []Record{put("a", 10, "new")})
	before := []*Segment{segL2, segL1, segL0}

	c := &Compaction{
		Inputs:      []*Segment{segL0, segL1, segL2},
		Snapshots:   []uint64{5}, // sees a@3 "old"
		OutputLevel: 2,
	}
	outs, err := c.Build(alloc)
	if err != nil {
		t.Fatal(err)
	}

	var aVersions []Record
	for _, o := range outs {
		aVersions = append(aVersions, findVersions(o, "a")...)
	}
	if len(aVersions) != 2 || aVersions[0].Seq != 10 || aVersions[1].Seq != 3 {
		t.Fatalf("expected a@10 and a@3, got %+v", aVersions)
	}

	checkReads(t, before, outs, []string{"a"}, []uint64{5, 100})
}

// A lower-level segment whose range covers a key blocks tombstone removal
// even when it contains no record for that key: coverage proofs are
// range-based and conservative. Without lower coverage the tombstone goes.
func TestRangeOverlapWithoutActualKeys(t *testing.T) {
	alloc := &Allocator{}
	segL1 := NewSegment(alloc.Next(), 1, []Record{del("k", 4), put("n", 2, "v")})

	// Lower segment's range [a,z] covers k but holds no record for it.
	lowerWide := NewSegment(alloc.Next(), 2, []Record{put("a", 1, "lo"), put("z", 1, "hi")})
	beforeWide := []*Segment{segL1, lowerWide}

	withLower := &Compaction{
		Inputs:      []*Segment{segL1},
		Snapshots:   nil,
		Lower:       RangesOf(lowerWide),
		OutputLevel: 1,
	}
	outs, err := withLower.Build(alloc)
	if err != nil {
		t.Fatal(err)
	}
	var kVersions []Record
	for _, o := range outs {
		kVersions = append(kVersions, findVersions(o, "k")...)
	}
	if len(kVersions) != 1 || !kVersions[0].Tombstone {
		t.Fatalf("tombstone k@4 must be kept while lower range covers k, got %+v", kVersions)
	}
	checkReads(t, beforeWide, append(outs, lowerWide), []string{"k", "n", "a", "z"}, []uint64{1, 2, 4, 100})

	// Same compaction with provably no lower coverage: tombstone dropped.
	noLower := &Compaction{
		Inputs:      []*Segment{segL1},
		Snapshots:   nil,
		Lower:       nil,
		OutputLevel: 1,
	}
	outs2, err := noLower.Build(alloc)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outs2 {
		if v := findVersions(o, "k"); len(v) != 0 {
			t.Fatalf("tombstone k@4 should be dropped without lower coverage, got %+v", v)
		}
	}
	checkReads(t, []*Segment{segL1}, outs2, []string{"k", "n"}, []uint64{2, 4, 100})
}

// Same key and sequence with different content is corruption; identical
// duplicates merge silently.
func TestSequenceConflict(t *testing.T) {
	alloc := &Allocator{}
	bad1 := NewSegment(alloc.Next(), 0, []Record{put("a", 5, "v1")})
	bad2 := NewSegment(alloc.Next(), 1, []Record{put("a", 5, "v2")})
	c := &Compaction{Inputs: []*Segment{bad1, bad2}, OutputLevel: 1}
	if _, err := c.Build(alloc); err != ErrCorruption {
		t.Fatalf("expected ErrCorruption, got %v", err)
	}

	good1 := NewSegment(alloc.Next(), 0, []Record{put("a", 5, "v")})
	good2 := NewSegment(alloc.Next(), 1, []Record{put("a", 5, "v"), put("b", 2, "w")})
	c2 := &Compaction{Inputs: []*Segment{good1, good2}, OutputLevel: 1}
	outs, err := c2.Build(alloc)
	if err != nil {
		t.Fatal(err)
	}
	var aVersions []Record
	for _, o := range outs {
		aVersions = append(aVersions, findVersions(o, "a")...)
	}
	if len(aVersions) != 1 {
		t.Fatalf("identical duplicates must merge to one version, got %+v", aVersions)
	}
	checkReads(t, []*Segment{good1, good2}, outs, []string{"a", "b"}, []uint64{1, 5, 100})
}

// A failed publish leaves the manifest untouched: old segments stay live
// and readable; a retry then swaps atomically to the new set.
func TestPublishFailure(t *testing.T) {
	alloc := &Allocator{}
	m := NewManifest()
	segL1 := NewSegment(alloc.Next(), 1, []Record{put("a", 5, "v1"), put("b", 6, "x")})
	segL0 := NewSegment(alloc.Next(), 0, []Record{del("a", 9)})
	m.AddSegment(segL1)
	m.AddSegment(segL0)
	before := m.Live()

	boom := errors.New("injected publish failure")
	fails := 1
	m.CommitHook = func() error {
		if fails > 0 {
			fails--
			return boom
		}
		return nil
	}

	newCompaction := func() *Compaction {
		return &Compaction{
			Inputs:      []*Segment{segL0, segL1},
			Snapshots:   []uint64{6},
			OutputLevel: 1,
		}
	}

	if _, err := RunCompaction(m, newCompaction(), alloc); err != boom {
		t.Fatalf("expected injected failure, got %v", err)
	}
	liveAfterFailure := m.Live()
	if len(liveAfterFailure) != 2 {
		t.Fatalf("failed publish changed live set: %+v", liveAfterFailure)
	}
	// Old readers are unaffected by the failed publish.
	checkReads(t, before, liveAfterFailure, []string{"a", "b"}, []uint64{6, 100})

	outs, err := RunCompaction(m, newCompaction(), alloc)
	if err != nil {
		t.Fatal(err)
	}
	live := m.Live()
	if len(live) != len(outs) || live[0].ID != outs[0].ID {
		t.Fatalf("live set after commit = %+v, want outputs %+v", live, outs)
	}
	assertNoOverlap(t, live)
	checkReads(t, before, live, []string{"a", "b"}, []uint64{6, 100})
}

// Deterministic randomized workload: compact a subset of segments and
// verify every (key, snapshot) read against the full-scan reference.
func TestRandomizedAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	alloc := &Allocator{}
	m := NewManifest()

	var allInputs []*Segment
	var seq uint64
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for lvl := 0; lvl < 3; lvl++ {
		for s := 0; s < 3; s++ {
			var recs []Record
			for i := 0; i < 1+rng.Intn(12); i++ {
				seq++
				k := keys[rng.Intn(len(keys))]
				if rng.Intn(4) == 0 {
					recs = append(recs, del(k, seq))
				} else {
					recs = append(recs, put(k, seq, fmt.Sprintf("v%d", seq)))
				}
			}
			seg := NewSegment(alloc.Next(), lvl, recs)
			m.AddSegment(seg)
			allInputs = append(allInputs, seg)
		}
	}

	snaps := []uint64{seq / 4, seq / 2, (3 * seq) / 4}
	probeSnaps := append(append([]uint64(nil), snaps...), seq+100)

	// Compact everything into the bottom level; lower coverage empty.
	c := &Compaction{
		Inputs:               allInputs,
		Snapshots:            snaps,
		OutputLevel:          3,
		MaxRecordsPerSegment: 5,
	}
	outs, err := RunCompaction(m, c, alloc)
	if err != nil {
		t.Fatal(err)
	}
	assertNoOverlap(t, outs)
	checkReads(t, allInputs, m.Live(), keys, probeSnaps)
}
