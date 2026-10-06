package fsdiff

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scan(t *testing.T, root string) *Snapshot {
	t.Helper()
	s, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func opsOfKind(ops []Op, k OpKind) []Op {
	var out []Op
	for _, op := range ops {
		if op.Kind == k {
			out = append(out, op)
		}
	}
	return out
}

// applyAndVerify plays the diff on a virtual tree seeded from src and
// requires the result to reproduce dst exactly.
func applyAndVerify(t *testing.T, src, dst *Snapshot, ops []Op) {
	t.Helper()
	tree := NewVTree(src)
	if err := tree.Apply(ops); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := tree.Snapshot()
	if !got.Equal(dst) {
		t.Fatalf("applied tree != target\n got: %v\nwant: %v", got.Entries, dst.Entries)
	}
}

func TestMoveByStableInode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "hello")
	before := scan(t, dir)

	// Rename in place: the inode survives, only the path changes.
	if err := os.Rename(filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	after := scan(t, dir)

	ops := Diff(before, after)
	if len(ops) != 1 || ops[0].Kind != OpMove {
		t.Fatalf("want single move, got %v", ops)
	}
	if ops[0].OldPath != "a.txt" || ops[0].Path != "b.txt" {
		t.Fatalf("bad move op: %+v", ops[0])
	}
	applyAndVerify(t, before, after, ops)
}

func TestSameContentDifferentInodeIsAddRemove(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "identical content")
	before := scan(t, dir)

	// Delete and recreate: same bytes, new inode.
	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "a.txt", "identical content")
	after := scan(t, dir)

	if before.Entries["a.txt"].Inode == after.Entries["a.txt"].Inode {
		t.Skip("filesystem reused the inode; cannot exercise this case")
	}
	ops := Diff(before, after)
	if n := len(opsOfKind(ops, OpMove)); n != 0 {
		t.Fatalf("content-equal inode change must not be a move: %v", ops)
	}
	if len(opsOfKind(ops, OpRemove)) != 1 || len(opsOfKind(ops, OpAdd)) != 1 {
		t.Fatalf("want remove+add, got %v", ops)
	}
	applyAndVerify(t, before, after, ops)
}

func TestHardLinkPathSetDiff(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "orig.txt", "shared")
	if err := os.Link(filepath.Join(dir, "orig.txt"), filepath.Join(dir, "link1.txt")); err != nil {
		t.Skipf("hard links unsupported: %v", err)
	}
	if err := os.Link(filepath.Join(dir, "orig.txt"), filepath.Join(dir, "link2.txt")); err != nil {
		t.Fatal(err)
	}
	before := scan(t, dir)

	// Drop link1, move link2's name to link3: inode survives with a changed path set.
	if err := os.Remove(filepath.Join(dir, "link1.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "link2.txt"), filepath.Join(dir, "link3.txt")); err != nil {
		t.Fatal(err)
	}
	after := scan(t, dir)

	ops := Diff(before, after)
	if n := len(opsOfKind(ops, OpMove)); n != 0 {
		t.Fatalf("multi-link inode must diff as path sets, not move: %v", ops)
	}
	rm, add := opsOfKind(ops, OpLinkRemove), opsOfKind(ops, OpLinkAdd)
	if len(rm) != 2 || len(add) != 1 {
		t.Fatalf("want 2 link-remove + 1 link-add, got %v", ops)
	}
	if add[0].Path != "link3.txt" {
		t.Fatalf("bad link-add: %+v", add[0])
	}
	applyAndVerify(t, before, after, ops)
}

