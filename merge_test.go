package archivemerge

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// vnode is the expected form of one visible path in the virtual tree model.
type vnode struct {
	kind     Kind
	source   int
	explicit bool
}

// vtree is the expected visible tree after one generation: path -> vnode.
type vtree map[string]vnode

func file(gen int) vnode { return vnode{kind: File, source: gen, explicit: true} }
func dir(gen int) vnode  { return vnode{kind: Dir, source: gen, explicit: true} }
func idir(gen int) vnode { return vnode{kind: Dir, source: gen, explicit: false} }

// checkSnapshot compares a snapshot against the expected virtual tree.
func checkSnapshot(t *testing.T, snap Snapshot, want vtree) {
	t.Helper()
	got := vtree{}
	for _, n := range snap.Flatten() {
		got[n.Path] = vnode{kind: n.Kind, source: n.Source, explicit: n.Explicit}
	}
	if len(got) != len(want) {
		t.Fatalf("gen %d: visible paths = %v, want %v", snap.Generation, got.paths(), want.paths())
	}
	for path, w := range want {
		g, ok := got[path]
		if !ok {
			t.Fatalf("gen %d: missing path %q; got %v", snap.Generation, path, got.paths())
		}
		if g != w {
			t.Errorf("gen %d: path %q = {kind:%v source:%d explicit:%v}, want {kind:%v source:%d explicit:%v}",
				snap.Generation, path, g.kind, g.source, g.explicit, w.kind, w.source, w.explicit)
		}
	}
}

