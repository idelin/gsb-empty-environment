package dirstats

import (
	"fmt"
	"math/rand"
	"testing"
)

func mustMkdir(t *testing.T, d *Dir, name string) *Dir {
	t.Helper()
	sub, err := d.Mkdir(name)
	if err != nil {
		t.Fatalf("Mkdir(%q): %v", name, err)
	}
	return sub
}

func mustAddFile(t *testing.T, d *Dir, name string, size int64, blocks ...Block) *File {
	t.Helper()
	f, err := d.AddFile(name, size, blocks)
	if err != nil {
		t.Fatalf("AddFile(%q): %v", name, err)
	}
	return f
}

func mustMove(t *testing.T, d *Dir, name string, target *Dir, newName string) {
	t.Helper()
	if err := d.Move(name, target, newName); err != nil {
		t.Fatalf("Move(%q -> %q): %v", name, newName, err)
	}
}

func checkStats(t *testing.T, d *Dir, wantLogical, wantPhysical int64) {
	t.Helper()
	logical, physical := d.Stats()
	if logical != wantLogical || physical != wantPhysical {
		t.Fatalf("Stats(%q) = (%d, %d), want (%d, %d)", d.Path(), logical, physical, wantLogical, wantPhysical)
	}
}

// verifyTree cross-checks every directory's maintained stats against a
// full-tree recompute.
func verifyTree(t *testing.T, root *Dir) {
	t.Helper()
	if bad := root.Verify(); bad != "" {
		logical, physical := root.Recompute()
		t.Fatalf("Verify mismatch at %q (root recompute = (%d, %d))", bad, logical, physical)
	}
}

func TestBasicStats(t *testing.T) {
	s := NewStore()
	root := s.Root()
	a := mustMkdir(t, root, "a")
	b := mustMkdir(t, root, "b")
	mustAddFile(t, a, "f1", 100, Block{"b1", 4096}, Block{"b2", 2048})
	mustAddFile(t, b, "f2", 50, Block{"b3", 1024})

	checkStats(t, a, 100, 6144)
	checkStats(t, b, 50, 1024)
	checkStats(t, root, 150, 7168)
	verifyTree(t, root)
}

func TestCrossDirSharedBlocks(t *testing.T) {
	s := NewStore()
	root := s.Root()
	a := mustMkdir(t, root, "a")
	b := mustMkdir(t, root, "b")
	shared := Block{"shared", 4096}
	mustAddFile(t, a, "f1", 100, shared, Block{"a1", 1000})
	mustAddFile(t, b, "f2", 200, shared, Block{"b1", 2000})

	// Logical counts both files; physical counts the shared block once.
	checkStats(t, a, 100, 5096)
	checkStats(t, b, 200, 6096)
	checkStats(t, root, 300, 7096)

	// Moving one file out of its directory must not release the shared
	// block at the root, but must release the private one.
	c := mustMkdir(t, root, "c")
	mustMove(t, a, "f1", c, "f1")
	checkStats(t, a, 0, 0)
	checkStats(t, c, 100, 5096)
	checkStats(t, root, 300, 7096)
	verifyTree(t, root)
}

