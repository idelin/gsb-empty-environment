package main

import (
	"reflect"
	"testing"
)

func mustCreate(t *testing.T, tr *Tree, path string, dir bool) *Node {
	t.Helper()
	n, err := tr.Create(path, dir)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	return n
}

func mappingOf(tr *Tree) map[string]NodeID { return tr.Mapping() }

func TestStableIDsAcrossDirMove(t *testing.T) {
	tr := NewTree()
	a := mustCreate(t, tr, "/a", true)
	f := mustCreate(t, tr, "/a/f", false)
	mustCreate(t, tr, "/b", true)

	if _, _, err := tr.Rename("/a", "/b/c"); err != nil {
		t.Fatalf("rename dir: %v", err)
	}
	if a.ID != 1 || f.ID != 2 {
		t.Fatalf("IDs changed: a=%d f=%d", a.ID, f.ID)
	}
	want := map[string]NodeID{"/": 0, "/b": 3, "/b/c": 1, "/b/c/f": 2}
	if got := mappingOf(tr); !reflect.DeepEqual(got, want) {
		t.Fatalf("mapping = %v, want %v", got, want)
	}
}

func TestRenameReplacesFileTarget(t *testing.T) {
	tr := NewTree()
	x := mustCreate(t, tr, "/x", false)
	y := mustCreate(t, tr, "/y", false)

	moved, replaced, err := tr.Rename("/x", "/y")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if moved != x || replaced != y {
		t.Fatalf("moved=%v replaced=%v", moved.ID, replaced.ID)
	}
	if _, dead := tr.Nodes[y.ID]; dead {
		t.Fatalf("replaced node %d still live", y.ID)
	}
	want := map[string]NodeID{"/": 0, "/y": x.ID}
	if got := mappingOf(tr); !reflect.DeepEqual(got, want) {
		t.Fatalf("mapping = %v, want %v", got, want)
	}
}

func TestRenameReplacesEmptyDirOnly(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/src", true)
	mustCreate(t, tr, "/dst", true)
	mustCreate(t, tr, "/dst/child", false)

	if _, _, err := tr.Rename("/src", "/dst"); err == nil {
		t.Fatal("rename onto non-empty dir should fail")
	}
	mustUnlink(t, tr, "/dst/child")
	if _, replaced, err := tr.Rename("/src", "/dst"); err != nil {
		t.Fatalf("rename onto emptied dir: %v", err)
	} else if replaced == nil || replaced.ID != 2 {
		t.Fatalf("replaced = %v, want id=2", replaced)
	}
}

func mustUnlink(t *testing.T, tr *Tree, path string) {
	t.Helper()
	if _, err := tr.Unlink(path); err != nil {
		t.Fatalf("unlink %s: %v", path, err)
	}
}

func TestRenameTypeMismatchFails(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/f", false)
	mustCreate(t, tr, "/d", true)
	before := mappingOf(tr)

	if _, _, err := tr.Rename("/f", "/d"); err == nil {
		t.Fatal("file onto dir should fail")
	}
	if _, _, err := tr.Rename("/d", "/f"); err == nil {
		t.Fatal("dir onto file should fail")
	}
	if got := mappingOf(tr); !reflect.DeepEqual(got, before) {
		t.Fatalf("state changed after failed renames: %v", got)
	}
}

func TestRenameParentCycleForbidden(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/a", true)
	mustCreate(t, tr, "/a/b", true)
	mustCreate(t, tr, "/a/b/c", false)
	before := mappingOf(tr)

	for _, dst := range []string{"/a/b/x", "/a/b", "/a/b/c/d"} {
		if _, _, err := tr.Rename("/a", dst); err == nil {
			t.Fatalf("rename /a -> %s should fail (cycle)", dst)
		}
	}
	if got := mappingOf(tr); !reflect.DeepEqual(got, before) {
		t.Fatalf("state changed after cycle attempts: %v", got)
	}
}

func TestFailedRenameIsAtomic(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/a", true)
	mustCreate(t, tr, "/a/f", false)
	mustCreate(t, tr, "/g", false)
	before := mappingOf(tr)

	// missing dst parent, missing src, non-empty dir target: none may mutate
	if _, _, err := tr.Rename("/a/f", "/nope/f"); err == nil {
		t.Fatal("rename into missing parent should fail")
	}
	if _, _, err := tr.Rename("/ghost", "/g"); err == nil {
		t.Fatal("rename of missing src should fail")
	}
	if _, _, err := tr.Rename("/g", "/a"); err == nil {
		t.Fatal("file onto dir should fail")
	}
	if got := mappingOf(tr); !reflect.DeepEqual(got, before) {
		t.Fatalf("partial rename left behind: %v", got)
	}
}

func TestSameNameRecreateGetsFreshIdentity(t *testing.T) {
	tr := NewTree()
	first := mustCreate(t, tr, "/a", false)
	mustUnlink(t, tr, "/a")
	second := mustCreate(t, tr, "/a", false)
	if first.ID == second.ID {
		t.Fatalf("identity reused: %d", first.ID)
	}
	if _, ok := tr.Nodes[first.ID]; ok {
		t.Fatalf("old id %d still live", first.ID)
	}
}

func TestUnlinkRules(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/d", true)
	mustCreate(t, tr, "/d/f", false)
	if _, err := tr.Unlink("/d"); err == nil {
		t.Fatal("unlink of non-empty dir should fail")
	}
	if _, err := tr.Unlink("/"); err == nil {
		t.Fatal("unlink of root should fail")
	}
	if _, err := tr.Unlink("/missing"); err == nil {
		t.Fatal("unlink of missing path should fail")
	}
}

func TestRenameOntoSelfIsNoop(t *testing.T) {
	tr := NewTree()
	mustCreate(t, tr, "/a", true)
	moved, replaced, err := tr.Rename("/a", "/a")
	if err != nil || replaced != nil {
		t.Fatalf("self rename: moved=%v replaced=%v err=%v", moved, replaced, err)
	}
	if got, want := mappingOf(tr), map[string]NodeID{"/": 0, "/a": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mapping = %v, want %v", got, want)
	}
}