func (t1 vtree) paths() []string {
	out := make([]string, 0, len(t1))
	for p := range t1 {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// run merges the manifests and checks the virtual tree after each generation.
func run(t *testing.T, manifests []Manifest, wants []vtree) {
	t.Helper()
	if len(manifests) != len(wants) {
		t.Fatalf("need one expected vtree per generation")
	}
	snaps, err := Merge(manifests)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for i, want := range wants {
		checkSnapshot(t, snaps[i], want)
	}
}

func TestBasicAccumulation(t *testing.T) {
	run(t, []Manifest{
		{Generation: 1, Entries: []Entry{
			{Path: "a/b.txt", Kind: File},
			{Path: "a/c", Kind: Dir},
		}},
		{Generation: 2, Entries: []Entry{
			{Path: "a/c/d.txt", Kind: File},
		}},
	}, []vtree{
		{
			"a":       idir(1),
			"a/b.txt": file(1),
			"a/c":     dir(1),
		},
		{
			"a":         idir(1),
			"a/b.txt":   file(1),
			"a/c":       dir(1),
			"a/c/d.txt": file(2),
		},
	})
}

func TestParentTypeChangeFileToDir(t *testing.T) {
	// gen 1: "a" is a file. gen 2: "a/b" appears, so "a" becomes a dir.
	run(t, []Manifest{
		{Generation: 1, Entries: []Entry{{Path: "a", Kind: File}}},
		{Generation: 2, Entries: []Entry{{Path: "a/b", Kind: File}}},
	}, []vtree{
		{"a": file(1)},
		{"a": idir(2), "a/b": file(2)},
	})
}

func TestParentTypeChangeDirToFile(t *testing.T) {
	// gen 1: "a" is a dir with children. gen 2: "a" becomes a file,
	// wiping the whole subtree.
	run(t, []Manifest{
		{Generation: 1, Entries: []Entry{
			{Path: "a", Kind: Dir},
			{Path: "a/b/c.txt", Kind: File},
		}},
		{Generation: 2, Entries: []Entry{{Path: "a", Kind: File}}},
	}, []vtree{
		{"a": dir(1), "a/b": idir(1), "a/b/c.txt": file(1)},
		{"a": file(2)},
	})
}

func TestAncestorDeletion(t *testing.T) {
	// Tombstone on "a" removes it and every inherited descendant.
	run(t, []Manifest{
		{Generation: 1, Entries: []Entry{
			{Path: "a/b/c.txt", Kind: File},
			{Path: "a/d.txt", Kind: File},
			{Path: "keep.txt", Kind: File},
		}},
		{Generation: 2, Entries: []Entry{{Path: "a", Kind: Tombstone}}},
	}, []vtree{
		{
			"a":         idir(1),
			"a/b":       idir(1),
			"a/b/c.txt": file(1),
			"a/d.txt":   file(1),
			"keep.txt":  file(1),
		},
		{"keep.txt": file(1)},
	})
}

func TestPartialRebuildAfterDirTombstone(t *testing.T) {
	// gen 2 tombstones "a" but explicitly rebuilds "a/b"; the rebuild
	// survives while the inherited "a/c" stays shadowed.
	run(t, []Manifest{
		{Generation: 1, Entries: []Entry{
			{Path: "a/b", Kind: File},
			{Path: "a/c", Kind: File},
		}},
		{Generation: 2, Entries: []Entry{
			{Path: "a", Kind: Tombstone},
			{Path: "a/b", Kind: File},
		}},
	}, []vtree{
		{"a": idir(1), "a/b": file(1), "a/c": file(1)},
		{"a": idir(2), "a/b": file(2)},
	})
}

func TestTombstoneShadowsOnlyOlderGenerations(t *testing.T) {
	// gen 2 tombstones "a"; gen 3 rebuilds under "a" without any new
	// tombstone, so the rebuild is fully visible.
	run(t, []Manifest{
		{Generation: 1, Entries: []Entry{
			{Path: "a/old.txt", Kind: File},
		}},
		{Generation: 2, Entries: []Entry{
			{Path: "a", Kind: Tombstone},
		}},
		{Generation: 3, Entries: []Entry{
			{Path: "a/new.txt", Kind: File},
		}},
	}, []vtree{
		{"a": idir(1), "a/old.txt": file(1)},
		{},
		{"a": idir(3), "a/new.txt": file(3)},
	})
}

func TestTombstoneFileOnly(t *testing.T) {
	// Tombstone on a file removes just the file, not its siblings.
	run(t, []Manifest{
		{Generation: 1, Entries: []Entry{
			{Path: "a/x.txt", Kind: File},
			{Path: "a/y.txt", Kind: File},
		}},
		{Generation: 2, Entries: []Entry{
			{Path: "a/x.txt", Kind: Tombstone},
		}},
	}, []vtree{
		{"a": idir(1), "a/x.txt": file(1), "a/y.txt": file(1)},
		{"a": idir(1), "a/y.txt": file(1)},
	})
}

func TestExplicitDirPromotionKeepsChildren(t *testing.T) {
	// An implicit dir later made explicit keeps its inherited children.
	run(t, []Manifest{
		{Generation: 1, Entries: []Entry{{Path: "a/b.txt", Kind: File}}},
		{Generation: 2, Entries: []Entry{{Path: "a", Kind: Dir}}},
	}, []vtree{
		{"a": idir(1), "a/b.txt": file(1)},
		{"a": dir(2), "a/b.txt": file(1)},
	})
}

func TestOverwriteUpdatesSource(t *testing.T) {
	run(t, []Manifest{
		{Generation: 1, Entries: []Entry{{Path: "f.txt", Kind: File}}},
		{Generation: 5, Entries: []Entry{{Path: "f.txt", Kind: File}}},
	}, []vtree{
		{"f.txt": file(1)},
		{"f.txt": file(5)},
	})
}

func TestNestedTombstoneAndRebuildOrdering(t *testing.T) {
	// Entries arrive tombstone-first in the manifest but the rebuild is
	// deeper; generation semantics must not depend on entry order.
	run(t, []Manifest{
		{Generation: 1, Entries: []Entry{
			{Path: "a/b/c.txt", Kind: File},
			{Path: "a/b/d.txt", Kind: File},
		}},
		{Generation: 2, Entries: []Entry{
			{Path: "a/b/c.txt", Kind: File}, // listed before the tombstone
			{Path: "a", Kind: Tombstone},
		}},
	}, []vtree{
		{"a": idir(1), "a/b": idir(1), "a/b/c.txt": file(1), "a/b/d.txt": file(1)},
		{"a": idir(2), "a/b": idir(2), "a/b/c.txt": file(2)},
	})
}

func TestDeepParentChainTypeChange(t *testing.T) {
	// gen 1: "a/b" is a file. gen 2: "a/b/c/d.txt" forces both "a" and
	// "a/b" to be directories.
	run(t, []Manifest{
		{Generation: 1, Entries: []Entry{{Path: "a/b", Kind: File}}},
		{Generation: 2, Entries: []Entry{{Path: "a/b/c/d.txt", Kind: File}}},
	}, []vtree{
		{"a": idir(1), "a/b": file(1)},
		{"a": idir(1), "a/b": idir(2), "a/b/c": idir(2), "a/b/c/d.txt": file(2)},
	})
}

func TestFindAndFlatten(t *testing.T) {
	snaps, err := Merge([]Manifest{
		{Generation: 1, Entries: []Entry{
			{Path: "x/y/z.txt", Kind: File},
			{Path: "x/w", Kind: Dir},
		}},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	snap := snaps[0]
	if n := snap.Find("x/y/z.txt"); n == nil || n.Kind != File || n.Source != 1 {
		t.Errorf("Find(x/y/z.txt) = %+v", n)
	}
	if n := snap.Find("x"); n == nil || n.Kind != Dir || n.Explicit {
		t.Errorf("Find(x) = %+v, want implicit dir", n)
	}
	if n := snap.Find("x/missing"); n != nil {
		t.Errorf("Find(x/missing) = %+v, want nil", n)
	}
	var names []string
	for _, n := range snap.Flatten() {
		names = append(names, fmt.Sprintf("%s:%s", n.Path, n.Kind))
	}
	want := []string{"x:dir", "x/w:dir", "x/y:dir", "x/y/z.txt:file"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("Flatten = %v, want %v", names, want)
	}
}

func TestInvalidPathsRejected(t *testing.T) {
	bad := []string{"", "/", "/abs", "trail/", "a//b", "a/./b", "a/../b"}
	for _, p := range bad {
		_, err := Merge([]Manifest{{Generation: 1, Entries: []Entry{{Path: p, Kind: File}}}})
		if err == nil {
			t.Errorf("path %q: expected error, got nil", p)
		}
	}
}
