package archivemerge

import (
	"reflect"
	"testing"
)

func nodeMap(s Snapshot) map[string]Node {
	m := map[string]Node{}
	for _, n := range s.Nodes {
		m[n.Path] = n
	}
	return m
}

func applyAll(t *testing.T, manifests []Manifest) []Snapshot {
	t.Helper()
	snaps, err := Merge(manifests)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(snaps) != len(manifests) {
		t.Fatalf("got %d snapshots, want %d", len(snaps), len(manifests))
	}
	return snaps
}

func wantNodes(t *testing.T, snap Snapshot, want map[string]Node) {
	t.Helper()
	got := nodeMap(snap)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot gen %d mismatch:\n got: %v\nwant: %v", snap.Generation, got, want)
	}
	// Nodes must be sorted by path.
	for i := 1; i < len(snap.Nodes); i++ {
		if snap.Nodes[i-1].Path >= snap.Nodes[i].Path {
			t.Fatalf("snapshot gen %d not sorted: %v", snap.Generation, snap.Nodes)
		}
	}
}

func f(path string) Node           { return Node{Path: path, Kind: KindFile} }
func d(path string, imp bool) Node { return Node{Path: path, Kind: KindDir, Implicit: imp} }

func TestBasicOverlayAndProvenance(t *testing.T) {
	snaps := applyAll(t, []Manifest{
		{Generation: 1, Entries: []Entry{
			{"/a/b.txt", KindFile},
			{"/docs", KindDir},
			{"/docs/readme.md", KindFile},
		}},
		{Generation: 2, Entries: []Entry{
			{"/a/c.txt", KindFile},
			{"/docs/readme.md", KindFile}, // re-archived in gen 2
		}},
	})

	wantNodes(t, snaps[0], map[string]Node{
		"/a":              {"/a", KindDir, 1, true},
		"/a/b.txt":        {"/a/b.txt", KindFile, 1, false},
		"/docs":           {"/docs", KindDir, 1, false},
		"/docs/readme.md": {"/docs/readme.md", KindFile, 1, false},
	})
	wantNodes(t, snaps[1], map[string]Node{
		"/a":              {"/a", KindDir, 1, true}, // provenance kept from gen 1
		"/a/b.txt":        {"/a/b.txt", KindFile, 1, false},
		"/a/c.txt":        {"/a/c.txt", KindFile, 2, false},
		"/docs":           {"/docs", KindDir, 1, false},
		"/docs/readme.md": {"/docs/readme.md", KindFile, 2, false}, // newer source
	})
}

func TestParentTypeChangeFileToDir(t *testing.T) {
	snaps := applyAll(t, []Manifest{
		{Generation: 1, Entries: []Entry{{"/a", KindFile}}},
		{Generation: 2, Entries: []Entry{{"/a/b", KindFile}}},
	})

	wantNodes(t, snaps[0], map[string]Node{
		"/a": {"/a", KindFile, 1, false},
	})
	// The gen-1 file at /a is shadowed by the gen-2 child: /a becomes an
	// implicit directory sourced from gen 2.
	wantNodes(t, snaps[1], map[string]Node{
		"/a":   {"/a", KindDir, 2, true},
		"/a/b": {"/a/b", KindFile, 2, false},
	})
}

func TestParentTypeChangeDirToFile(t *testing.T) {
	snaps := applyAll(t, []Manifest{
		{Generation: 1, Entries: []Entry{
			{"/a/x", KindFile},
			{"/a/y/z", KindFile},
		}},
		{Generation: 2, Entries: []Entry{{"/a", KindFile}}},
	})

	wantNodes(t, snaps[0], map[string]Node{
		"/a":     {"/a", KindDir, 1, true},
		"/a/x":   {"/a/x", KindFile, 1, false},
		"/a/y":   {"/a/y", KindDir, 1, true},
		"/a/y/z": {"/a/y/z", KindFile, 1, false},
	})
	// The gen-2 file replaces the whole subtree.
	wantNodes(t, snaps[1], map[string]Node{
		"/a": {"/a", KindFile, 2, false},
	})
}

func TestAncestorDeletionByDirTombstone(t *testing.T) {
	snaps := applyAll(t, []Manifest{
		{Generation: 1, Entries: []Entry{
			{"/a/b/c.txt", KindFile},
			{"/a/keep.txt", KindFile},
			{"/other.txt", KindFile},
		}},
		{Generation: 2, Entries: []Entry{{"/a", KindDirTombstone}}},
	})

	if _, ok := nodeMap(snaps[0])["/a/b/c.txt"]; !ok {
		t.Fatalf("gen 1 should contain /a/b/c.txt")
	}
	// The tombstone hides the whole /a subtree; siblings survive.
	wantNodes(t, snaps[1], map[string]Node{
		"/other.txt": {"/other.txt", KindFile, 1, false},
	})
}

