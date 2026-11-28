package lsm

import (
	"sync"
	"testing"
)

func TestPrescreenLevelScoreFormula(t *testing.T) {
	opts := CompactionOptions{
		L0Trigger:        4,
		LevelTargetBytes: []uint64{0, 1000, 10000},
	}
	if got := ComputeLevelScore(0, 8, 0, 0, opts); got != 2 {
		t.Fatalf("L0 score = %v, want 2", got)
	}
	if got := ComputeLevelScore(1, 2, 800, 300, opts); got != 0.5 {
		t.Fatalf("L1 score = %v, want 0.5", got)
	}
	if got := ComputeLevelScore(1, 2, 800, 900, opts); got != 0 {
		t.Fatalf("fully compacting L1 score = %v, want 0", got)
	}
	v := NewVersionFromFiles([][]*FileMeta{
		{prescreenFile(1, 0, 10, prescreenEntry("a", 1, "a")), prescreenFile(2, 0, 10, prescreenEntry("b", 1, "b"))},
		{prescreenFile(3, 1, 400, prescreenEntry("c", 1, "c"))},
	})
	scores := ComputeScores(v, opts, map[int]uint64{1: 100})
	if scores[0].Score != 0.5 {
		t.Fatalf("computed L0 score = %v, want 0.5", scores[0].Score)
	}
	if scores[1].Score != 0.3 {
		t.Fatalf("computed L1 score = %v, want 0.3", scores[1].Score)
	}
}

func TestPrescreenSchedulerExpandsL0AndSkipsLockedComponent(t *testing.T) {
	l0 := []*FileMeta{
		prescreenFile(1, 0, 10, prescreenEntry("a", 1, "a"), prescreenEntry("b", 1, "b")),
		prescreenFile(2, 0, 10, prescreenEntry("b", 2, "b2"), prescreenEntry("e", 1, "e")),
		prescreenFile(3, 0, 10, prescreenEntry("x", 1, "x"), prescreenEntry("y", 1, "y")),
		prescreenFile(4, 0, 10, prescreenEntry("y", 2, "y2"), prescreenEntry("z", 1, "z")),
	}
	vs := NewVersionSet(NewVersionFromFiles([][]*FileMeta{l0, nil}))
	s := NewCompactionScheduler(vs, CompactionOptions{L0Trigger: 2, MaxSubcompactions: 2})

	first := s.Pick()
	if first == nil {
		t.Fatal("first Pick returned nil")
	}
	if len(first.InputLevelFiles) != 2 || first.InputLevelFiles[0].FileID != 1 || first.InputLevelFiles[1].FileID != 2 {
		t.Fatalf("first L0 expansion = %v, want files 1,2", prescreenFileIDs(first.InputLevelFiles))
	}
	if first.OutputLevel != 1 {
		t.Fatalf("output level = %d, want 1", first.OutputLevel)
	}

	second := s.Pick()
	if second == nil {
		t.Fatal("second Pick returned nil; locked component should be skipped")
	}
	if len(second.InputLevelFiles) != 2 || second.InputLevelFiles[0].FileID != 3 || second.InputLevelFiles[1].FileID != 4 {
		t.Fatalf("second L0 expansion = %v, want files 3,4", prescreenFileIDs(second.InputLevelFiles))
	}
	if !first.Range.Overlaps(second.Range) {
		// The ranges are disjoint, so parallel L0->L1 output locks are valid.
	} else {
		t.Fatalf("unexpected overlapping output ranges: %+v %+v", first.Range, second.Range)
	}

	if third := s.Pick(); third != nil {
		t.Fatalf("third Pick = task %d, want nil", third.ID)
	}
	s.Abort(first)
	s.Abort(second)
}

