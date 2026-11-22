package permverifier

import "testing"

const cowPage = uint64(0x1000)

func cowBatch(writeFirst bool) []Op {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0x10000, Len: 4 * cowPage}}},
		// Parent view: writable COW over four pages.
		{ID: 2, Map: &MapOp{
			View: 1, Object: 20, Gen: 1, Off: 0, Len: 4 * cowPage,
			Perm: PermRead | PermWrite, CPUs: []uint64{1},
			Cow:     &CowSpec{PAddr: 0x10000, Len: 4 * cowPage, Page: cowPage},
			Depends: []uint64{1},
		}},
		// Executable fork sharing every page with the parent.
		{ID: 3, Fork: &ForkOp{
			View: 2, Parent: 1, Object: 21, Gen: 1, Off: 0, Len: 4 * cowPage,
			Perm: PermRead | PermExec, CPUs: []uint64{1},
			Depends: []uint64{2},
		}},
	}
	write := Op{ID: 4, Write: &WriteOp{
		View: 1, Object: 20, Gen: 1, Off: cowPage, Len: cowPage,
		Depends: []uint64{2},
	}}
	if writeFirst {
		batch = append(batch, write)
	}
	return batch
}

func TestCowSharedAliasConflicts(t *testing.T) {
	rep, err := Verify(cowBatch(false), opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictUnsafe {
		t.Fatalf("shared parent/fork must conflict, got %s %s", rep.Verdict, rep.Reason)
	}
	if rep.Conflict.Phys.Lo != 0x10000 || rep.Conflict.Phys.Len != cowPage {
		t.Fatalf("first conflict should be page 0, got %+v", rep.Conflict.Phys)
	}

	// Byte table mirror of the shared state: both views name the same frames.
	pt := NewSmallPageTable()
	for page := uint64(0); page < 4; page++ {
		frame := Interval{Lo: 0x10000 + page*cowPage, Len: cowPage}
		pt.MapView(frame, ViewRef{ID: 1, Gen: 1, CPU: 1, Perm: PermWrite})
		pt.MapView(frame, ViewRef{ID: 2, Gen: 1, CPU: 1, Perm: PermExec})
	}
	c := pt.Conflict()
	if c == nil || c.Phys.Lo != 0x10000 {
		t.Fatalf("byte table conflict mismatch: %+v", c)
	}
	if c.Phys.Len != 4*cowPage {
		t.Fatalf("four contiguous shared pages form one run, got len %#x", c.Phys.Len)
	}
}

func TestCowLocalSplitIsolatesWrittenPage(t *testing.T) {
	rep, err := Verify(cowBatch(true), opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Only page 1 is split; pages 0,2,3 remain shared and still conflict.
	if rep.Verdict != VerdictUnsafe {
		t.Fatalf("unsplit shared pages must still conflict, got %s %s", rep.Verdict, rep.Reason)
	}
	if rep.Conflict.Phys.Lo != 0x10000 || rep.Conflict.Phys.Len != cowPage {
		t.Fatalf("first conflict must remain on page 0, got %+v", rep.Conflict.Phys)
	}

	// Exact mirror after splitting page 1 onto a private frame: page 1 clean,
	// every other page still W+X.
	pt := NewSmallPageTable()
	for page := uint64(0); page < 4; page++ {
		frame := Interval{Lo: 0x10000 + page*cowPage, Len: cowPage}
		pt.MapView(frame, ViewRef{ID: 2, Gen: 1, CPU: 1, Perm: PermExec})
		if page == 1 {
			private := Interval{Lo: 0x8000_0000_0000, Len: cowPage}
			pt.MapView(private, ViewRef{ID: 1, Gen: 1, CPU: 1, Perm: PermWrite})
			continue
		}
		pt.MapView(frame, ViewRef{ID: 1, Gen: 1, CPU: 1, Perm: PermWrite})
	}
	c := pt.Conflict()
	if c == nil || c.Phys.Lo != 0x10000 {
		t.Fatalf("page 0 should be the first conflict: %+v", c)
	}

	// Remove shared aliases entirely: isolated private page is clean.
	clean := NewSmallPageTable()
	clean.MapView(Interval{Lo: 0x8000_0000_0000, Len: cowPage},
		ViewRef{ID: 1, Gen: 1, CPU: 1, Perm: PermWrite})
	clean.MapView(Interval{Lo: 0x10000 + cowPage, Len: cowPage},
		ViewRef{ID: 2, Gen: 1, CPU: 1, Perm: PermExec})
	if c := clean.Conflict(); c != nil {
		t.Fatalf("split pages must not conflict: %+v", c.Phys)
	}
}

func TestCowOnlySplitsTouchedPages(t *testing.T) {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0x20000, Len: 4 * cowPage}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 30, Gen: 1, Off: 0, Len: 4 * cowPage,
			Perm: PermWrite, CPUs: []uint64{1},
			Cow:     &CowSpec{PAddr: 0x20000, Len: 4 * cowPage, Page: cowPage},
			Depends: []uint64{1},
		}},
		{ID: 3, Write: &WriteOp{
			View: 1, Object: 30, Gen: 1, Off: 2*cowPage + 5, Len: 17,
			Depends: []uint64{2},
		}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictSafe {
		t.Fatalf("single view cannot conflict: %s", rep.Reason)
	}
	// Drive the world directly to assert exactly one private frame was made.
	w := newWorld(opts().Pool)
	for _, op := range batch {
		if !w.enabled(op) || !w.apply(op) {
			t.Fatalf("op %d did not apply", op.ID)
		}
	}
	node := w.views[1].cow
	if len(node.private) != 1 {
		t.Fatalf("exactly one page must split, got %d", len(node.private))
	}
	if _, ok := node.private[2]; !ok {
		t.Fatalf("only page index 2 should be private: %v", node.private)
	}
}