func TestLocalRebuildAfterDirTombstone(t *testing.T) {
	snaps := applyAll(t, []Manifest{
		{Generation: 1, Entries: []Entry{
			{"/a/old1.txt", KindFile},
			{"/a/old2.txt", KindFile},
		}},
		{Generation: 2, Entries: []Entry{{"/a", KindDirTombstone}}},
		{Generation: 3, Entries: []Entry{
			{"/a", KindDir}, // explicit rebuild of the directory
			{"/a/new.txt", KindFile},
		}},
	})

	wantNodes(t, snaps[1], map[string]Node{})
	// Old descendants stay hidden; only gen-3 rebuild entries are visible.
	wantNodes(t, snaps[2], map[string]Node{
		"/a":         {"/a", KindDir, 3, false},
		"/a/new.txt": {"/a/new.txt", KindFile, 3, false},
	})
}

func TestImplicitRebuildAfterDirTombstone(t *testing.T) {
	snaps := applyAll(t, []Manifest{
		{Generation: 1, Entries: []Entry{{"/a/old.txt", KindFile}}},
		{Generation: 2, Entries: []Entry{{"/a", KindDirTombstone}}},
		{Generation: 3, Entries: []Entry{{"/a/new.txt", KindFile}}},
	})

	wantNodes(t, snaps[2], map[string]Node{
		"/a":         {"/a", KindDir, 3, true}, // implicit ancestor of the rebuild
		"/a/new.txt": {"/a/new.txt", KindFile, 3, false},
	})
}

func TestTombstoneKindMatching(t *testing.T) {
	snaps := applyAll(t, []Manifest{
		{Generation: 1, Entries: []Entry{
			{"/f", KindFile},
			{"/g/x", KindFile},
		}},
		{Generation: 2, Entries: []Entry{
			{"/f", KindDirTombstone},  // dir tombstone must not remove a file
			{"/g", KindFileTombstone}, // file tombstone must not remove a dir
		}},
	})

	wantNodes(t, snaps[1], map[string]Node{
		"/f":   {"/f", KindFile, 1, false},
		"/g":   {"/g", KindDir, 1, true},
		"/g/x": {"/g/x", KindFile, 1, false},
	})
}

func TestFileTombstoneAndRewrite(t *testing.T) {
	snaps := applyAll(t, []Manifest{
		{Generation: 1, Entries: []Entry{{"/f", KindFile}, {"/g", KindFile}}},
		{Generation: 2, Entries: []Entry{{"/f", KindFileTombstone}}},
		{Generation: 3, Entries: []Entry{{"/f", KindFile}}},
	})

	wantNodes(t, snaps[1], map[string]Node{
		"/g": {"/g", KindFile, 1, false},
	})
	wantNodes(t, snaps[2], map[string]Node{
		"/f": {"/f", KindFile, 3, false},
		"/g": {"/g", KindFile, 1, false},
	})
}

func TestExplicitDirPromotesImplicit(t *testing.T) {
	snaps := applyAll(t, []Manifest{
		{Generation: 1, Entries: []Entry{{"/a/b", KindFile}}},
		{Generation: 2, Entries: []Entry{{"/a", KindDir}}},
	})

	wantNodes(t, snaps[1], map[string]Node{
		"/a":   {"/a", KindDir, 2, false}, // promoted: explicit, re-sourced to gen 2
		"/a/b": {"/a/b", KindFile, 1, false},
	})
}

func TestInvalidPath(t *testing.T) {
	m := NewMerger()
	if _, err := m.Apply(Manifest{Generation: 1, Entries: []Entry{{"/", KindFile}}}); err == nil {
		t.Fatal("expected error for root path entry")
	}
	if _, err := m.Apply(Manifest{Generation: 2, Entries: []Entry{{"", KindDir}}}); err == nil {
		t.Fatal("expected error for empty path entry")
	}
}

func TestSnapshotGetAndNormalization(t *testing.T) {
	snaps := applyAll(t, []Manifest{
		{Generation: 1, Entries: []Entry{{"a//b/./c.txt", KindFile}}},
	})
	n, ok := snaps[0].Get("a/b/c.txt")
	if !ok || n.Kind != KindFile || n.SourceGen != 1 {
		t.Fatalf("Get with normalized path failed: %v %v", n, ok)
	}
	if _, ok := snaps[0].Get("/a/b"); !ok {
		t.Fatal("Get should find implicit dir with leading slash")
	}
}
