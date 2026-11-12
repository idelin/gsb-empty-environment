package layermerge

import (
	"archive/tar"
	"testing"
)

// TestSameLayerWhiteoutIsolation verifies the core ordering rule: a whiteout
// in one tar stream only hides entries from lower layers. A same-layer file
// with the same base name must survive regardless of tar entry order.
func TestSameLayerWhiteoutIsolation(t *testing.T) {
	m := New()
	applyLayers(t, m,
		[]tarEntry{
			{name: "app/", kind: tar.TypeDir},
			{name: "app/policy.yaml", kind: tar.TypeReg},
		},
		// Whiteout precedes the recreated entry inside the same tar.
		[]tarEntry{
			whiteout("app/.wh.policy.yaml"),
			{name: "app/policy.yaml", kind: tar.TypeReg, mode: 0o640},
			{name: "app/extra.yaml", kind: tar.TypeReg},
		},
	)

	assertType(t, m, "app/policy.yaml", TypeRegular)
	assertType(t, m, "app/extra.yaml", TypeRegular)

	entry, ok, err := m.Lookup("app/policy.yaml")
	if err != nil || !ok {
		t.Fatalf("lookup policy: ok=%v err=%v", ok, err)
	}
	if entry.Layer != 1 || entry.Mode.Perm() != 0o640 {
		t.Fatalf("unexpected recreated entry: %+v", entry)
	}
}

// TestWhiteoutTargetMissingIsNoop checks that whiteouting a path absent from
// lower layers is harmless, including when the same layer creates it.
func TestWhiteoutTargetMissingIsNoop(t *testing.T) {
	m := New()
	applyLayers(t, m,
		[]tarEntry{
			whiteout(".wh.never-existed"),
			whiteout("dir/.wh.ghost"),
			{name: "dir/ghost", kind: tar.TypeReg},
		},
	)
	assertType(t, m, "dir/ghost", TypeRegular)
	if _, ok, err := m.Lookup(".wh.never-existed"); err != nil || ok {
		t.Fatalf("whiteout marker leaked into view: ok=%v err=%v", ok, err)
	}
}

// TestUnsafePathRejected verifies that entries escaping the root cannot
// poison the merged view and that a rejected layer stays atomic.
func TestUnsafePathRejected(t *testing.T) {
	m := New()
	applyLayers(t, m, []tarEntry{
		{name: "safe/", kind: tar.TypeDir},
		{name: "safe/file", kind: tar.TypeReg},
	})

	for _, bad := range []string{"../escape", "a/../../escape", "/../escape"} {
		err := m.ApplyLayer(buildTar(t, []tarEntry{{name: bad, kind: tar.TypeReg}}))
		if err == nil {
			t.Fatalf("expected error for unsafe path %q", bad)
		}
	}
	if m.LayerCount() != 1 {
		t.Fatalf("layer count = %d, want 1 after rejected layers", m.LayerCount())
	}
	assertType(t, m, "safe/file", TypeRegular)

	if err := m.Protect("../no"); err == nil {
		t.Fatal("expected Protect to reject unsafe path")
	}
	if _, _, err := m.Lookup("../no"); err == nil {
		t.Fatal("expected Lookup to reject unsafe path")
	}
}

// TestSnapshotIsolationAndParents verifies the defensive copy of the view and
// automatic synthesis of missing parent directories.
func TestSnapshotIsolationAndParents(t *testing.T) {
	m := New()
	applyLayers(t, m, []tarEntry{
		{name: "deep/nested/tree/file.txt", kind: tar.TypeReg},
	})

	snap := m.Snapshot()
	snap["deep/nested/tree/file.txt"] = Entry{Type: TypeSymlink}
	delete(snap, "deep")

	entry, ok, err := m.Lookup("deep/nested/tree/file.txt")
	if err != nil || !ok || entry.Type != TypeRegular {
		t.Fatalf("snapshot mutation leaked: %+v ok=%v err=%v", entry, ok, err)
	}
	assertType(t, m, "deep", TypeDir)
	assertType(t, m, "deep/nested", TypeDir)
	assertType(t, m, "deep/nested/tree", TypeDir)
}

// TestHardlinkResolution checks that hardlinks resolve against the current
// merged view and follow entries recreated after a lower-layer whiteout.
func TestHardlinkResolution(t *testing.T) {
	m := New()
	applyLayers(t, m,
		[]tarEntry{
			{name: "originals/", kind: tar.TypeDir},
			{name: "originals/a", kind: tar.TypeReg},
			{name: "links/", kind: tar.TypeDir},
			{name: "links/a-link", kind: tar.TypeLink, linkname: "originals/a"},
		},
		[]tarEntry{
			whiteout("originals/.wh.a"),
			{name: "originals/a", kind: tar.TypeReg, mode: 0o600},
			{name: "links/a-link2", kind: tar.TypeLink, linkname: "/originals/a"},
		},
	)

	link, ok, err := m.Lookup("links/a-link")
	if err != nil || !ok {
		t.Fatalf("lookup hardlink: ok=%v err=%v", ok, err)
	}
	if link.Type != TypeHardlink || link.Linkname != "originals/a" {
		t.Fatalf("unexpected hardlink entry: %+v", link)
	}
	link2, ok, err := m.Lookup("links/a-link2")
	if err != nil || !ok {
		t.Fatalf("lookup second hardlink: ok=%v err=%v", ok, err)
	}
	if link2.Linkname != "originals/a" || link2.Layer != 1 {
		t.Fatalf("unexpected second hardlink: %+v", link2)
	}

	err = m.ApplyLayer(buildTar(t, []tarEntry{
		{name: "dangling", kind: tar.TypeLink, linkname: "missing/target"},
	}))
	if err == nil {
		t.Fatal("expected error for dangling hardlink")
	}
	if m.LayerCount() != 2 {
		t.Fatalf("dangling-link layer must be rejected, layer count = %d", m.LayerCount())
	}
}

// TestOpaqueDirectoryKeepsItself ensures an opaque marker masks children only
// and never removes the containing directory or its same-layer children.
func TestOpaqueDirectoryKeepsItself(t *testing.T) {
	m := New()
	applyLayers(t, m,
		[]tarEntry{
			{name: "vol/", kind: tar.TypeDir, mode: 0o750},
			{name: "vol/old", kind: tar.TypeReg},
			{name: "vol/old-sub/", kind: tar.TypeDir},
			{name: "vol/old-sub/x", kind: tar.TypeReg},
		},
		[]tarEntry{
			whiteout("vol/.wh..wh..opq"),
			{name: "vol/new", kind: tar.TypeReg},
		},
	)

	assertView(t, m, []string{"vol", "vol/new"})
	dir, ok, err := m.Lookup("vol")
	if err != nil || !ok {
		t.Fatalf("lookup vol: ok=%v err=%v", ok, err)
	}
	if dir.Layer != 0 {
		t.Fatalf("opaque marker must not replace the directory itself, layer=%d", dir.Layer)
	}
}
