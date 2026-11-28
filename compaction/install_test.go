package compaction

import "testing"

// TestInstalledLevelStrictlyOrdered runs a full L1->L2 compaction and
// asserts every L >= 1 level remains strictly key-ordered and that adjacent
// files never overlap.
func TestInstalledLevelStrictlyOrdered(t *testing.T) {
	cfg := testConfig()
	cfg.LevelTargetBytes[1] = 80
	e := NewEngine(cfg)

	// Scattered, overlapping-across-files keys on L1 plus an existing L2 run.
	l1Keys := []UserKey{"d", "a", "f", "b", "e", "c", "g"}
	for i, k := range l1Keys {
		e.Ingest(1, []Entry{putEntry(k, uint64(i+1), bigVal)}, nil)
	}
	e.Ingest(2, []Entry{
		putEntry("b", 50, "old"),
		putEntry("h", 51, "old"),
	}, nil)

	sched := NewScheduler(e)
	n, err := sched.RunUntilStable()
	if err != nil {
		t.Fatalf("RunUntilStable: %v", err)
	}
	if n == 0 {
		t.Fatal("expected at least one compaction")
	}

	v := e.CurrentVersion()
	for level := 1; level < len(v.Levels); level++ {
		files := v.Files(level)
		for i := 1; i < len(files); i++ {
			prev, cur := files[i-1], files[i]
			if cur.Smallest <= prev.Largest {
				t.Fatalf("L%d overlap: file %d [%q,%q] then %d [%q,%q]",
					level, prev.FileNumber, prev.Smallest, prev.Largest,
					cur.FileNumber, cur.Smallest, cur.Largest)
			}
		}
		// Every entry must be sorted by internal key inside its file.
		for _, f := range files {
			for i := 1; i < len(f.Entries); i++ {
				if compareInternal(f.Entries[i-1].InternalKey, f.Entries[i].InternalKey) > 0 {
					t.Fatalf("L%d file %d entries not internally sorted", level, f.FileNumber)
				}
			}
		}
	}
	if err := e.CheckInvariants(); err != nil {
		t.Fatalf("CheckInvariants: %v", err)
	}

	if e.ActiveJobCount() != 0 {
		t.Fatalf("all jobs must finish, %d still active", e.ActiveJobCount())
	}

	// The newest value of key b must win over the old L2 value.
	var best *Entry
	for _, en := range levelEntries(v, 2) {
		if en.UserKey == "b" {
			if best == nil || en.Seq > best.Seq {
				best = &en
			}
		}
	}
	if best == nil {
		t.Fatal("key b missing after compaction")
	}
	if best.Seq != 50 {
		t.Fatalf("key b resolved to seq %d, expected newest L2 seq 50", best.Seq)
	}
}

// TestL0ToL1CompactionInstalls checks the L0 overlap-closure path produces a
// valid L1 and removes all input L0 files.
func TestL0ToL1CompactionInstalls(t *testing.T) {
	e := NewEngine(testConfig())
	var inputs []uint64
	for i := 0; i < 4; i++ {
		f := e.Flush([]Entry{
			putEntry(UserKey("k"+string(rune('a'+i))), uint64(i+1), "v"),
			putEntry(UserKey("k"+string(rune('a'+i+1))), uint64(i+10), "v"),
		}, nil)
		inputs = append(inputs, f.FileNumber)
	}

	job, err := e.PickCompaction()
	if err != nil {
		t.Fatalf("PickCompaction: %v", err)
	}
	if job.Level != 0 || job.OutputLevel != 1 {
		t.Fatalf("expected L0->L1, got %d->%d", job.Level, job.OutputLevel)
	}
	if _, err := e.RunCompaction(job); err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	v := e.CurrentVersion()
	if len(v.Files(0)) != 0 {
		t.Fatalf("L0 should be empty, has %d files", len(v.Files(0)))
	}
	if len(v.Files(1)) == 0 {
		t.Fatal("L1 should hold the compacted output")
	}
	if err := e.CheckInvariants(); err != nil {
		t.Fatalf("CheckInvariants: %v", err)
	}
	for _, num := range inputs {
		if fileByNumber(v, num) != nil {
			t.Fatalf("input file %d still published after install", num)
		}
	}
}
