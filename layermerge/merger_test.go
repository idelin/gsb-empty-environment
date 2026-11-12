package layermerge

import (
	"archive/tar"
	"bytes"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type tarEntry struct {
	name     string
	kind     byte
	linkname string
	mode     int64
}

func buildTar(t *testing.T, entries []tarEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     e.mode,
			Typeflag: e.kind,
			Linkname: e.linkname,
		}
		if e.kind == tar.TypeDir && !strings.HasSuffix(hdr.Name, "/") {
			hdr.Name += "/"
		}
		if hdr.Mode == 0 {
			if e.kind == tar.TypeDir {
				hdr.Mode = 0o755
			} else {
				hdr.Mode = 0o644
			}
		}
		var body []byte
		if e.kind == tar.TypeReg || e.kind == tar.TypeRegA {
			body = []byte("content:" + e.name)
			hdr.Size = int64(len(body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if body != nil {
			if _, err := tw.Write(body); err != nil {
				t.Fatalf("write body %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return &buf
}

func whiteout(name string) tarEntry {
	return tarEntry{name: name, kind: tar.TypeReg, mode: 0o644}
}

func applyLayers(t *testing.T, m *Merger, layers ...[]tarEntry) {
	t.Helper()
	for i, layer := range layers {
		if err := m.ApplyLayer(buildTar(t, layer)); err != nil {
			t.Fatalf("apply layer %d: %v", i, err)
		}
	}
}

func assertView(t *testing.T, m *Merger, want []string) {
	t.Helper()
	got := m.View()
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("view mismatch\n got: %v\nwant: %v", got, want)
	}
}

func assertType(t *testing.T, m *Merger, p string, want EntryType) {
	t.Helper()
	got, ok, err := m.Lookup(p)
	if err != nil {
		t.Fatalf("lookup %q: %v", p, err)
	}
	if !ok {
		t.Fatalf("expected %q in view, view=%v", p, m.View())
	}
	if got.Type != want {
		t.Fatalf("entry %q type = %d, want %d", p, got.Type, want)
	}
}

func assertAbsent(t *testing.T, m *Merger, p string) {
	t.Helper()
	if _, ok, err := m.Lookup(p); err != nil {
		t.Fatalf("lookup %q: %v", p, err)
	} else if ok {
		t.Fatalf("expected %q absent from view, view=%v", p, m.View())
	}
}

// TestSequentialInterleaving verifies ordered stacking across several layers:
// higher layers replace lower entries, whiteouts remove only lower content,
// opaque dirs mask lower siblings, and entries deleted in one layer can be
// reintroduced by a later layer.
func TestSequentialInterleaving(t *testing.T) {
	m := New()
	applyLayers(t, m,
		// layer 0: base tree
		[]tarEntry{
			{name: "etc/", kind: tar.TypeDir},
			{name: "etc/app.conf", kind: tar.TypeReg},
			{name: "etc/old.conf", kind: tar.TypeReg},
			{name: "etc/nested/", kind: tar.TypeDir},
			{name: "etc/nested/a.conf", kind: tar.TypeReg},
			{name: "usr/", kind: tar.TypeDir},
			{name: "usr/bin/", kind: tar.TypeDir},
			{name: "usr/bin/tool", kind: tar.TypeReg},
		},
		// layer 1: whiteout a file and make a directory opaque
		[]tarEntry{
			whiteout("etc/.wh.old.conf"),
			whiteout("etc/nested/.wh..wh..opq"),
			{name: "etc/nested/b.conf", kind: tar.TypeReg},
			{name: "usr/bin/tool2", kind: tar.TypeReg},
		},
		// layer 2: replace a file and resurrect a previously deleted one
		[]tarEntry{
			{name: "etc/app.conf", kind: tar.TypeReg, mode: 0o600},
			{name: "etc/old.conf", kind: tar.TypeSymlink, linkname: "app.conf"},
		},
	)

	assertView(t, m, []string{
		"etc",
		"etc/app.conf",
		"etc/old.conf",
		"etc/nested",
		"etc/nested/b.conf",
		"usr",
		"usr/bin",
		"usr/bin/tool",
		"usr/bin/tool2",
	})
	assertAbsent(t, m, "etc/nested/a.conf")
	assertType(t, m, "etc/old.conf", TypeSymlink)

	app, ok, err := m.Lookup("etc/app.conf")
	if err != nil || !ok {
		t.Fatalf("lookup etc/app.conf: ok=%v err=%v", ok, err)
	}
	if app.Layer != 2 {
		t.Fatalf("etc/app.conf layer = %d, want 2", app.Layer)
	}
	if app.Mode.Perm() != 0o600 {
		t.Fatalf("etc/app.conf perm = %o, want 0600", app.Mode.Perm())
	}

	b, ok, err := m.Lookup("etc/nested/b.conf")
	if err != nil || !ok {
		t.Fatalf("lookup b.conf: ok=%v err=%v", ok, err)
	}
	if b.Layer != 1 {
		t.Fatalf("b.conf layer = %d, want 1", b.Layer)
	}
	if m.LayerCount() != 3 {
		t.Fatalf("layer count = %d, want 3", m.LayerCount())
	}
}

// TestParentDirectoryWhiteout verifies that whiteouting a directory hides the
// directory together with every descendant inherited from lower layers.
func TestParentDirectoryWhiteout(t *testing.T) {
	m := New()
	applyLayers(t, m,
		[]tarEntry{
			{name: "etc/", kind: tar.TypeDir},
			{name: "etc/app/", kind: tar.TypeDir},
			{name: "etc/app/conf.d/", kind: tar.TypeDir},
			{name: "etc/app/conf.d/10-base.conf", kind: tar.TypeReg},
			{name: "etc/app/conf.d/20-extra.conf", kind: tar.TypeReg},
			{name: "etc/app/app.conf", kind: tar.TypeReg},
			{name: "etc/keep.conf", kind: tar.TypeReg},
		},
		[]tarEntry{
			whiteout("etc/.wh.app"),
			{name: "etc/new.conf", kind: tar.TypeReg},
		},
	)

	assertAbsent(t, m, "etc/app")
	assertAbsent(t, m, "etc/app/conf.d/10-base.conf")
	assertAbsent(t, m, "etc/app/conf.d/20-extra.conf")
	assertType(t, m, "etc/keep.conf", TypeRegular)
	assertType(t, m, "etc/new.conf", TypeRegular)
}

// TestDeleteAndRecreateSameLayer verifies that a directory deleted and
// recreated within one layer does not inherit any lower-layer content.
func TestDeleteAndRecreateSameLayer(t *testing.T) {
	m := New()
	applyLayers(t, m,
		[]tarEntry{
			{name: "data/", kind: tar.TypeDir},
			{name: "data/secret.conf", kind: tar.TypeReg},
			{name: "data/sub/", kind: tar.TypeDir},
			{name: "data/sub/old.txt", kind: tar.TypeReg},
		},
		// The opaque marker appears after entries in the tar on purpose:
		// markers must apply against the lower view regardless of ordering.
		[]tarEntry{
			{name: "data/", kind: tar.TypeDir, mode: 0o700},
			{name: "data/fresh.txt", kind: tar.TypeReg},
			whiteout("data/.wh..wh..opq"),
		},
	)

	assertView(t, m, []string{"data", "data/fresh.txt"})
	assertAbsent(t, m, "data/secret.conf")
	assertAbsent(t, m, "data/sub")
	assertAbsent(t, m, "data/sub/old.txt")

	dir, ok, err := m.Lookup("data")
	if err != nil || !ok {
		t.Fatalf("lookup data: ok=%v err=%v", ok, err)
	}
	if dir.Layer != 1 {
		t.Fatalf("data layer = %d, want 1 (recreated entry must win)", dir.Layer)
	}
}

// TestDeleteAndRecreateWithWhiteout covers the explicit ".wh." form of the
// same requirement: removing a directory then re-adding it in the same layer
// yields a fresh directory without lower-layer descendants.
func TestDeleteAndRecreateWithWhiteout(t *testing.T) {
	m := New()
	applyLayers(t, m,
		[]tarEntry{
			{name: "cfg/", kind: tar.TypeDir},
			{name: "cfg/a.conf", kind: tar.TypeReg},
			{name: "cfg/b.conf", kind: tar.TypeReg},
		},
		[]tarEntry{
			whiteout(".wh.cfg"),
			{name: "cfg/", kind: tar.TypeDir},
			{name: "cfg/c.conf", kind: tar.TypeReg},
		},
	)

	assertView(t, m, []string{"cfg", "cfg/c.conf"})
	assertAbsent(t, m, "cfg/a.conf")
	assertAbsent(t, m, "cfg/b.conf")
}

// TestProtectedPathRejected verifies the guardrail required by the import
// service: whiteouting a registered path (directly or via a parent) rejects
// the layer and leaves the merged view untouched.
func TestProtectedPathRejected(t *testing.T) {
	m := New()
	if err := m.Protect("etc/app/security.conf"); err != nil {
		t.Fatalf("protect: %v", err)
	}
	if err := m.Protect("/etc/app/"); err != nil {
		t.Fatalf("protect normalized dir: %v", err)
	}

	base := []tarEntry{
		{name: "etc/", kind: tar.TypeDir},
		{name: "etc/app/", kind: tar.TypeDir},
		{name: "etc/app/security.conf", kind: tar.TypeReg},
		{name: "etc/app/other.conf", kind: tar.TypeReg},
	}
	applyLayers(t, m, base)

	// Direct whiteout on the protected file, even with a same-layer rebuild:
	// the lower-layer entry was still hidden, so the layer is rejected.
	attack := buildTar(t, []tarEntry{
		whiteout("etc/app/.wh.security.conf"),
		{name: "etc/app/security.conf", kind: tar.TypeReg},
	})
	err := m.ApplyLayer(attack)
	var protectedErr *ProtectedPathError
	if !errors.As(err, &protectedErr) {
		t.Fatalf("want *ProtectedPathError, got %v", err)
	}
	if protectedErr.Protected != "etc/app/security.conf" {
		t.Fatalf("protected = %q", protectedErr.Protected)
	}
	if protectedErr.Removed != "etc/app/security.conf" {
		t.Fatalf("removed = %q", protectedErr.Removed)
	}
	if protectedErr.Layer != 1 || protectedErr.Marker != "etc/app/.wh.security.conf" {
		t.Fatalf("unexpected error detail: %+v", protectedErr)
	}

	// The failed layer must not have mutated the view or the layer counter.
	assertView(t, m, []string{
		"etc",
		"etc/app",
		"etc/app/security.conf",
		"etc/app/other.conf",
	})
	if m.LayerCount() != 1 {
		t.Fatalf("layer count after failed apply = %d, want 1", m.LayerCount())
	}

	// Parent-directory whiteout must also trip the registered directory.
	err = m.ApplyLayer(buildTar(t, []tarEntry{whiteout("etc/.wh.app")}))
	if !errors.As(err, &protectedErr) {
		t.Fatalf("want *ProtectedPathError for parent whiteout, got %v", err)
	}
	if protectedErr.Protected != "etc/app" || protectedErr.Removed != "etc/app" {
		t.Fatalf("unexpected parent error detail: %+v", protectedErr)
	}

	// An opaque directory marker hiding the protected path is rejected too.
	err = m.ApplyLayer(buildTar(t, []tarEntry{
		whiteout("etc/app/.wh..wh..opq"),
		{name: "etc/app/new.conf", kind: tar.TypeReg},
	}))
	if !errors.As(err, &protectedErr) {
		t.Fatalf("want *ProtectedPathError for opaque dir, got %v", err)
	}
	if protectedErr.Marker != "etc/app/.wh..wh..opq" {
		t.Fatalf("marker = %q", protectedErr.Marker)
	}
}

// TestProtectedPathNotPresentAllowsWhiteout verifies that protection only
// fires when a lower-layer protected entry actually exists, and that a later
// benign layer can still be applied.
func TestProtectedPathNotPresentAllowsWhiteout(t *testing.T) {
	m := New()
	if err := m.Protect("etc/missing.conf"); err != nil {
		t.Fatalf("protect: %v", err)
	}
	applyLayers(t, m,
		[]tarEntry{
			{name: "etc/", kind: tar.TypeDir},
			{name: "etc/transient.conf", kind: tar.TypeReg},
		},
		[]tarEntry{whiteout("etc/.wh.transient.conf")},
		[]tarEntry{{name: "etc/missing.conf", kind: tar.TypeReg}},
	)
	assertType(t, m, "etc/missing.conf", TypeRegular)
	assertAbsent(t, m, "etc/transient.conf")
}