func TestPrescreenSchedulerHigherLevelExpansionAndCompactingBytes(t *testing.T) {
	l1 := []*FileMeta{
		prescreenFile(11, 1, 100, prescreenEntry("a", 1, "a"), prescreenEntry("b", 1, "b")),
		prescreenFile(12, 1, 100, prescreenEntry("c", 1, "c"), prescreenEntry("d", 1, "d")),
		prescreenFile(13, 1, 100, prescreenEntry("e", 1, "e"), prescreenEntry("f", 1, "f")),
	}
	l2 := []*FileMeta{
		prescreenFile(21, 2, 100, prescreenEntry("b", 2, "b2"), prescreenEntry("g", 1, "g")),
	}
	opts := CompactionOptions{
		L0Trigger:            2,
		LevelTargetBytes:     []uint64{0, 100, 100000},
		MaxSubcompactions:    2,
		LevelSizeMultiplier:  10,
		MaxBytesForLevelBase: 100,
	}
	vs := NewVersionSet(NewVersionFromFiles([][]*FileMeta{nil, l1, l2}))
	s := NewCompactionScheduler(vs, opts)
	task := s.Pick()
	if task == nil {
		t.Fatal("Pick returned nil")
	}
	if task.Level != 1 {
		t.Fatalf("level = %d, want 1", task.Level)
	}
	if len(task.InputLevelFiles) != 3 {
		t.Fatalf("input-level files = %v, want all three adjacent files", prescreenFileIDs(task.InputLevelFiles))
	}
	if len(task.OutputLevelFiles) != 1 || task.OutputLevelFiles[0].FileID != 21 {
		t.Fatalf("output-level files = %v, want file 21", prescreenFileIDs(task.OutputLevelFiles))
	}
	if got := s.locks.CompactingBytes(1); got != 300 {
		t.Fatalf("compacting bytes = %d, want 300", got)
	}
	if next := s.Pick(); next != nil {
		t.Fatalf("Pick after fully compacting L1 = task %d, want nil", next.ID)
	}
}

func TestPrescreenSchedulerConcurrentPicksAreDisjoint(t *testing.T) {
	var l0 []*FileMeta
	for i := 0; i < 16; i++ {
		key := string(rune('a' + i))
		l0 = append(l0, prescreenFile(uint64(i+1), 0, 10, prescreenEntry(key, 1, key)))
	}
	vs := NewVersionSet(NewVersionFromFiles([][]*FileMeta{l0, nil}))
	s := NewCompactionScheduler(vs, CompactionOptions{L0Trigger: 4, MaxSubcompactions: 2})

	const workers = 8
	var wg sync.WaitGroup
	tasks := make(chan *CompactionTask, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if task := s.Pick(); task != nil {
				tasks <- task
			}
		}()
	}
	wg.Wait()
	close(tasks)

	seen := make(map[uint64]bool)
	for task := range tasks {
		for _, f := range task.Inputs {
			if seen[f.FileID] {
				t.Fatalf("file %d was locked by two concurrent tasks", f.FileID)
			}
			seen[f.FileID] = true
		}
	}
}

func TestPrescreenSchedulersSharingLocksNeverOverlap(t *testing.T) {
	l0 := []*FileMeta{
		prescreenFile(1, 0, 10, prescreenEntry("a", 1, "a"), prescreenEntry("b", 1, "b")),
		prescreenFile(2, 0, 10, prescreenEntry("b", 2, "b2"), prescreenEntry("c", 1, "c")),
		prescreenFile(3, 0, 10, prescreenEntry("m", 1, "m"), prescreenEntry("n", 1, "n")),
		prescreenFile(4, 0, 10, prescreenEntry("n", 2, "n2"), prescreenEntry("o", 1, "o")),
	}
	vs := NewVersionSet(NewVersionFromFiles([][]*FileMeta{l0, nil}))
	locks := NewLockManager()
	opts := CompactionOptions{L0Trigger: 2}
	s1 := NewCompactionSchedulerWithLocks(vs, locks, opts)
	s2 := NewCompactionSchedulerWithLocks(vs, locks, opts)
	t1 := s1.Pick()
	t2 := s2.Pick()
	if t1 == nil || t2 == nil {
		t.Fatalf("picks = %v, %v; want both schedulers to find disjoint work", t1, t2)
	}
	seen := make(map[uint64]bool)
	for _, task := range []*CompactionTask{t1, t2} {
		for _, f := range task.Inputs {
			if seen[f.FileID] {
				t.Fatalf("file %d locked twice across schedulers", f.FileID)
			}
			seen[f.FileID] = true
		}
	}
	s1.Abort(t1)
	s2.Abort(t2)
}

