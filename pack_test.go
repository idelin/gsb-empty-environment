package pkgincr

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pack runs Walk+Pack over root and returns the archive bytes and stats.
func pack(t *testing.T, root string, rules []Rule, prev *Manifest) ([]byte, *Manifest, PackStats) {
	t.Helper()
	res, err := Walk(root, rules)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	next, stats, err := Pack(root, res, &buf, prev)
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), next, stats
}

// readTar collects a tar archive into path -> (header, content).
func readTar(t *testing.T, data []byte) map[string]*tar.Header {
	t.Helper()
	out := map[string]*tar.Header{}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out[hdr.Name] = hdr
	}
}

func TestPackStoresSymlinkAsLink(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, map[string]string{
		"real/inner.txt": "i",
		"link":           "@real",
		"dangling":       "@no/such/target",
	})
	data, _, _ := pack(t, root, nil, nil)
	hdrs := readTar(t, data)

	hdr, ok := hdrs["link"]
	if !ok {
		t.Fatal("archive is missing the symlink entry")
	}
	if hdr.Typeflag != tar.TypeSymlink || hdr.Linkname != "real" {
		t.Errorf("link: typeflag=%q linkname=%q, want symlink -> real", hdr.Typeflag, hdr.Linkname)
	}
	if hdr.Size != 0 {
		t.Errorf("symlink entry has size %d, want 0 (only the link itself)", hdr.Size)
	}
	if _, ok := hdrs["dangling"]; !ok {
		t.Error("dangling symlink missing from archive")
	}
	// The target's content must appear only under its real path, never
	// through the link.
	if _, ok := hdrs["real/inner.txt"]; !ok {
		t.Error("real/inner.txt missing")
	}
	for name := range hdrs {
		if strings.HasPrefix(name, "link/") {
			t.Errorf("archive contains %q: symlink target was followed", name)
		}
	}
}

func TestPackRegularContents(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, map[string]string{
		"empty/":    "",
		"f.txt":     "hello",
		"sub/g.txt": "world",
	})
	data, _, _ := pack(t, root, nil, nil)
	hdrs := readTar(t, data)
	if hdr, ok := hdrs["empty/"]; !ok || hdr.Typeflag != tar.TypeDir {
		t.Errorf("empty dir entry missing or wrong type: %v", hdr)
	}
	if hdr := hdrs["f.txt"]; hdr == nil || hdr.Typeflag != tar.TypeReg || hdr.Size != 5 {
		t.Errorf("f.txt header wrong: %v", hdr)
	}
	// Verify file content round-trips.
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == "f.txt" {
			body, _ := io.ReadAll(tr)
			if string(body) != "hello" {
				t.Errorf("f.txt content = %q, want hello", body)
			}
		}
	}
}

func TestPackIncremental(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, map[string]string{
		"a.txt": "aaa",
		"b.txt": "bbb",
		"c.txt": "ccc",
	})
	// Pin modtimes so change detection is deterministic.
	stable := time.Unix(1_700_000_000, 0)
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.Chtimes(filepath.Join(root, f), stable, stable); err != nil {
			t.Fatal(err)
		}
	}

	_, m1, stats1 := pack(t, root, nil, nil)
	if stats1.Written != 3 || stats1.Unchanged != 0 {
		t.Fatalf("first run: %+v, want 3 written 0 unchanged", stats1)
	}

	// Second run, no changes: everything unchanged.
	data2, _, stats2 := pack(t, root, nil, m1)
	if stats2.Written != 0 || stats2.Unchanged != 3 {
		t.Fatalf("second run: %+v, want 0 written 3 unchanged", stats2)
	}
	if len(readTar(t, data2)) != 0 {
		t.Error("second archive should be empty")
	}

	// Modify one file, delete another, add a new one.
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("BBB2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(root, "b.txt"), stable.Add(time.Hour), stable.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "c.txt")); err != nil {
		t.Fatal(err)
	}
	makeTree(t, root, map[string]string{"d.txt": "ddd"})

	data3, m3, stats3 := pack(t, root, nil, m1)
	if stats3.Written != 2 || stats3.Unchanged != 1 {
		t.Fatalf("third run: %+v, want 2 written (b.txt, d.txt) 1 unchanged (a.txt)", stats3)
	}
	hdrs := readTar(t, data3)
	if _, ok := hdrs["b.txt"]; !ok {
		t.Error("modified b.txt missing from incremental archive")
	}
	if _, ok := hdrs["d.txt"]; !ok {
		t.Error("new d.txt missing from incremental archive")
	}
	if _, ok := hdrs["a.txt"]; ok {
		t.Error("unchanged a.txt should not be re-archived")
	}
	if len(stats3.Removed) != 1 || stats3.Removed[0] != "c.txt" {
		t.Errorf("removed = %v, want [c.txt]", stats3.Removed)
	}
	if _, ok := m3.Entries["c.txt"]; ok {
		t.Error("deleted c.txt still in manifest")
	}
	if len(m3.Entries) != 3 {
		t.Errorf("manifest has %d entries, want 3 (a, b, d)", len(m3.Entries))
	}
}

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "manifest.json")
	m := &Manifest{Entries: map[string]ManifestEntry{
		"a/b.txt": {Size: 3, ModTimeNS: 42, Mode: 0o644},
	}}
	if err := m.Save(name); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(name)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Entries["a/b.txt"] != m.Entries["a/b.txt"] {
		t.Errorf("round trip mismatch: %+v", loaded.Entries["a/b.txt"])
	}
	// Missing manifest is an empty one, not an error.
	empty, err := LoadManifest(filepath.Join(dir, "nope.json"))
	if err != nil || len(empty.Entries) != 0 {
		t.Errorf("missing manifest: %v, %v", empty, err)
	}
}
