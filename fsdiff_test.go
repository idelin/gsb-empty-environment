package fsdiff

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, name string) {
	t.Helper()
	if err := os.MkdirAll(name, 0o755); err != nil {
		t.Fatal(err)
	}
}

func rename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}

func hardlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Link(oldname, newname); err != nil {
		t.Fatal(err)
	}
}

func remove(t *testing.T, name string) {
	t.Helper()
	if err := os.Remove(name); err != nil {
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

func opsOfKind(ops []Op, kind OpKind) []Op {
	var out []Op
	for _, op := range ops {
		if op.Kind == kind {
			out = append(out, op)
		}
	}
	return out
}

// diffVerify computes the diff and asserts it replays to the target.
func diffVerify(t *testing.T, old, new *Snapshot) []Op {
	t.Helper()
	ops := Diff(old, new)
	if err := Verify(old, new, ops); err != nil {
		t.Fatalf("verify failed: %v\nops: %v", err, ops)
	}
	return ops
}

func TestIdenticalSnapshots(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a"), "alpha")
	mkdir(t, filepath.Join(root, "d"))
	writeFile(t, filepath.Join(root, "d", "b"), "beta")
	s1 := scan(t, root)
	s2 := scan(t, root)
	if ops := Diff(s1, s2); len(ops) != 0 {
		t.Fatalf("expected no ops, got %v", ops)
	}
}

func TestCrossMoveSwap(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a"), "content-a")
	writeFile(t, filepath.Join(root, "b"), "content-b")
	s1 := scan(t, root)

	// Swap a and b through a temporary name; inodes are preserved.
	tmp := filepath.Join(root, "tmp")
	rename(t, filepath.Join(root, "a"), tmp)
	rename(t, filepath.Join(root, "b"), filepath.Join(root, "a"))
	rename(t, tmp, filepath.Join(root, "b"))
	s2 := scan(t, root)

	ops := diffVerify(t, s1, s2)
	moves := opsOfKind(ops, OpMove)
	if len(moves) != 2 {
		t.Fatalf("expected 2 moves, got %v", ops)
	}
	for _, m := range moves {
		if !((m.From == "a" && m.To == "b") || (m.From == "b" && m.To == "a")) {
			t.Fatalf("unexpected move %v", m)
		}
	}
	if n := len(opsOfKind(ops, OpCreate)) + len(opsOfKind(ops, OpDelete)); n != 0 {
		t.Fatalf("swap must not create/delete, got %v", ops)
	}
}

func TestSameContentDifferentInodeIsDeleteCreate(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "x"), "identical")
	s1 := scan(t, root)

	// Create y before removing x so the two never share an inode.
	writeFile(t, filepath.Join(root, "y"), "identical")
	remove(t, filepath.Join(root, "x"))
	s2 := scan(t, root)

	ops := diffVerify(t, s1, s2)
	if moves := opsOfKind(ops, OpMove); len(moves) != 0 {
		t.Fatalf("same content with different inode must not be a move: %v", moves)
	}
	creates, deletes := opsOfKind(ops, OpCreate), opsOfKind(ops, OpDelete)
	if len(creates) != 1 || creates[0].Path != "y" {
		t.Fatalf("expected create y, got %v", ops)
	}
	if len(deletes) != 1 || deletes[0].Path != "x" {
		t.Fatalf("expected delete x, got %v", ops)
	}
}

func TestReplaceSamePathDifferentInode(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "p"), "v1")
	s1 := scan(t, root)

	// Replace via temp name: new inode at the same path.
	writeFile(t, filepath.Join(root, "q"), "v2")
	remove(t, filepath.Join(root, "p"))
	rename(t, filepath.Join(root, "q"), filepath.Join(root, "p"))
	s2 := scan(t, root)

	ops := diffVerify(t, s1, s2)
	if len(opsOfKind(ops, OpModify)) != 0 || len(opsOfKind(ops, OpMove)) != 0 {
		t.Fatalf("replacement is delete+create, got %v", ops)
	}
	if len(opsOfKind(ops, OpDelete)) != 1 || len(opsOfKind(ops, OpCreate)) != 1 {
		t.Fatalf("expected delete+create of p, got %v", ops)
	}
}

func TestHardLinkPathSetDiff(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "f"), "shared")
	s1 := scan(t, root)

	// Add a second name for the same inode.
	hardlink(t, filepath.Join(root, "f"), filepath.Join(root, "g"))
	s2 := scan(t, root)

	ops := diffVerify(t, s1, s2)
	if links := opsOfKind(ops, OpLink); len(links) != 1 || links[0].Path != "g" {
		t.Fatalf("expected link g, got %v", ops)
	}
	if len(ops) != 1 {
		t.Fatalf("expected exactly one op, got %v", ops)
	}

	// Remove the original name; the inode survives through g.
	remove(t, filepath.Join(root, "f"))
	s3 := scan(t, root)

	ops = diffVerify(t, s2, s3)
	if unlinks := opsOfKind(ops, OpUnlink); len(unlinks) != 1 || unlinks[0].Path != "f" {
		t.Fatalf("expected unlink f, got %v", ops)
	}
	if len(opsOfKind(ops, OpDelete)) != 0 {
		t.Fatalf("inode survives via g; no delete expected: %v", ops)
	}

	// Move plus extra link: {g} -> {h, k} emits a move and a link.
	hardlink(t, filepath.Join(root, "g"), filepath.Join(root, "k"))
	rename(t, filepath.Join(root, "g"), filepath.Join(root, "h"))
	s4 := scan(t, root)

	ops = diffVerify(t, s3, s4)
	if moves := opsOfKind(ops, OpMove); len(moves) != 1 || moves[0].From != "g" || moves[0].To != "h" {
		t.Fatalf("expected move g -> h, got %v", ops)
	}
	if links := opsOfKind(ops, OpLink); len(links) != 1 || links[0].Path != "k" {
		t.Fatalf("expected link k, got %v", ops)
	}
}