func TestHardLinks(t *testing.T) {
	s := NewStore()
	root := s.Root()
	a := mustMkdir(t, root, "a")
	b := mustMkdir(t, root, "b")
	f := mustAddFile(t, a, "orig", 100, Block{"b1", 4096})
	if err := b.Link("alias", f); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Logical counts each entry; physical counts the block once.
	checkStats(t, root, 200, 4096)
	checkStats(t, a, 100, 4096)
	checkStats(t, b, 100, 4096)

	// Removing one link keeps the block alive in the other subtree.
	if err := a.Remove("orig"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	checkStats(t, a, 0, 0)
	checkStats(t, b, 100, 4096)
	checkStats(t, root, 100, 4096)

	// Removing the last link releases the block everywhere.
	if err := b.Remove("alias"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	checkStats(t, root, 0, 0)
	verifyTree(t, root)
}

func TestSameParentRename(t *testing.T) {
	s := NewStore()
	root := s.Root()
	a := mustMkdir(t, root, "a")
	mustAddFile(t, a, "old", 100, Block{"b1", 4096})
	before := root

	mustMove(t, a, "old", a, "new")
	checkStats(t, a, 100, 4096)
	checkStats(t, before, 100, 4096)
	if _, _, err := a.Lookup("old"); err == nil {
		t.Fatal("old name still present after rename")
	}
	if _, f, err := a.Lookup("new"); err != nil || f == nil {
		t.Fatalf("new name not found: %v", err)
	}
	verifyTree(t, root)
}

func TestLastReferenceMovedOutReleasesSpace(t *testing.T) {
	s := NewStore()
	root := s.Root()
	a := mustMkdir(t, root, "a")
	b := mustMkdir(t, root, "b")
	mustAddFile(t, a, "f", 100, Block{"only", 4096}, Block{"shared", 1024})
	mustAddFile(t, a, "g", 10, Block{"shared", 1024})

	// "only" is referenced solely by f under a; moving f to b releases it
	// from a but keeps "shared" alive via g.
	mustMove(t, a, "f", b, "f")
	checkStats(t, a, 10, 1024)
	checkStats(t, b, 100, 5120)
	checkStats(t, root, 110, 5120)

	// Moving f back restores a.
	mustMove(t, b, "f", a, "f")
	checkStats(t, a, 110, 5120)
	checkStats(t, b, 0, 0)
	verifyTree(t, root)
}

func TestMoveSubtree(t *testing.T) {
	s := NewStore()
	root := s.Root()
	x := mustMkdir(t, root, "x")
	y := mustMkdir(t, x, "y")
	mustAddFile(t, y, "f", 100, Block{"b1", 4096})
	z := mustMkdir(t, root, "z")
	mustAddFile(t, z, "g", 50, Block{"b1", 4096}, Block{"b2", 512})

	// Move subtree x/y under z; the shared block stays counted once.
	mustMove(t, x, "y", z, "y")
	checkStats(t, x, 0, 0)
	checkStats(t, z, 150, 4608)
	checkStats(t, root, 150, 4608)
	verifyTree(t, root)
}

func TestCycleRejectedAtomically(t *testing.T) {
	s := NewStore()
	root := s.Root()
	a := mustMkdir(t, root, "a")
	b := mustMkdir(t, a, "b")
	c := mustMkdir(t, b, "c")
	mustAddFile(t, c, "f", 100, Block{"b1", 4096})

	snapshot := func() (int64, int64) { return root.Stats() }
	beforeL, beforeP := snapshot()

	for _, tc := range []struct {
		name, entry string
		src         *Dir
		target      *Dir
	}{
		{"into itself", "a", root, a},
		{"into child", "a", root, b},
		{"into grandchild", "a", root, c},
		{"subtree into descendant", "b", a, c},
	} {
		if err := tc.src.Move(tc.entry, tc.target, "moved"); err == nil {
			t.Fatalf("%s: expected cycle error", tc.name)
		} else if !isErr(err, ErrCycle) {
			t.Fatalf("%s: got %v, want ErrCycle", tc.name, err)
		}
		if l, p := snapshot(); l != beforeL || p != beforeP {
			t.Fatalf("%s: stats changed after rejected move: (%d, %d) -> (%d, %d)",
				tc.name, beforeL, beforeP, l, p)
		}
	}
	// Tree structure must be untouched.
	if got := root.Children(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("root children changed: %v", got)
	}
	verifyTree(t, root)
}

func TestSetBlocks(t *testing.T) {
	s := NewStore()
	root := s.Root()
	a := mustMkdir(t, root, "a")
	b := mustMkdir(t, root, "b")
	f := mustAddFile(t, a, "f", 100, Block{"old1", 4096}, Block{"old2", 2048})
	mustAddFile(t, b, "g", 10, Block{"old2", 2048})

	if err := f.SetBlocks(250, []Block{{"new1", 8192}, {"old2", 2048}}); err != nil {
		t.Fatalf("SetBlocks: %v", err)
	}
	// old1 released; old2 still shared with g; new1 added.
	checkStats(t, a, 250, 10240)
	checkStats(t, b, 10, 2048)
	checkStats(t, root, 260, 10240)
	verifyTree(t, root)
}

func TestSetBlocksHardLinked(t *testing.T) {
	s := NewStore()
	root := s.Root()
	a := mustMkdir(t, root, "a")
	b := mustMkdir(t, root, "b")
	f := mustAddFile(t, a, "x", 100, Block{"b1", 4096})
	if err := b.Link("y", f); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// The update must propagate along the ancestors of every entry.
	if err := f.SetBlocks(300, []Block{{"b2", 1024}}); err != nil {
		t.Fatalf("SetBlocks: %v", err)
	}
	checkStats(t, a, 300, 1024)
	checkStats(t, b, 300, 1024)
	checkStats(t, root, 600, 1024)
	verifyTree(t, root)
}

func TestFailedOpsAreAtomic(t *testing.T) {
	s := NewStore()
	root := s.Root()
	a := mustMkdir(t, root, "a")
	f := mustAddFile(t, a, "f", 100, Block{"b1", 4096})
	beforeL, beforeP := root.Stats()

	assertUnchanged := func(step string) {
		t.Helper()
		if l, p := root.Stats(); l != beforeL || p != beforeP {
			t.Fatalf("%s: stats changed to (%d, %d)", step, l, p)
		}
		verifyTree(t, root)
	}

	// Block size conflict on a known block.
	if err := f.SetBlocks(100, []Block{{"b1", 999}}); !isErr(err, ErrBlockConflict) {
		t.Fatalf("SetBlocks conflict: got %v", err)
	}
	assertUnchanged("SetBlocks conflict")
	if got := f.Blocks(); len(got) != 1 || got[0].Size != 4096 {
		t.Fatalf("file blocks mutated on failure: %v", got)
	}

	// Conflicting AddFile must not register anything or add an entry.
	if _, err := a.AddFile("bad", 1, []Block{{"b1", 1}, {"fresh", 8}}); !isErr(err, ErrBlockConflict) {
		t.Fatalf("AddFile conflict: got %v", err)
	}
	assertUnchanged("AddFile conflict")
	if _, err := a.AddFile("ok", 1, []Block{{"fresh", 16}}); err != nil {
		t.Fatalf("block leaked from failed AddFile: %v", err)
	}
	if err := a.Remove("ok"); err != nil {
		t.Fatalf("Remove ok: %v", err)
	}
	assertUnchanged("after leak check")

	// Negative sizes.
	if _, err := a.AddFile("neg", -1, nil); !isErr(err, ErrNegativeSize) {
		t.Fatalf("AddFile negative: got %v", err)
	}
	if err := f.SetBlocks(-5, nil); !isErr(err, ErrNegativeSize) {
		t.Fatalf("SetBlocks negative: got %v", err)
	}
	assertUnchanged("negative sizes")

	// Name conflicts and missing entries.
	if err := a.Move("nope", a, "x"); !isErr(err, ErrNotFound) {
		t.Fatalf("Move missing: got %v", err)
	}
	mustAddFile(t, a, "g", 1, Block{"b2", 8})
	if err := a.Move("f", a, "g"); !isErr(err, ErrExists) {
		t.Fatalf("Move onto existing: got %v", err)
	}
	if err := a.Remove("nope"); !isErr(err, ErrNotFound) {
		t.Fatalf("Remove missing: got %v", err)
	}
	if _, err := a.Mkdir("f"); !isErr(err, ErrExists) {
		t.Fatalf("Mkdir existing: got %v", err)
	}
	verifyTree(t, root)
}

func isErr(err, target error) bool {
	return err != nil && (err == target || errorIs(err, target))
}

func errorIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// TestRandomizedAgainstRecompute performs a deterministic random sequence
// of operations and cross-checks the incrementally maintained statistics
// of every directory against a full-tree recompute after each step.
func TestRandomizedAgainstRecompute(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := NewStore()
	root := s.Root()

	dirs := []*Dir{root}
	var files []*File
	blockIDs := []BlockID{"b0", "b1", "b2", "b3", "b4", "b5", "b6", "b7"}

	randBlocks := func() []Block {
		n := 1 + rng.Intn(3)
		out := make([]Block, 0, n)
		used := map[BlockID]bool{}
		for len(out) < n {
			id := blockIDs[rng.Intn(len(blockIDs))]
			if used[id] {
				continue
			}
			used[id] = true
			out = append(out, Block{id, int64(1+rng.Intn(8)) * 512})
		}
		return out
	}
	randDir := func() *Dir { return dirs[rng.Intn(len(dirs))] }
	freeName := func(d *Dir) string {
		for i := 0; ; i++ {
			name := fmt.Sprintf("n%d", rng.Intn(50+i))
			if _, _, err := d.Lookup(name); err != nil {
				return name
			}
		}
	}

	for step := 0; step < 2000; step++ {
		switch rng.Intn(6) {
		case 0: // mkdir
			d := randDir()
			if sub, err := d.Mkdir(freeName(d)); err == nil {
				dirs = append(dirs, sub)
			}
		case 1: // add file
			d := randDir()
			if f, err := d.AddFile(freeName(d), int64(rng.Intn(1000)), randBlocks()); err == nil {
				files = append(files, f)
			}
		case 2: // hard link
			if len(files) == 0 {
				continue
			}
			d := randDir()
			_ = d.Link(freeName(d), files[rng.Intn(len(files))])
		case 3: // move (rename or relocate; cycles may be rejected)
			d := randDir()
			names := d.Children()
			if len(names) == 0 {
				continue
			}
			target := randDir()
			_ = d.Move(names[rng.Intn(len(names))], target, freeName(target))
		case 4: // set blocks
			if len(files) == 0 {
				continue
			}
			_ = files[rng.Intn(len(files))].SetBlocks(int64(rng.Intn(1000)), randBlocks())
		case 5: // remove
			d := randDir()
			names := d.Children()
			if len(names) == 0 {
				continue
			}
			_ = d.Remove(names[rng.Intn(len(names))])
		}
		if bad := root.Verify(); bad != "" {
			t.Fatalf("step %d: maintained stats diverge from recompute at %q", step, bad)
		}
	}
}
