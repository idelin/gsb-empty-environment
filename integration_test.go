package lsm

import (
	"context"
	"testing"
)

func TestPrescreenSchedulerInstallPreservesSnapshotsAndLevelOrder(t *testing.T) {
	l0 := []*FileMeta{
		prescreenFileWithTombstone(1, 0, 100,
			RangeTombstone{Start: []byte("k"), End: []byte("l"), Seq: 8},
			prescreenEntry("k", 10, "new"), prescreenEntry("k", 5, "old")),
	}
	vs := NewVersionSet(NewVersionFromFiles([][]*FileMeta{l0, nil}))
	vs.AddSnapshot(7)
	s := NewCompactionScheduler(vs, CompactionOptions{L0Trigger: 1})
	task := s.Pick()
	if task == nil {
		t.Fatal("Pick returned nil")
	}
	if !task.Bottommost {
		t.Fatal("L0->L1 task should be bottommost in a two-level tree")
	}
	outputs, err := s.ExecuteSubcompactionsAndInstall(context.Background(), task, SubcompactionOptions{
		MaxSubcompactions:      2,
		ThresholdBytes:         1,
		UseVersionSetSnapshots: true,
	})
	if err != nil {
		t.Fatalf("ExecuteSubcompactionsAndInstall: %v", err)
	}
	if len(outputs) == 0 {
		t.Fatal("no output installed")
	}
	if err := vs.Current().Validate(); err != nil {
		t.Fatalf("version invalid after install: %v", err)
	}
	prescreenRequireValue(t, outputs, "k", 7, "old")
	prescreenRequireValue(t, outputs, "k", MaxSeqNum, "new")
	prescreenRequireValue(t, vs.Current().Levels[1], "k", 7, "old")
	vs.ReleaseSnapshot(7)

	// With no snapshot the same bottommost compaction may physically discard
	// the old version and the range tombstone.
	l0 = []*FileMeta{
		prescreenFileWithTombstone(2, 0, 100,
			RangeTombstone{Start: []byte("k"), End: []byte("l"), Seq: 8},
			prescreenEntry("k", 10, "new"), prescreenEntry("k", 5, "old")),
	}
	vs2 := NewVersionSet(NewVersionFromFiles([][]*FileMeta{l0, nil}))
	s2 := NewCompactionScheduler(vs2, CompactionOptions{L0Trigger: 1})
	task2 := s2.Pick()
	outputs2, err := s2.ExecuteSubcompactionsAndInstall(context.Background(), task2, SubcompactionOptions{
		MaxSubcompactions:      2,
		ThresholdBytes:         1,
		UseVersionSetSnapshots: true,
	})
	if err != nil {
		t.Fatalf("second ExecuteSubcompactionsAndInstall: %v", err)
	}
	for _, f := range outputs2 {
		if len(f.RangeTombstones) != 0 {
			t.Fatalf("bottommost snapshot-free output kept %d tombstones", len(f.RangeTombstones))
		}
		for _, e := range f.Entries {
			if string(e.Key) == "k" && e.Seq == 5 {
				t.Fatal("bottommost snapshot-free output kept the old version")
			}
		}
	}
}

func TestPrescreenConcurrentCompactionsInstallDisjointL1Ranges(t *testing.T) {
	l0 := []*FileMeta{
		prescreenFile(1, 0, 10, prescreenEntry("a", 1, "a")),
		prescreenFile(2, 0, 10, prescreenEntry("b", 1, "b")),
		prescreenFile(3, 0, 10, prescreenEntry("x", 1, "x")),
		prescreenFile(4, 0, 10, prescreenEntry("y", 1, "y")),
	}
	vs := NewVersionSet(NewVersionFromFiles([][]*FileMeta{l0, nil}))
	s := NewCompactionScheduler(vs, CompactionOptions{L0Trigger: 2})
	first := s.Pick()
	second := s.Pick()
	if first == nil || second == nil {
		t.Fatalf("picked %v and %v, want two tasks", first, second)
	}
	out1 := prescreenFile(0, 1, 10, prescreenEntry("a", 1, "a"), prescreenEntry("b", 1, "b"))
	out2 := prescreenFile(0, 1, 10, prescreenEntry("x", 1, "x"), prescreenEntry("y", 1, "y"))
	if err := s.Finish(second, []*FileMeta{out2}); err != nil {
		t.Fatalf("finish second: %v", err)
	}
	if err := s.Finish(first, []*FileMeta{out1}); err != nil {
		t.Fatalf("finish first: %v", err)
	}
	if err := vs.Current().Validate(); err != nil {
		t.Fatalf("final version invalid: %v", err)
	}
	if len(vs.Current().Levels[1]) != 2 {
		t.Fatalf("L1 file count = %d, want 2", len(vs.Current().Levels[1]))
	}
}
