package cowfs

import "testing"

// TestMiddleSnapshotDeadlistMerge directly exercises rule (2): deleting a
// middle snapshot S_k merges its deadlist into S_{k+1}, freeing entries born
// after S_{k-1}.txg and retaining older entries for S_{k-1}.
func TestMiddleSnapshotDeadlistMerge(t *testing.T) {
	e := NewEngine()

	mustWrite := func(file string, size int, tag byte) {
		t.Helper()
		if err := e.Write(file, mkBlock(size, tag)); err != nil {
			t.Fatal(err)
		}
	}
	mustSnap := func(name string) {
		t.Helper()
		if _, err := e.Snapshot(name); err != nil {
			t.Fatal(err)
		}
	}

	// S1: x=5
	mustWrite("x", 5, 1)
	mustSnap("S1")

	// S2: overwrite x -> old x(5) lands on S2's deadlist (born at S1, retained
	// by S1).
	mustWrite("x", 7, 2)
	mustSnap("S2")

	// S3: overwrite x -> x(7) lands on S3's deadlist (retained by S2).
	mustWrite("x", 9, 3)
	mustSnap("S3")

	// S4: overwrite x -> x(9) lands on S4's deadlist (retained by S3).
	mustWrite("x", 11, 4)
	mustSnap("S4")

	allocatedBefore := e.Store().AllocatedBytes()

	// Delete the middle snapshot S2 (between S1 and S3). In this linear history
	// each old block has a single retaining snapshot, so x(5) - held only by S2
	// - is reclaimed, while S3 keeps its own x(7) deadlist entry.
	infoS2 := infoByName(e.ListSnapshots(), "S2")
	freed, err := e.DeleteSnapshot("S2")
	if err != nil {
		t.Fatal(err)
	}
	if freed != infoS2.UniqueUsed {
		t.Fatalf("S2 freed %d != unique %d", freed, infoS2.UniqueUsed)
	}
	if freed != 7 {
		t.Fatalf("S2 should free its exclusive x(7) block, freed=%d", freed)
	}

	// S3's own deadlist entry x(7) remains; the freed x(5) must be gone.
	s3 := e.snapByName["S3"]
	if len(s3.dead) != 1 {
		t.Fatalf("S3 deadlist entries=%d, want 1", len(s3.dead))
	}

	// The retained block must still be physically live.
	for id := range s3.dead {
		if _, ok := e.Store().Read(id); !ok {
			t.Fatalf("merged deadlist block %d was incorrectly freed", id)
		}
	}

	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}

	// Deleting S1 reclaims the blocks exclusively retained by S1; cumulative
	// allocated bytes must track the sum of all reported unique_used values.
	infoS1 := infoByName(e.ListSnapshots(), "S1")
	freedS1, err := e.DeleteSnapshot("S1")
	if err != nil {
		t.Fatal(err)
	}
	if freedS1 != infoS1.UniqueUsed {
		t.Fatalf("S1 freed %d != unique %d", freedS1, infoS1.UniqueUsed)
	}
	if e.Store().AllocatedBytes() != allocatedBefore-freed-freedS1 {
		t.Fatal("allocated byte drift after cascaded deletions")
	}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestNewestAndOldestDeletion covers the two boundary positions explicitly.
// TestDeadlistRetentionOnMiddleDeletion builds a shared block that remains
// referenced by an older snapshot, then proves that deleting the middle
// snapshot merges its deadlist entry into the next snapshot without freeing it.
func TestDeadlistRetentionOnMiddleDeletion(t *testing.T) {
	e := NewEngine()

	if err := e.Write("keep", mkBlock(8, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Snapshot("S1"); err != nil {
		t.Fatal(err)
	}

	// S2 still references the same keep block while a second file is added and
	// killed before S3, producing deadlist entries that S1 must retain.
	if err := e.Write("temp", mkBlock(6, 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Snapshot("S2"); err != nil {
		t.Fatal(err)
	}

	if err := e.DeleteFile("temp"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Snapshot("S3"); err != nil {
		t.Fatal(err)
	}

	keepID := e.snapByName["S1"].tree["keep"][0].ID
	tempID := e.snapByName["S2"].tree["temp"][0].ID

	// S3's deadlist holds the temp block that S2 still observes.
	s3 := e.snapByName["S3"]
	if _, present := s3.dead[tempID]; !present {
		t.Fatal("expected temp block on S3's deadlist before deletion")
	}

	before := e.Store().AllocatedBytes()
	infoS2 := infoByName(e.ListSnapshots(), "S2")
	freed, err := e.DeleteSnapshot("S2")
	if err != nil {
		t.Fatal(err)
	}
	if freed != infoS2.UniqueUsed {
		t.Fatalf("freed %d != unique %d", freed, infoS2.UniqueUsed)
	}

	// The shared keep block survives because S1 still references it.
	if _, ok := e.Store().Read(keepID); !ok {
		t.Fatal("shared keep block was incorrectly freed")
	}
	// The temp block, whose last observer was S2, is reclaimed and dropped from
	// the merged deadlist.
	if _, ok := e.Store().Read(tempID); ok {
		t.Fatal("temp block should have been freed with its last observer")
	}
	if e.Store().AllocatedBytes() != before-freed {
		t.Fatal("allocated byte drift")
	}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNewestAndOldestDeletion(t *testing.T) {
	build := func(t *testing.T) *Engine {
		t.Helper()
		e := NewEngine()
		for i := 0; i < 4; i++ {
			if err := e.Write("g", mkBlock(4+i, byte(i))); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Snapshot("S" + string(rune('1'+i))); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
		return e
	}

	t.Run("newest", func(t *testing.T) {
		e := build(t)
		info := infoByName(e.ListSnapshots(), "S4")
		freed, err := e.DeleteSnapshot("S4")
		if err != nil {
			t.Fatal(err)
		}
		if freed != info.UniqueUsed {
			t.Fatalf("newest freed %d != unique %d", freed, info.UniqueUsed)
		}
		// Active head deadlist absorbs S4's entries; nothing leaks.
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("oldest", func(t *testing.T) {
		e := build(t)
		info := infoByName(e.ListSnapshots(), "S1")
		freed, err := e.DeleteSnapshot("S1")
		if err != nil {
			t.Fatal(err)
		}
		if freed != info.UniqueUsed {
			t.Fatalf("oldest freed %d != unique %d", freed, info.UniqueUsed)
		}
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
	})
}
