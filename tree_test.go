package main

import (
	"reflect"
	"testing"
)

func mustCreate(t *testing.T, tr *Tree, path string, id int64, dir bool) {
	t.Helper()
	if err := tr.Create(path, id, dir); err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
}

func TestDirMoveKeepsIdentity(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/a", 1, true)
	mustCreate(t, tr, "/a/b", 2, true)
	mustCreate(t, tr, "/a/b/f.txt", 3, false)
	mustCreate(t, tr, "/c", 4, true)

	if err := tr.Rename("/a", "/c/a2"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	want := map[string]int64{"/": 0, "/c": 4, "/c/a2": 1, "/c/a2/b": 2, "/c/a2/b/f.txt": 3}
	if got := tr.Mapping(); !reflect.DeepEqual(got, want) {
		t.Fatalf("mapping = %v, want %v", got, want)
	}
}

func TestRenameReplaceTarget(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/src", 1, false)
	mustCreate(t, tr, "/dst", 2, false)
	if err := tr.Rename("/src", "/dst"); err != nil {
		t.Fatalf("rename replace: %v", err)
	}
	want := map[string]int64{"/": 0, "/dst": 1}
	if got := tr.Mapping(); !reflect.DeepEqual(got, want) {
		t.Fatalf("mapping = %v, want %v", got, want)
	}
	if _, ok := tr.byID[2]; ok {
		t.Fatal("replaced node id 2 still registered")
	}
}

func TestRenameReplaceRules(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/f", 1, false)
	mustCreate(t, tr, "/d", 2, true)
	mustCreate(t, tr, "/d/x", 3, false)
	mustCreate(t, tr, "/empty", 4, true)

	before := tr.Mapping()
	if err := tr.Rename("/f", "/d"); err == nil {
		t.Fatal("file over non-empty dir should fail")
	}
	if err := tr.Rename("/d", "/f"); err == nil {
		t.Fatal("dir over file should fail")
	}
	if got := tr.Mapping(); !reflect.DeepEqual(got, before) {
		t.Fatalf("failed renames mutated tree: %v", got)
	}
	// POSIX: a directory may replace an empty directory.
	if err := tr.Rename("/d", "/empty"); err != nil {
		t.Fatalf("dir over empty dir should succeed: %v", err)
	}
	want := map[string]int64{"/": 0, "/f": 1, "/empty": 2, "/empty/x": 3}
	if got := tr.Mapping(); !reflect.DeepEqual(got, want) {
		t.Fatalf("mapping = %v, want %v", got, want)
	}
}

func TestRenameCycleRejected(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/a", 1, true)
	mustCreate(t, tr, "/a/b", 2, true)
	mustCreate(t, tr, "/a/b/c", 3, true)

	before := tr.Mapping()
	for _, dst := range []string{"/a/b/a2", "/a/b/c/deep", "/a/self"} {
		if err := tr.Rename("/a", dst); err == nil {
			t.Fatalf("rename /a -> %s should be rejected as a cycle", dst)
		}
	}
	if got := tr.Mapping(); !reflect.DeepEqual(got, before) {
		t.Fatalf("rejected cyclic rename mutated tree: %v", got)
	}
}

func TestRenameAtomicOnFailure(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/a", 1, true)
	mustCreate(t, tr, "/a/f", 2, false)
	mustCreate(t, tr, "/b", 3, true)

	before := tr.Mapping()
	cases := [][2]string{
		{"/a", "/nonexistent/x"}, // missing parent
		{"/a/f", "/b"},           // file over dir
		{"/nope", "/b/x"},        // missing source
	}
	for _, c := range cases {
		if err := tr.Rename(c[0], c[1]); err == nil {
			t.Fatalf("rename %s -> %s should fail", c[0], c[1])
		}
		if got := tr.Mapping(); !reflect.DeepEqual(got, before) {
			t.Fatalf("failed rename %s -> %s left partial state: %v", c[0], c[1], got)
		}
	}
}

func TestRenameSelfIsNoop(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/a", 1, true)
	mustCreate(t, tr, "/a/f", 2, false)
	before := tr.Mapping()
	if err := tr.Rename("/a", "/a"); err != nil {
		t.Fatalf("self rename: %v", err)
	}
	if got := tr.Mapping(); !reflect.DeepEqual(got, before) {
		t.Fatalf("self rename changed mapping: %v", got)
	}
}

func TestUnlinkRequiresEmptyDir(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/d", 1, true)
	mustCreate(t, tr, "/d/f", 2, false)
	if err := tr.Unlink("/d"); err == nil {
		t.Fatal("unlink of non-empty dir should fail")
	}
	if err := tr.Unlink("/d/f"); err != nil {
		t.Fatalf("unlink file: %v", err)
	}
	if err := tr.Unlink("/d"); err != nil {
		t.Fatalf("unlink emptied dir: %v", err)
	}
	want := map[string]int64{"/": 0}
	if got := tr.Mapping(); !reflect.DeepEqual(got, want) {
		t.Fatalf("mapping = %v, want %v", got, want)
	}
}

func TestDuplicateIDRejected(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/a", 7, true)
	if err := tr.Create("/b", 7, true); err == nil {
		t.Fatal("duplicate id should be rejected")
	}
}
