package compaction

import "testing"

// TestSnapshotPreservesOldVersions confirms that an active snapshot blocks
// physical dropping even on the bottommost level: all versions survive and
// the range tombstone stays a physical record.
func TestSnapshotPreservesOldVersions(t *testing.T) {
	cfg := testConfig()
	cfg.NumLevels = 2
	cfg.LevelTargetBytes = []int64{0, 100}
	cfg.MaxSubcompactions = 2
	cfg.SubcompactionBytes = 50
	e := NewEngine(cfg)

	entries := []Entry{
		putEntry("k", 1, bigVal),
		putEntry("k", 2, bigVal),
		putEntry("k", 3, bigVal),
		delEntry("k", 4),
		putEntry("m", 5, bigVal),
	}
	tomb := RangeTombstone{Start: "a", End: "z", Seq: 6}
	e.Ingest(1, entries, []RangeTombstone{tomb})

	snap := e.snapshots.Acquire(1) // anchor at the oldest version

	job, err := e.PickCompaction()
	if err != nil {
		t.Fatalf("PickCompaction: %v", err)
	}
	if !e.IsBottommost(job.OutputLevel) {
		t.Fatalf("expected bottommost job, got output level %d", job.OutputLevel)
	}
	result, err := e.RunCompaction(job)
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}
	if !result.SnapshotSafe || result.DroppedEntries != 0 {
		t.Fatalf("snapshot must forbid drops, safe=%v dropped=%d",
			result.SnapshotSafe, result.DroppedEntries)
	}

	v := e.CurrentVersion()
	out := levelEntries(v, 1)
	if len(out) != len(entries) {
		t.Fatalf("expected all %d versions retained, got %d", len(entries), len(out))
	}
	var tombCount int
	for _, f := range v.Files(1) {
		tombCount += len(f.RangeDeletes)
	}
	if tombCount == 0 {
		t.Fatal("range tombstone must be retained while a snapshot is active")
	}

	// Once the snapshot is released, another bottommost compaction reclaims
	// everything shadowed by the delete/tombstone.
	e.snapshots.Release(snap)
	job2, err := e.PickCompaction()
	if err != nil {
		t.Fatalf("second PickCompaction: %v", err)
	}
	result2, err := e.RunCompaction(job2)
	if err != nil {
		t.Fatalf("second RunCompaction: %v", err)
	}
	if result2.SnapshotSafe {
		t.Fatal("without snapshots, bottommost compaction must be allowed to drop")
	}
	if result2.DroppedEntries == 0 {
		t.Fatal("expected physical drops after snapshot release")
	}
}

// TestNonBottommostNeverDrops ensures levels with an older layer underneath
// never physically discard versions or tombstones even without snapshots.
func TestNonBottommostNeverDrops(t *testing.T) {
	cfg := testConfig()
	cfg.LevelTargetBytes[1] = 100
	e := NewEngine(cfg)

	entries := []Entry{
		putEntry("k", 1, bigVal),
		putEntry("k", 2, bigVal),
		delEntry("k", 3),
	}
	tomb := RangeTombstone{Start: "a", End: "z", Seq: 4}
	e.Ingest(1, entries, []RangeTombstone{tomb})

	job, err := e.PickCompaction()
	if err != nil {
		t.Fatalf("PickCompaction: %v", err)
	}
	if e.IsBottommost(job.OutputLevel) {
		t.Fatal("L1->L2 must not be bottommost in this config")
	}
	result, err := e.RunCompaction(job)
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}
	if result.DroppedEntries != 0 {
		t.Fatalf("non-bottommost compaction dropped %d entries", result.DroppedEntries)
	}
	v := e.CurrentVersion()
	if got := len(levelEntries(v, 2)); got != len(entries) {
		t.Fatalf("expected %d retained entries on L2, got %d", len(entries), got)
	}
	var tombCount int
	for _, f := range v.Files(2) {
		tombCount += len(f.RangeDeletes)
	}
	if tombCount == 0 {
		t.Fatal("tombstone must propagate to L2, not be consumed")
	}
}
