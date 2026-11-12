package layermerge

import (
	"archive/tar"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestProtectedPathErrorMessage(t *testing.T) {
	err := &ProtectedPathError{
		Protected: "etc/sec/app.conf",
		Removed:   "etc/sec",
		Layer:     2,
		Marker:    "etc/.wh.sec",
	}
	msg := err.Error()
	for _, want := range []string{"etc/sec/app.conf", "etc/sec", "etc/.wh.sec", "layer 2"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error message %q missing %q", msg, want)
		}
	}
}

func TestSpecialEntryTypesAndUnsupportedFlag(t *testing.T) {
	m := New()
	applyLayers(t, m, []tarEntry{
		{name: "dev/null", kind: tar.TypeChar},
		{name: "dev/disk0", kind: tar.TypeBlock},
		{name: "run/pipe", kind: tar.TypeFifo},
		{name: "run/link", kind: tar.TypeSymlink, linkname: "../dev/null"},
	})

	assertType(t, m, "dev/null", TypeCharDevice)
	assertType(t, m, "dev/disk0", TypeBlockDevice)
	assertType(t, m, "run/pipe", TypeFifo)
	assertType(t, m, "run/link", TypeSymlink)

	link, ok, err := m.Lookup("run/link")
	if err != nil || !ok {
		t.Fatalf("lookup symlink: ok=%v err=%v", ok, err)
	}
	if link.Linkname != "../dev/null" {
		t.Fatalf("symlink target = %q", link.Linkname)
	}

	err = m.ApplyLayer(buildTar(t, []tarEntry{{name: "weird", kind: tar.TypeXHeader + 10}}))
	if err == nil || !strings.Contains(err.Error(), "unsupported tar type flag") {
		t.Fatalf("want unsupported type error, got %v", err)
	}
	if m.LayerCount() != 1 {
		t.Fatalf("layer count = %d, want 1", m.LayerCount())
	}
}

func TestReplacedDirectoryDoesNotResurrectChildren(t *testing.T) {
	m := New()
	applyLayers(t, m,
		[]tarEntry{
			{name: "swap/", kind: tar.TypeDir},
			{name: "swap/child", kind: tar.TypeReg},
		},
		// A regular file replaces the lower-layer directory.
		[]tarEntry{{name: "swap", kind: tar.TypeReg}},
	)

	assertType(t, m, "swap", TypeRegular)
	assertAbsent(t, m, "swap/child")

	// A later layer turns it back into a directory; no stale children return.
	applyLayers(t, m, []tarEntry{{name: "swap/", kind: tar.TypeDir}})
	assertType(t, m, "swap", TypeDir)
	assertAbsent(t, m, "swap/child")
}

func TestOpaqueMarkerProtectionReport(t *testing.T) {
	m := New()
	if err := m.Protect("vol/data/secret"); err != nil {
		t.Fatalf("protect: %v", err)
	}
	applyLayers(t, m, []tarEntry{
		{name: "vol/data/secret", kind: tar.TypeReg},
	})

	err := m.ApplyLayer(buildTar(t, []tarEntry{
		whiteout("vol/data/.wh..wh..opq"),
	}))
	var protectedErr *ProtectedPathError
	if !errors.As(err, &protectedErr) {
		t.Fatalf("want *ProtectedPathError, got %v", err)
	}
	if protectedErr.Marker != "vol/data/.wh..wh..opq" ||
		protectedErr.Removed != "vol/data/secret" ||
		protectedErr.Protected != "vol/data/secret" {
		t.Fatalf("unexpected error detail: %+v", protectedErr)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestApplyLayerReaderError(t *testing.T) {
	m := New()
	if err := m.ApplyLayer(failingReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want wrapped read error, got %v", err)
	}
	if m.LayerCount() != 0 || len(m.View()) != 0 {
		t.Fatal("failed read must not mutate merger state")
	}

	if err := m.Protect(""); err == nil {
		t.Fatal("expected error on empty protected path")
	}
}

func TestRootAndDuplicateHandling(t *testing.T) {
	m := New()
	applyLayers(t, m,
		[]tarEntry{
			{name: "./", kind: tar.TypeDir},
			{name: "./a", kind: tar.TypeReg, mode: 0o644},
		},
		[]tarEntry{
			// Duplicate path within one tar: the last header wins.
			{name: "a", kind: tar.TypeReg, mode: 0o600},
			{name: "a", kind: tar.TypeSymlink, linkname: "b"},
		},
	)

	if _, ok, err := m.Lookup("."); err != nil || ok {
		t.Fatalf("root entry must not be stored: ok=%v err=%v", ok, err)
	}
	entry, ok, err := m.Lookup("a")
	if err != nil || !ok {
		t.Fatalf("lookup a: ok=%v err=%v", ok, err)
	}
	if entry.Type != TypeSymlink || entry.Layer != 1 {
		t.Fatalf("last same-layer entry must win: %+v", entry)
	}
}