func TestDirMoveDoesNotHideInternalModification(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "d"))
	writeFile(t, filepath.Join(root, "d", "f"), "before")
	writeFile(t, filepath.Join(root, "d", "untouched"), "same")
	s1 := scan(t, root)

	// Modify in place (truncate keeps the inode), then move the directory.
	writeFile(t, filepath.Join(root, "d", "f"), "after")
	rename(t, filepath.Join(root, "d"), filepath.Join(root, "e"))
	s2 := scan(t, root)

	ops := diffVerify(t, s1, s2)
	moves := opsOfKind(ops, OpMove)
	if len(moves) != 1 || moves[0].From != "d" || moves[0].To != "e" {
		t.Fatalf("expected move d -> e, got %v", ops)
	}
	mods := opsOfKind(ops, OpModify)
	if len(mods) != 1 {
		t.Fatalf("internal modification must surface, got %v", ops)
	}
	if len(mods[0].Node.Paths) != 1 || mods[0].Node.Paths[0] != "e/f" {
		t.Fatalf("modify must reference the new path e/f, got %v", mods[0].Node.Paths)
	}
}

func TestCrossMoveDirectories(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "d1"))
	mkdir(t, filepath.Join(root, "d2"))
	writeFile(t, filepath.Join(root, "d1", "f"), "one")
	writeFile(t, filepath.Join(root, "d2", "g"), "two")
	s1 := scan(t, root)

	// Swap two directories.
	tmp := filepath.Join(root, "tmp")
	rename(t, filepath.Join(root, "d1"), tmp)
	rename(t, filepath.Join(root, "d2"), filepath.Join(root, "d1"))
	rename(t, tmp, filepath.Join(root, "d2"))
	s2 := scan(t, root)

	ops := diffVerify(t, s1, s2)
	if moves := opsOfKind(ops, OpMove); len(moves) != 2 {
		t.Fatalf("expected 2 directory moves, got %v", ops)
	}
	if n := len(opsOfKind(ops, OpCreate)) + len(opsOfKind(ops, OpDelete)); n != 0 {
		t.Fatalf("directory swap must not create/delete, got %v", ops)
	}
}

func TestCombinedScenario(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "dir"))
	mkdir(t, filepath.Join(root, "dir", "sub"))
	writeFile(t, filepath.Join(root, "swap1"), "s1")
	writeFile(t, filepath.Join(root, "swap2"), "s2")
	writeFile(t, filepath.Join(root, "dir", "keep"), "keep")
	writeFile(t, filepath.Join(root, "dir", "sub", "mod"), "old")
	writeFile(t, filepath.Join(root, "gone"), "gone")
	writeFile(t, filepath.Join(root, "lnk"), "lnk")
	hardlink(t, filepath.Join(root, "lnk"), filepath.Join(root, "lnk2"))
	s1 := scan(t, root)

	// Cross-move two files.
	tmp := filepath.Join(root, "tmp")
	rename(t, filepath.Join(root, "swap1"), tmp)
	rename(t, filepath.Join(root, "swap2"), filepath.Join(root, "swap1"))
	rename(t, tmp, filepath.Join(root, "swap2"))
	// Move a directory while modifying a file inside it.
	writeFile(t, filepath.Join(root, "dir", "sub", "mod"), "new")
	rename(t, filepath.Join(root, "dir"), filepath.Join(root, "moved"))
	// Hard link add and remove.
	hardlink(t, filepath.Join(root, "moved", "keep"), filepath.Join(root, "moved", "keep2"))
	remove(t, filepath.Join(root, "lnk2"))
	// Delete and create.
	remove(t, filepath.Join(root, "gone"))
	mkdir(t, filepath.Join(root, "fresh"))
	writeFile(t, filepath.Join(root, "fresh", "new"), "new")
	s2 := scan(t, root)

	ops := diffVerify(t, s1, s2)

	want := map[OpKind]int{
		OpMove:   3, // swap1<->swap2, dir -> moved
		OpModify: 1, // moved/sub/mod
		OpLink:   1, // moved/keep2
		OpUnlink: 1, // lnk2
		OpDelete: 1, // gone
		OpCreate: 2, // fresh, fresh/new
	}
	for kind, n := range want {
		if got := len(opsOfKind(ops, kind)); got != n {
			t.Fatalf("expected %d %s ops, got %d: %v", n, kind, got, ops)
		}
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a"), "alpha")
	s1 := scan(t, root)
	writeFile(t, filepath.Join(root, "b"), "beta")
	s2 := scan(t, root)

	ops := Diff(s1, s2)
	// Drop the create op: replay must no longer match the target.
	if err := Verify(s1, s2, nil); err == nil {
		t.Fatal("expected verify to fail with missing ops")
	}
	// Corrupt the digest of the created node.
	bad := append([]Op(nil), ops...)
	cp := *bad[0].Node
	cp.Digest = "sha256:corrupt"
	bad[0].Node = &cp
	if err := Verify(s1, s2, bad); err == nil {
		t.Fatal("expected verify to fail with corrupted digest")
	}
}
