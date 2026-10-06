package dirindex

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// modelUsage is the independent per-subtree inode-set model: walk the whole
// subtree, collect the set of distinct inodes, sum their sizes. The index's
// incremental aggregates must agree with this for every directory at all
// times.
func modelUsage(d *Dir) int64 {
	seen := make(map[uint64]bool)
	var total int64
	var walk func(*Dir)
	walk = func(cur *Dir) {
		for _, f := range cur.files {
			if !seen[f.Inode] {
				seen[f.Inode] = true
				total += f.Size
			}
		}
		for _, c := range cur.children {
			walk(c)
		}
	}
	walk(d)
	return total
}

// allDirs lists every directory in the subtree rooted at d, root first.
func allDirs(d *Dir) []*Dir {
	var out []*Dir
	var walk func(*Dir)
	walk = func(cur *Dir) {
		out = append(out, cur)
		names := make([]string, 0, len(cur.children))
		for n := range cur.children {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			walk(cur.children[n])
		}
	}
	walk(d)
	return out
}

func pathOf(d *Dir) string {
	var parts []string
	for cur := d; cur.parent != nil; cur = cur.parent {
		parts = append([]string{cur.Name}, parts...)
	}
	s := ""
	for _, p := range parts {
		s += "/" + p
	}
	if s == "" {
		s = "/"
	}
	return s
}

// checkAll verifies every directory's incremental usage against the model.
func checkAll(t *testing.T, ix *Index, ctx string) {
	t.Helper()
	for _, d := range allDirs(ix.root) {
		if got, want := d.usage, modelUsage(d); got != want {
			t.Errorf("%s: usage(%s) = %d, model = %d", ctx, pathOf(d), got, want)
		}
	}
}

func mustMkdirAll(t *testing.T, ix *Index, path string) {
	t.Helper()
	if _, err := ix.MkdirAll(path); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}

func mustAdd(t *testing.T, ix *Index, dir, name string, inode uint64, size int64) {
	t.Helper()
	if err := ix.AddFile(dir, name, inode, size); err != nil {
		t.Fatalf("AddFile(%q, %q, %d, %d): %v", dir, name, inode, size, err)
	}
}

func mustRemove(t *testing.T, ix *Index, dir, name string) {
	t.Helper()
	if err := ix.RemoveFile(dir, name); err != nil {
		t.Fatalf("RemoveFile(%q, %q): %v", dir, name, err)
	}
}

func mustMove(t *testing.T, ix *Index, src, dstParent, newName string) {
	t.Helper()
	if err := ix.Move(src, dstParent, newName); err != nil {
		t.Fatalf("Move(%q, %q, %q): %v", src, dstParent, newName, err)
	}
}

func usage(t *testing.T, ix *Index, path string) int64 {
	t.Helper()
	u, err := ix.UsageAt(path)
	if err != nil {
		t.Fatalf("UsageAt(%q): %v", path, err)
	}
	return u
}

// Cross-subtree hard links: one inode linked from two sibling directories is
// counted once in each sibling subtree and still only once at their parent.
// Summing child usages would give the wrong answer at the parent.
func TestCrossSubtreeHardlink(t *testing.T) {
	ix := New()
	mustMkdirAll(t, ix, "/a")
	mustMkdirAll(t, ix, "/b")
	mustAdd(t, ix, "/a", "f1", 100, 50)
	mustAdd(t, ix, "/b", "f2", 100, 50) // same inode, second link
	mustAdd(t, ix, "/a", "g", 200, 10)

	if got := usage(t, ix, "/a"); got != 60 {
		t.Errorf("usage(/a) = %d, want 60", got)
	}
	if got := usage(t, ix, "/b"); got != 50 {
		t.Errorf("usage(/b) = %d, want 50", got)
	}
	if got := usage(t, ix, "/"); got != 60 {
		t.Errorf("usage(/) = %d, want 60 (not the 110 child sum)", got)
	}
	checkAll(t, ix, "cross-subtree hardlink")
}

