package dirstat

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// verifyAll cross-checks the incrementally maintained Stats of every
// directory in the tree against a full Recompute of that subtree.
func verifyAll(t *testing.T, fs *FS) {
	t.Helper()
	var dirs []string
	var walk func(d *Dir, path string)
	walk = func(d *Dir, path string) {
		dirs = append(dirs, path)
		names := make([]string, 0, len(d.children))
		for name := range d.children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if child, ok := d.children[name].(*Dir); ok {
				walk(child, path+"/"+name)
			}
		}
	}
	walk(fs.root, "")
	for _, path := range dirs {
		if path == "" {
			path = "/"
		}
		got, err := fs.Stats(path)
		if err != nil {
			t.Fatalf("Stats(%q): %v", path, err)
		}
		want, err := fs.Recompute(path)
		if err != nil {
			t.Fatalf("Recompute(%q): %v", path, err)
		}
		if got != want {
			t.Fatalf("path %q: maintained %+v != recomputed %+v", path, got, want)
		}
	}
}

func mustStats(t *testing.T, fs *FS, path string) Stats {
	t.Helper()
	s, err := fs.Stats(path)
	if err != nil {
		t.Fatalf("Stats(%q): %v", path, err)
	}
	return s
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBasicLogicalAndPhysical(t *testing.T) {
	fs := New()
	mustOK(t, fs.RegisterBlock("b1", 100))
	mustOK(t, fs.RegisterBlock("b2", 200))
	mustOK(t, fs.RegisterBlock("b3", 300))
	mustOK(t, fs.Mkdir("/a"))
	mustOK(t, fs.Mkdir("/a/b"))
	// Two files sharing block b2.
	mustOK(t, fs.AddFile("/a/f1", 1000, []BlockID{"b1", "b2"}))
	mustOK(t, fs.AddFile("/a/b/f2", 2000, []BlockID{"b2", "b3"}))

	if got, want := mustStats(t, fs, "/a/b"), (Stats{2000, 500}); got != want {
		t.Fatalf("/a/b = %+v, want %+v", got, want)
	}
	// Logical counts both files; physical counts b2 only once.
	if got, want := mustStats(t, fs, "/"), (Stats{3000, 600}); got != want {
		t.Fatalf("/ = %+v, want %+v", got, want)
	}
	verifyAll(t, fs)
}

func TestCrossDirSharingMove(t *testing.T) {
	fs := New()
	mustOK(t, fs.RegisterBlock("shared", 64))
	mustOK(t, fs.RegisterBlock("onlyA", 128))
	mustOK(t, fs.Mkdir("/x"))
	mustOK(t, fs.Mkdir("/y"))
	// "shared" is referenced from both /x and /y.
	mustOK(t, fs.AddFile("/x/f", 10, []BlockID{"shared", "onlyA"}))
	mustOK(t, fs.AddFile("/y/g", 20, []BlockID{"shared"}))

	before := mustStats(t, fs, "/")
	if before != (Stats{30, 192}) {
		t.Fatalf("root = %+v", before)
	}
	// Moving /x/f into /y must not change root stats: the blocks are
	// still referenced exactly the same number of times overall.
	mustOK(t, fs.Move("/x/f", "/y/f"))
	if got := mustStats(t, fs, "/"); got != before {
		t.Fatalf("root changed after move: %+v -> %+v", before, got)
	}
	if got := mustStats(t, fs, "/x"); got != (Stats{}) {
		t.Fatalf("/x should be empty, got %+v", got)
	}
	// /y now holds both files; shared block still counted once.
	if got, want := mustStats(t, fs, "/y"), (Stats{30, 192}); got != want {
		t.Fatalf("/y = %+v, want %+v", got, want)
	}
	verifyAll(t, fs)
}

func TestSameParentRename(t *testing.T) {
	fs := New()
	mustOK(t, fs.RegisterBlock("b", 512))
	mustOK(t, fs.Mkdir("/d"))
	mustOK(t, fs.AddFile("/d/old", 42, []BlockID{"b"}))
	beforeRoot := mustStats(t, fs, "/")
	beforeDir := mustStats(t, fs, "/d")

	mustOK(t, fs.Move("/d/old", "/d/new"))
	if got := mustStats(t, fs, "/"); got != beforeRoot {
		t.Fatalf("root changed on rename: %+v -> %+v", beforeRoot, got)
	}
	if got := mustStats(t, fs, "/d"); got != beforeDir {
		t.Fatalf("/d changed on rename: %+v -> %+v", beforeDir, got)
	}
	// Rename a whole subtree within the same parent.
	mustOK(t, fs.Move("/d", "/e"))
	if got := mustStats(t, fs, "/"); got != beforeRoot {
		t.Fatalf("root changed on subtree rename: %+v -> %+v", beforeRoot, got)
	}
	if got := mustStats(t, fs, "/e"); got != beforeDir {
		t.Fatalf("/e = %+v, want %+v", got, beforeDir)
	}
	verifyAll(t, fs)
}

func TestLastReferenceMovesOutReleasesSpace(t *testing.T) {
	fs := New()
	mustOK(t, fs.RegisterBlock("hot", 1000))
	mustOK(t, fs.RegisterBlock("cold", 2000))
	mustOK(t, fs.Mkdir("/keep"))
	mustOK(t, fs.Mkdir("/gone"))
	mustOK(t, fs.Mkdir("/gone/sub"))
	mustOK(t, fs.AddFile("/keep/f", 1, []BlockID{"hot"}))
	// "cold" is only referenced deep inside /gone.
	mustOK(t, fs.AddFile("/gone/sub/g", 2, []BlockID{"cold"}))

	if got := mustStats(t, fs, "/"); got != (Stats{3, 3000}) {
		t.Fatalf("root = %+v", got)
	}
	// Move the subtree holding the last "cold" reference out of /gone's
	// parent chain... first move it under /keep: root unchanged.
	mustOK(t, fs.Move("/gone/sub", "/keep/sub"))
	if got := mustStats(t, fs, "/"); got != (Stats{3, 3000}) {
		t.Fatalf("root = %+v", got)
	}
	if got := mustStats(t, fs, "/gone"); got != (Stats{}) {
		t.Fatalf("/gone = %+v", got)
	}
	// Unlink the last reference: "cold" space is released at the root.
	mustOK(t, fs.Unlink("/keep/sub/g"))
	if got := mustStats(t, fs, "/"); got != (Stats{1, 1000}) {
		t.Fatalf("root after release = %+v", got)
	}
	verifyAll(t, fs)
}

func TestHardLinks(t *testing.T) {
	fs := New()
	mustOK(t, fs.RegisterBlock("b1", 100))
	mustOK(t, fs.Mkdir("/p"))
	mustOK(t, fs.Mkdir("/q"))
	mustOK(t, fs.AddFile("/p/f", 500, []BlockID{"b1"}))
	mustOK(t, fs.Link("/p/f", "/q/g"))

	// Logical counts both links; physical still counts b1 once.
	if got := mustStats(t, fs, "/"); got != (Stats{1000, 100}) {
		t.Fatalf("root = %+v", got)
	}
	if got := mustStats(t, fs, "/q"); got != (Stats{500, 100}) {
		t.Fatalf("/q = %+v", got)
	}
	// Removing one link keeps the block alive.
	mustOK(t, fs.Unlink("/p/f"))
	if got := mustStats(t, fs, "/"); got != (Stats{500, 100}) {
		t.Fatalf("root after first unlink = %+v", got)
	}
	// Removing the last link releases the block.
	mustOK(t, fs.Unlink("/q/g"))
	if got := mustStats(t, fs, "/"); got != (Stats{}) {
		t.Fatalf("root after last unlink = %+v", got)
	}
	verifyAll(t, fs)
}

func TestCycleRejected(t *testing.T) {
	fs := New()
	mustOK(t, fs.Mkdir("/a"))
	mustOK(t, fs.Mkdir("/a/b"))
	mustOK(t, fs.RegisterBlock("x", 10))
	mustOK(t, fs.AddFile("/a/b/f", 5, []BlockID{"x"}))
	before := mustStats(t, fs, "/")

	for _, dst := range []string{"/a/b/c", "/a/b/deep/deeper"} {
		if err := fs.Move("/a", dst); err != ErrCycle && err != ErrNotFound {
			t.Fatalf("Move(/a, %q) = %v, want ErrCycle or ErrNotFound", dst, err)
		}
	}
	// Moving a directory into its own subtree where the parent exists.
	if err := fs.Move("/a", "/a/b/moved"); err != ErrCycle {
		t.Fatalf("Move(/a, /a/b/moved) = %v, want ErrCycle", err)
	}
	// Moving a directory onto its own path's child name via itself.
	if err := fs.Move("/a/b", "/a/b/c"); err != ErrCycle {
		t.Fatalf("Move(/a/b, /a/b/c) = %v, want ErrCycle", err)
	}
	if got := mustStats(t, fs, "/"); got != before {
		t.Fatalf("stats changed after rejected moves: %+v -> %+v", before, got)
	}
	verifyAll(t, fs)
}

func TestFailureAtomicity(t *testing.T) {
	fs := New()
	mustOK(t, fs.RegisterBlock("ok", 64))
	mustOK(t, fs.Mkdir("/d"))
	mustOK(t, fs.AddFile("/d/f", 7, []BlockID{"ok"}))
	before := mustStats(t, fs, "/")

	checkUnchanged := func(label string) {
		t.Helper()
		if got := mustStats(t, fs, "/"); got != before {
			t.Fatalf("%s: stats changed after failed op: %+v -> %+v", label, before, got)
		}
		verifyAll(t, fs)
	}

	if err := fs.AddFile("/d/g", 1, []BlockID{"missing"}); err == nil {
		t.Fatal("AddFile with unknown block should fail")
	}
	checkUnchanged("AddFile unknown block")

	if err := fs.ReplaceBlocks("/d/f", 9, []BlockID{"ok", "missing"}); err == nil {
		t.Fatal("ReplaceBlocks with unknown block should fail")
	}
	checkUnchanged("ReplaceBlocks unknown block")

	if err := fs.Move("/d/f", "/nonexistent/f"); err == nil {
		t.Fatal("Move into missing parent should fail")
	}
	checkUnchanged("Move missing parent")

	if err := fs.Move("/d/f", "/d/f"); err != ErrExists {
		t.Fatalf("Move onto itself = %v, want ErrExists", err)
	}
	checkUnchanged("Move onto existing name")

	if err := fs.Link("/d", "/d/h"); err != ErrNotFile {
		t.Fatalf("Link of directory = %v, want ErrNotFile", err)
	}
	checkUnchanged("Link directory")

	if err := fs.Mkdir("/d/f/x"); err != ErrNotDir {
		t.Fatalf("Mkdir under file = %v, want ErrNotDir", err)
	}
	checkUnchanged("Mkdir under file")
}

func TestReplaceBlocksUpdatesAncestors(t *testing.T) {
	fs := New()
	mustOK(t, fs.RegisterBlock("old", 100))
	mustOK(t, fs.RegisterBlock("new1", 40))
	mustOK(t, fs.RegisterBlock("new2", 60))
	mustOK(t, fs.Mkdir("/m"))
	mustOK(t, fs.Mkdir("/n"))
	mustOK(t, fs.AddFile("/m/f", 1000, []BlockID{"old"}))
	mustOK(t, fs.Link("/m/f", "/n/g"))

	mustOK(t, fs.ReplaceBlocks("/n/g", 800, []BlockID{"new1", "new2"}))
	// Both link directories (and the root) see the new size and blocks.
	if got := mustStats(t, fs, "/m"); got != (Stats{800, 100}) {
		t.Fatalf("/m = %+v", got)
	}
	if got := mustStats(t, fs, "/n"); got != (Stats{800, 100}) {
		t.Fatalf("/n = %+v", got)
	}
	if got := mustStats(t, fs, "/"); got != (Stats{1600, 100}) {
		t.Fatalf("/ = %+v", got)
	}
	verifyAll(t, fs)
}

// TestRandomizedAgainstRecompute applies a long deterministic sequence of
// random mutations and verifies every directory after every single step.
func TestRandomizedAgainstRecompute(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			randomizedAgainstRecompute(t, seed)
		})
	}
}

