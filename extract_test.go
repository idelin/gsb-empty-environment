package netsec_test

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	netsec "example.org/netsec/q0338"
)

type tarEntry struct {
	name string
	body []byte
}

func tarBytes(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name:     e.name,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(e.body)),
		}); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatalf("write body %q: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	return buf.Bytes()
}

// nestArchive builds an archive nested depth layers deep: each layer holds a
// single "layer.tar" member, and the innermost layer holds payload.txt.
func nestArchive(t *testing.T, depth int) []byte {
	t.Helper()
	data := tarBytes(t, tarEntry{name: "payload.txt", body: []byte("payload")})
	for i := 1; i < depth; i++ {
		data = tarBytes(t, tarEntry{name: "layer.tar", body: data})
	}
	return data
}

func TestExtractSingleLayer(t *testing.T) {
	archive := tarBytes(t,
		tarEntry{name: "hello.txt", body: []byte("hello")},
		tarEntry{name: "sub/world.txt", body: []byte("world")},
	)
	dir := t.TempDir()
	if err := netsec.Extract(bytes.NewReader(archive), dir, 1); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for name, want := range map[string]string{
		"hello.txt":     "hello",
		"sub/world.txt": "world",
	} {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestExtractNestedWithinLimit(t *testing.T) {
	dir := t.TempDir()
	if err := netsec.Extract(bytes.NewReader(nestArchive(t, 3)), dir, 3); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "layer", "layer", "payload.txt"))
	if err != nil {
		t.Fatalf("read nested payload: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("payload = %q, want %q", got, "payload")
	}
}

func TestExtractNestedExceedsLimit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		depth    int
		maxDepth int
	}{
		{"one over", 2, 1},
		{"three over two", 3, 2},
		{"deeply nested", 8, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			err := netsec.Extract(bytes.NewReader(nestArchive(t, tc.depth)), dir, tc.maxDepth)
			if !errors.Is(err, netsec.ErrNestingTooDeep) {
				t.Fatalf("Extract err = %v, want ErrNestingTooDeep", err)
			}
		})
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../evil.txt", "a/../../evil.txt", "/abs/evil.txt"} {
		archive := tarBytes(t, tarEntry{name: name, body: []byte("evil")})
		dir := t.TempDir()
		err := netsec.Extract(bytes.NewReader(archive), dir, 1)
		if !errors.Is(err, netsec.ErrUnsafePath) {
			t.Errorf("entry %q: Extract err = %v, want ErrUnsafePath", name, err)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "..", "evil.txt")); statErr == nil {
			t.Errorf("entry %q: file escaped extraction dir", name)
		}
	}
}

func TestExtractNestedLayerStillChecksPaths(t *testing.T) {
	inner := tarBytes(t, tarEntry{name: "../evil.txt", body: []byte("evil")})
	outer := tarBytes(t, tarEntry{name: "layer.tar", body: inner})
	dir := t.TempDir()
	err := netsec.Extract(bytes.NewReader(outer), dir, 2)
	if !errors.Is(err, netsec.ErrUnsafePath) {
		t.Fatalf("Extract err = %v, want ErrUnsafePath", err)
	}
}

func TestExtractRejectsSymlink(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "link",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
	}); err != nil {
		t.Fatalf("write symlink header: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	dir := t.TempDir()
	err := netsec.Extract(bytes.NewReader(buf.Bytes()), dir, 1)
	if !errors.Is(err, netsec.ErrUnsupportedEntry) {
		t.Fatalf("Extract err = %v, want ErrUnsupportedEntry", err)
	}
}