// Deleting the last link: usage drops only when the final name for an inode
// disappears; intermediate unlinks change nothing.
func TestDeleteLastLink(t *testing.T) {
	ix := New()
	mustMkdirAll(t, ix, "/x/y")
	mustAdd(t, ix, "/x", "a", 7, 100)
	mustAdd(t, ix, "/x/y", "b", 7, 100) // second link to inode 7

	mustRemove(t, ix, "/x", "a")
	if got := usage(t, ix, "/"); got != 100 {
		t.Errorf("after first unlink usage(/) = %d, want 100", got)
	}
	if got := usage(t, ix, "/x"); got != 100 {
		t.Errorf("after first unlink usage(/x) = %d, want 100", got)
	}
	checkAll(t, ix, "after first unlink")

	mustRemove(t, ix, "/x/y", "b") // last link
	if got := usage(t, ix, "/"); got != 0 {
		t.Errorf("after last unlink usage(/) = %d, want 0", got)
	}
	if got := usage(t, ix, "/x/y"); got != 0 {
		t.Errorf("after last unlink usage(/x/y) = %d, want 0", got)
	}
	if len(ix.sizes) != 0 {
		t.Errorf("inode table not cleaned: %v", ix.sizes)
	}
	checkAll(t, ix, "after last unlink")
}

// Moving a directory updates every ancestor summary on both the old and the
// new side, including inodes shared by hard links across the move boundary.
func TestMoveUpdatesAncestors(t *testing.T) {
	ix := New()
	mustMkdirAll(t, ix, "/src/sub")
	mustMkdirAll(t, ix, "/dst")
	mustAdd(t, ix, "/src/sub", "f", 1, 40)
	mustAdd(t, ix, "/src", "keep", 2, 5)
	mustAdd(t, ix, "/dst", "shared", 1, 40) // same inode as /src/sub/f

	if got := usage(t, ix, "/"); got != 45 {
		t.Fatalf("pre-move usage(/) = %d, want 45", got)
	}

	mustMove(t, ix, "/src/sub", "/dst", "sub")

	// /src loses inode 1 entirely; /dst already had it so it does not grow;
	// the root total is unchanged.
	if got := usage(t, ix, "/src"); got != 5 {
		t.Errorf("post-move usage(/src) = %d, want 5", got)
	}
	if got := usage(t, ix, "/dst"); got != 40 {
		t.Errorf("post-move usage(/dst) = %d, want 40 (inode already present)", got)
	}
	if got := usage(t, ix, "/dst/sub"); got != 40 {
		t.Errorf("post-move usage(/dst/sub) = %d, want 40", got)
	}
	if got := usage(t, ix, "/"); got != 45 {
		t.Errorf("post-move usage(/) = %d, want 45", got)
	}
	checkAll(t, ix, "post-move")

	// Move it back under a rename; /src must regain the inode.
	mustMove(t, ix, "/dst/sub", "/src", "sub2")
	if got := usage(t, ix, "/src"); got != 45 {
		t.Errorf("after move-back usage(/src) = %d, want 45", got)
	}
	if got := usage(t, ix, "/dst"); got != 40 {
		t.Errorf("after move-back usage(/dst) = %d, want 40", got)
	}
	checkAll(t, ix, "after move-back")
}

func TestMoveIntoOwnSubtreeRejected(t *testing.T) {
	ix := New()
	mustMkdirAll(t, ix, "/a/b")
	if err := ix.Move("/a", "/a/b", "a"); err == nil {
		t.Fatal("move into own subtree succeeded, want error")
	}
	if err := ix.Move("/", "/a", "root"); err == nil {
		t.Fatal("move of root succeeded, want error")
	}
	checkAll(t, ix, "after rejected moves")
}

// Snapshots are frozen: later adds, removes and moves never change them, and
// every snapshot directory still agrees with the inode-set model.
func TestSnapshotIsolation(t *testing.T) {
	ix := New()
	mustMkdirAll(t, ix, "/p/q")
	mustAdd(t, ix, "/p", "f", 1, 30)
	mustAdd(t, ix, "/p/q", "g", 1, 30) // second link
	mustAdd(t, ix, "/p/q", "h", 2, 8)

	snap := ix.Snapshot()
	checkAll(t, ix, "at snapshot")

	// Record model expectations for the frozen tree.
	want := make(map[string]int64)
	for _, d := range allDirs(snap.root) {
		want[pathOf(d)] = modelUsage(d)
	}

	// Mutate the index heavily.
	mustRemove(t, ix, "/p/q", "g")
	mustAdd(t, ix, "/p", "extra", 3, 1000)
	mustMove(t, ix, "/p/q", "/", "q")
	checkAll(t, ix, "after mutations")

	for path, w := range want {
		got, err := snap.UsageAt(path)
		if err != nil {
			t.Errorf("snapshot UsageAt(%q): %v", path, err)
			continue
		}
		if got != w {
			t.Errorf("snapshot UsageAt(%q) = %d, want %d", path, got, w)
		}
	}
	if _, err := snap.UsageAt("/q"); err == nil {
		t.Error("snapshot sees moved directory /q, want not-found")
	}
}