func TestDirMoveDoesNotHideInternalModify(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "d/f1.txt", "one")
	writeFile(t, dir, "d/sub/f2.txt", "two")
	before := scan(t, dir)

	// Move the whole directory, then modify a file inside it.
	if err := os.Rename(filepath.Join(dir, "d"), filepath.Join(dir, "e")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "e/sub/f2.txt", "two-changed")
	after := scan(t, dir)

	ops := Diff(before, after)
	moves := opsOfKind(ops, OpMove)
	if len(moves) != 2 { // dir d->e and inner dir d/sub->e/sub
		t.Fatalf("want only directory moves, got %v", ops)
	}
	for _, m := range moves {
		if !m.IsDir {
			t.Fatalf("file move leaked through dir move: %+v", m)
		}
	}
	mods := opsOfKind(ops, OpModify)
	if len(mods) != 1 || mods[0].Path != "e/sub/f2.txt" {
		t.Fatalf("internal modify hidden by dir move: %v", ops)
	}
	if n := len(opsOfKind(ops, OpAdd)) + len(opsOfKind(ops, OpRemove)); n != 0 {
		t.Fatalf("unexpected add/remove: %v", ops)
	}
	applyAndVerify(t, before, after, ops)
}

func TestCrossMoveSwap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "AAA")
	writeFile(t, dir, "b.txt", "BBB")
	before := scan(t, dir)

	// Swap the two files via a temporary name.
	if err := os.Rename(filepath.Join(dir, "a.txt"), filepath.Join(dir, "tmp.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "b.txt"), filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "tmp.txt"), filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	after := scan(t, dir)

	ops := Diff(before, after)
	moves := opsOfKind(ops, OpMove)
	if len(ops) != 2 || len(moves) != 2 {
		t.Fatalf("want exactly two moves, got %v", ops)
	}
	applyAndVerify(t, before, after, ops)

	// Content must have followed the inodes across the swap.
	if got := after.Entries["a.txt"].Hash; got != HashContent([]byte("BBB")) {
		t.Fatalf("a.txt should hold BBB after swap")
	} else if after.Entries["b.txt"].Hash != HashContent([]byte("AAA")) {
		t.Fatalf("b.txt should hold AAA after swap, got %v", got)
	}
}

func TestLinkAddRemoveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.txt", "payload")
	if err := os.Link(filepath.Join(dir, "base.txt"), filepath.Join(dir, "x1.txt")); err != nil {
		t.Skipf("hard links unsupported: %v", err)
	}
	before := scan(t, dir)

	// Add two links, remove one, and modify content through a link.
	if err := os.Link(filepath.Join(dir, "base.txt"), filepath.Join(dir, "x2.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "base.txt"), filepath.Join(dir, "x3.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "x1.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "x2.txt", "payload-v2")
	after := scan(t, dir)

	ops := Diff(before, after)
	if len(opsOfKind(ops, OpLinkAdd)) != 2 || len(opsOfKind(ops, OpLinkRemove)) != 1 {
		t.Fatalf("want 2 link-add + 1 link-remove, got %v", ops)
	}
	if len(opsOfKind(ops, OpModify)) != 1 {
		t.Fatalf("content change through a link must modify the inode: %v", ops)
	}
	applyAndVerify(t, before, after, ops)
}

func TestMoveAndModifySameFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "old.txt", "v1")
	before := scan(t, dir)

	if err := os.Rename(filepath.Join(dir, "old.txt"), filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "new.txt", "v2")
	after := scan(t, dir)

	ops := Diff(before, after)
	if len(opsOfKind(ops, OpMove)) != 1 || len(opsOfKind(ops, OpModify)) != 1 {
		t.Fatalf("want move+modify, got %v", ops)
	}
	applyAndVerify(t, before, after, ops)
}

func TestNestedDirMoveAndAddRemoveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "keep/x.txt", "x")
	writeFile(t, dir, "gone/y.txt", "y")
	writeFile(t, dir, "tree/a/b/c.txt", "c")
	before := scan(t, dir)

	if err := os.RemoveAll(filepath.Join(dir, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "tree"), filepath.Join(dir, "keep/tree")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "keep/tree/a/b/new.txt", "brand new")
	after := scan(t, dir)

	ops := Diff(before, after)
	applyAndVerify(t, before, after, ops)
}
