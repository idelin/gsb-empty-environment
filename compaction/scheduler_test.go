package compaction

import "testing"

// TestLevelScores verifies L0 uses the file-count ratio while L>=1 subtracts
// bytes already under compaction before dividing by the level target.
func TestLevelScores(t *testing.T) {
	cfg := testConfig()
	cfg.L0Trigger = 2
	cfg.LevelTargetBytes = []int64{0, 100, 1000, 1000}
	e := NewEngine(cfg)

	for i := 0; i < 4; i++ {
		e.Ingest(0, []Entry{putEntry(UserKey("l0"+string(rune('a'+i))), uint64(i+1), "v")}, nil)
	}
	e.Ingest(1, []Entry{
		putEntry("a", 1, "vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv"),
		putEntry("b", 2, "vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv"),
		putEntry("c", 3, "vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv"),
	}, nil)

	byLevel := map[int]LevelScore{}
	for _, s := range e.LevelScores() {
		byLevel[s.Level] = s
	}
	if got := byLevel[0].Score; got != 2.0 {
		t.Fatalf("L0 score = %.2f, want 2.0 (4 files / trigger 2)", got)
	}
	l1Before := byLevel[1].Score
	if l1Before < 1.0 {
		t.Fatalf("L1 score before compaction = %.2f, want >= 1.0", l1Before)
	}

	// L0 has the highest score and must be picked first.
	job, err := e.PickCompaction()
	if err != nil {
		t.Fatalf("PickCompaction: %v", err)
	}
	if job.Level != 0 {
		t.Fatalf("expected highest-score L0 to be picked, got level %d", job.Level)
	}
	e.mu.Lock()
	e.releaseJobLocked(job)
	// Clear L0 so the next eligible level is L1 rather than L0 again.
	e.version.Levels[0] = nil
	e.mu.Unlock()

	// Now the L1 job is pickable and its score must drop as bytes are locked.
	l1Job, err := e.PickCompaction()
	if err != nil {
		t.Fatalf("PickCompaction for L1: %v", err)
	}
	if l1Job.Level != 1 {
		t.Fatalf("expected L1 job, got level %d", l1Job.Level)
	}
	for _, s := range e.LevelScores() {
		if s.Level == 1 {
			if s.CompactingBytes <= 0 {
				t.Fatalf("expected compacting bytes tracked, got %d", s.CompactingBytes)
			}
			if s.Score != 0 {
				t.Fatalf("L1 score must be 0 while fully compacting, got %.2f", s.Score)
			}
		}
	}
}

// TestL0OverlapClosure ensures picking one L0 file drags in every overlapping
// L0 file transitively, plus the overlapping L1 files.
func TestL0OverlapClosure(t *testing.T) {
	e := NewEngine(testConfig())
	f1 := e.Ingest(0, []Entry{putEntry("a", 1, "v1"), putEntry("b", 2, "v2")}, nil)
	f2 := e.Ingest(0, []Entry{putEntry("b", 3, "v3"), putEntry("c", 4, "v4")}, nil)
	f3 := e.Ingest(0, []Entry{putEntry("x", 5, "v5"), putEntry("y", 6, "v6")}, nil)
	f4 := e.Ingest(0, []Entry{putEntry("c", 7, "v7"), putEntry("d", 8, "v8")}, nil)
	l1 := e.Ingest(1, []Entry{putEntry("b", 9, "old")}, nil)

	job, err := e.PickCompaction()
	if err != nil {
		t.Fatalf("PickCompaction: %v", err)
	}
	if job.Level != 0 {
		t.Fatalf("expected L0 compaction, got level %d", job.Level)
	}
	for _, want := range []uint64{f1.FileNumber, f2.FileNumber, f4.FileNumber, l1.FileNumber} {
		if !containsFile(job.Inputs, want) {
			t.Fatalf("job inputs %v missing required file %d", fmtJob(job.Inputs), want)
		}
	}
	if containsFile(job.Inputs, f3.FileNumber) {
		t.Fatalf("disjoint L0 file %d must not join the closure", f3.FileNumber)
	}
}

// TestFileLockMutualExclusionAndSkip verifies that while the urgent L0/L1
// candidate is locked, the scheduler skips it and picks a conflict-free
// lower level instead of force-starting a colliding job.
func TestFileLockMutualExclusionAndSkip(t *testing.T) {
	e := NewEngine(testConfig())
	e.cfg.LevelTargetBytes[2] = 260 // L2 eligible (score>1) but below L0 urgency

	l0Files := make([]uint64, 0, 5)
	for i := 0; i < 5; i++ {
		f := e.Ingest(0, []Entry{
			putEntry(UserKey("k"+string(rune('a'+i))), uint64(i+1), "v"),
			putEntry(UserKey("k"+string(rune('a'+i+1))), uint64(i+10), "v"),
		}, nil)
		l0Files = append(l0Files, f.FileNumber)
	}

	// L2 is independently over its small target and uses disjoint z* keys.
	big := make([]Entry, 0, 8)
	for i := 0; i < 8; i++ {
		big = append(big, putEntry(UserKey("z"+string(rune('a'+i))), uint64(100+i),
			"vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv"))
	}
	e.Ingest(2, big, nil)

	first, err := e.PickCompaction()
	if err != nil {
		t.Fatalf("first pick: %v", err)
	}
	if first.Level != 0 {
		t.Fatalf("first pick should be L0, got level %d", first.Level)
	}

	second, err := e.PickCompaction()
	if err != nil {
		t.Fatalf("second pick should find a conflict-free fallback: %v", err)
	}
	if second.Level != 2 {
		t.Fatalf("second pick should skip locked L0/L1 and choose L2, got level %d", second.Level)
	}
	for _, f := range second.Inputs {
		if owner, locked := e.lockedFiles[f.FileNumber]; locked && owner == first.ID {
			t.Fatalf("second job grabbed file %d already locked by job %d", f.FileNumber, first.ID)
		}
	}
	if e.ActiveJobCount() != 2 {
		t.Fatalf("expected 2 active jobs, got %d", e.ActiveJobCount())
	}
}

// TestL0RangeConflictOnly blocks a second L0->L1 job whose range intersects
// the running one even when no physical file is shared.
func TestL0RangeConflictOnly(t *testing.T) {
	e := NewEngine(testConfig())
	e.Flush([]Entry{putEntry("a", 1, "v"), putEntry("m", 2, "v")}, nil)
	for i := 0; i < 3; i++ {
		e.Flush([]Entry{putEntry(UserKey("g"+string(rune('a'+i))), uint64(10+i), "v")}, nil)
	}

	job1, err := e.PickCompaction()
	if err != nil || job1.Level != 0 {
		t.Fatalf("expected first L0 job, got %v", err)
	}

	e.Flush([]Entry{putEntry("c", 99, "v"), putEntry("d", 100, "v")}, nil)
	if _, err := e.PickCompaction(); err == nil {
		t.Fatal("expected overlapping L0->L1 candidate to be rejected")
	}
}

func fmtJob(files []*SSTable) []uint64 {
	out := make([]uint64, 0, len(files))
	for _, f := range files {
		out = append(out, f.FileNumber)
	}
	return out
}