// Randomized property test: after every operation, every directory's
// incremental usage must equal the per-subtree inode-set model, and every
// snapshot ever taken must stay frozen at its capture-time model values.
func TestRandomizedAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ix := New()

	type frozenDir struct {
		snap *Snapshot
		path string
		want int64
	}
	var frozen []frozenDir

	dirs := []string{"/"}
	files := make(map[string][]string) // dir path -> file names
	var nextInode uint64 = 1
	var nextDir, nextMove int

	pickDir := func() string { return dirs[rng.Intn(len(dirs))] }

	for step := 0; step < 2000; step++ {
		switch rng.Intn(6) {
		case 0: // mkdir
			nextDir++
			parent := pickDir()
			p := parent
			if p == "/" {
				p = ""
			}
			p += fmt.Sprintf("/d%d", nextDir)
			if _, err := ix.MkdirAll(p); err != nil {
				t.Fatalf("step %d: MkdirAll(%q): %v", step, p, err)
			}
			dirs = append(dirs, p)

		case 1: // add file, sometimes reusing an existing inode (hard link)
			dir := pickDir()
			name := fmt.Sprintf("f%d", rng.Intn(50))
			exists := false
			for _, n := range files[dir] {
				if n == name {
					exists = true
					break
				}
			}
			if !exists {
				var inode uint64
				if rng.Intn(3) == 0 && nextInode > 1 {
					inode = uint64(rng.Intn(int(nextInode-1))) + 1
				} else {
					inode = nextInode
					nextInode++
				}
				size := int64(rng.Intn(100) + 1)
				// A size conflict with a live inode is rejected; skip those.
				if err := ix.AddFile(dir, name, inode, size); err == nil {
					files[dir] = append(files[dir], name)
				}
			}

		case 2: // remove a file
			dir := pickDir()
			if len(files[dir]) > 0 {
				i := rng.Intn(len(files[dir]))
				name := files[dir][i]
				if err := ix.RemoveFile(dir, name); err != nil {
					t.Fatalf("step %d: RemoveFile(%q, %q): %v", step, dir, name, err)
				}
				files[dir] = append(files[dir][:i], files[dir][i+1:]...)
			}

		case 3: // move a directory
			if len(dirs) > 1 {
				src := dirs[1+rng.Intn(len(dirs)-1)]
				dst := pickDir()
				srcNode := ix.Lookup(src)
				dstNode := ix.Lookup(dst)
				ok := srcNode != nil && dstNode != nil && dstNode != srcNode
				for a := dstNode; ok && a != nil; a = a.parent {
					if a == srcNode {
						ok = false
					}
				}
				if ok {
					nextMove++
					newName := fmt.Sprintf("m%d", nextMove)
					if err := ix.Move(src, dst, newName); err != nil {
						t.Fatalf("step %d: Move(%q, %q, %q): %v", step, src, dst, newName, err)
					}
					// Recompute dir paths and file ownership from scratch.
					dirs = dirs[:0]
					files = make(map[string][]string)
					for _, d := range allDirs(ix.root) {
						p := pathOf(d)
						dirs = append(dirs, p)
						for n := range d.files {
							files[p] = append(files[p], n)
						}
					}
				}
			}

		case 4: // snapshot
			snap := ix.Snapshot()
			for _, d := range allDirs(snap.root) {
				frozen = append(frozen, frozenDir{snap, pathOf(d), modelUsage(d)})
			}

		case 5: // rmdir an empty dir
			if len(dirs) > 1 {
				p := dirs[1+rng.Intn(len(dirs)-1)]
				if err := ix.Rmdir(p); err == nil {
					var kept []string
					for _, d := range dirs {
						if d != p {
							kept = append(kept, d)
						}
					}
					dirs = kept
				}
			}
		}

		checkAll(t, ix, fmt.Sprintf("step %d", step))
		if t.Failed() {
			t.FailNow()
		}
	}

	for i, fz := range frozen {
		got, err := fz.snap.UsageAt(fz.path)
		if err != nil {
			t.Fatalf("frozen snapshot %d UsageAt(%q): %v", i, fz.path, err)
		}
		if got != fz.want {
			t.Fatalf("frozen snapshot %d UsageAt(%q) = %d, want %d", i, fz.path, got, fz.want)
		}
	}
}