func TestPrescreenSchedulerFallsThroughWhenTopLayerConflicts(t *testing.T) {
	l0 := []*FileMeta{
		prescreenFile(1, 0, 10, prescreenEntry("a", 1, "a"), prescreenEntry("d", 1, "d")),
		prescreenFile(2, 0, 10, prescreenEntry("b", 1, "b"), prescreenEntry("e", 1, "e")),
		prescreenFile(3, 0, 10, prescreenEntry("c", 1, "c"), prescreenEntry("f", 1, "f")),
		prescreenFile(4, 0, 10, prescreenEntry("d", 2, "d2"), prescreenEntry("g", 1, "g")),
	}
	l1 := []*FileMeta{
		prescreenFile(11, 1, 110, prescreenEntry("m", 1, "m"), prescreenEntry("n", 1, "n")),
	}
	opts := CompactionOptions{
		L0Trigger:        2,
		LevelTargetBytes: []uint64{0, 100, 100000},
	}
	vs := NewVersionSet(NewVersionFromFiles([][]*FileMeta{l0, l1, nil}))
	s := NewCompactionScheduler(vs, opts)
	if !s.locks.LockFiles(99, []*FileMeta{l0[0]}) {
		t.Fatal("could not pre-lock L0 file")
	}
	task := s.Pick()
	if task == nil {
		t.Fatal("Pick returned nil")
	}
	if task.Level != 1 {
		t.Fatalf("picked level %d, want fallback to L1 when the L0 component is locked", task.Level)
	}
	s.locks.Unlock(99)
}

func TestPrescreenVersionInstallRejectsOverlap(t *testing.T) {
	l1 := []*FileMeta{
		prescreenFile(1, 1, 10, prescreenEntry("b", 1, "b"), prescreenEntry("c", 1, "c")),
	}
	l2 := []*FileMeta{
		prescreenFile(2, 2, 10, prescreenEntry("a", 1, "a"), prescreenEntry("d", 1, "d")),
		prescreenFile(3, 2, 10, prescreenEntry("g", 1, "g"), prescreenEntry("h", 1, "h")),
	}
	vs := NewVersionSet(NewVersionFromFiles([][]*FileMeta{nil, l1, l2}))
	outputs := []*FileMeta{
		prescreenFile(10, 2, 10, prescreenEntry("b", 1, "b")),
		prescreenFile(11, 2, 10, prescreenEntry("e", 1, "e"), prescreenEntry("f", 1, "f")),
	}
	if err := vs.InstallCompaction(append(l1, l2...), outputs, 2); err != nil {
		t.Fatalf("InstallCompaction: %v", err)
	}
	if err := vs.Current().Validate(); err != nil {
		t.Fatalf("installed version invalid: %v", err)
	}

	// A task that forgot to include the overlapping L2 file must be rejected.
	badVS := NewVersionSet(NewVersionFromFiles([][]*FileMeta{
		nil,
		{prescreenFile(20, 1, 10, prescreenEntry("b", 1, "b"), prescreenEntry("d", 1, "d"))},
		{prescreenFile(21, 2, 10, prescreenEntry("a", 1, "a"), prescreenEntry("c", 1, "c"))},
	}))
	badOutput := prescreenFile(22, 2, 10, prescreenEntry("b", 1, "b"), prescreenEntry("d", 1, "d"))
	if err := badVS.InstallCompaction([]*FileMeta{badVS.Current().Levels[1][0]}, []*FileMeta{badOutput}, 2); err == nil {
		t.Fatal("InstallCompaction accepted an overlapping output")
	}
}

func prescreenFileIDs(files []*FileMeta) []uint64 {
	ids := make([]uint64, len(files))
	for i, f := range files {
		ids[i] = f.FileID
	}
	return ids
}