func randomizedAgainstRecompute(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	fs := New()
	const nblocks = 8
	for i := 0; i < nblocks; i++ {
		mustOK(t, fs.RegisterBlock(BlockID(fmt.Sprintf("blk%d", i)), int64(1+i*7)))
	}

	var dirs = []string{"/"}
	var files []string

	randDir := func() string { return dirs[rng.Intn(len(dirs))] }
	randFile := func() string { return files[rng.Intn(len(files))] }
	randBlocks := func() []BlockID {
		n := rng.Intn(3)
		out := make([]BlockID, n)
		for i := range out {
			out[i] = BlockID(fmt.Sprintf("blk%d", rng.Intn(nblocks)))
		}
		return out
	}
	childPath := func(dir string) string {
		name := fmt.Sprintf("n%d", rng.Intn(6))
		if dir == "/" {
			return "/" + name
		}
		return dir + "/" + name
	}

	for step := 0; step < 2000; step++ {
		op := rng.Intn(6)
		switch op {
		case 0: // mkdir
			p := childPath(randDir())
			if err := fs.Mkdir(p); err == nil {
				dirs = append(dirs, p)
			}
		case 1: // add file
			p := childPath(randDir())
			if err := fs.AddFile(p, int64(rng.Intn(1000)), randBlocks()); err == nil {
				files = append(files, p)
			}
		case 2: // hard link
			if len(files) > 0 {
				p := childPath(randDir())
				if err := fs.Link(randFile(), p); err == nil {
					files = append(files, p)
				}
			}
		case 3: // unlink
			if len(files) > 0 && rng.Intn(2) == 0 {
				i := rng.Intn(len(files))
				if err := fs.Unlink(files[i]); err == nil {
					files = append(files[:i], files[i+1:]...)
				}
			} else if len(dirs) > 1 {
				i := 1 + rng.Intn(len(dirs)-1)
				if err := fs.Unlink(dirs[i]); err == nil {
					gone := dirs[i] + "/"
					keepDirs := dirs[:0]
					for _, d := range dirs {
						if d != dirs[i] && !hasPrefix(d, gone) {
							keepDirs = append(keepDirs, d)
						}
					}
					dirs = keepDirs
					keepFiles := files[:0]
					for _, f := range files {
						if !hasPrefix(f, gone) {
							keepFiles = append(keepFiles, f)
						}
					}
					files = keepFiles
				}
			}
		case 4: // move / rename
			if len(files) > 0 && rng.Intn(2) == 0 {
				i := rng.Intn(len(files))
				dst := childPath(randDir())
				if err := fs.Move(files[i], dst); err == nil {
					files[i] = dst
				}
			} else if len(dirs) > 1 {
				i := 1 + rng.Intn(len(dirs)-1)
				dst := childPath(randDir())
				src := dirs[i]
				if err := fs.Move(src, dst); err == nil {
					prefix := src + "/"
					for j, d := range dirs {
						if d == src {
							dirs[j] = dst
						} else if hasPrefix(d, prefix) {
							dirs[j] = dst + d[len(src):]
						}
					}
					for j, f := range files {
						if hasPrefix(f, prefix) {
							files[j] = dst + f[len(src):]
						}
					}
				}
			}
		case 5: // replace blocks
			if len(files) > 0 {
				_ = fs.ReplaceBlocks(randFile(), int64(rng.Intn(1000)), randBlocks())
			}
		}
		verifyAll(t, fs)
	}
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
