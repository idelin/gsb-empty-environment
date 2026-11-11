package manifestbind

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"iter"
	"slices"
	"testing"
)

type testKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newKey(t *testing.T) testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{pub: pub, priv: priv}
}

func signedManifest(t *testing.T, key testKey, files map[string][]byte) (*Manifest, []byte) {
	t.Helper()
	entries := make([]ManifestEntry, 0, len(files))
	for path, data := range files {
		entries = append(entries, ManifestEntry{
			Path:   path,
			Size:   int64(len(data)),
			SHA256: sha256.Sum256(data),
		})
	}
	m, err := NewManifest(entries)
	if err != nil {
		t.Fatal(err)
	}
	return m, m.Sign(key.priv)
}

func seqOf(entries ...Entry) iter.Seq2[Entry, error] {
	return func(yield func(Entry, error) bool) {
		for _, e := range entries {
			if !yield(e, nil) {
				return
			}
		}
	}
}

// gzEntry simulates an archive member whose stored bytes are
// gzip-compressed; the entry stream yields the decompressed form.
func gzEntry(t *testing.T, path string, plain []byte) Entry {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	stored := buf.Bytes()
	return Entry{
		Path: path,
		Open: func() (io.ReadCloser, error) {
			return gzip.NewReader(bytes.NewReader(stored))
		},
	}
}

func assertNotInstalled(t *testing.T, fsys *MemFS, dest string) {
	t.Helper()
	for _, name := range []string{dest, dest + ".staging"} {
		if _, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("rollback incomplete: %q still present (err=%v)", name, err)
		}
	}
}

func TestInstallSuccess(t *testing.T) {
	key := newKey(t)
	files := map[string][]byte{
		"plugin/main.lua":   []byte("print('hello')"),
		"plugin/data/a.bin": bytes.Repeat([]byte{0xAB}, 4096),
		"README":            []byte("readme text"),
	}
	manifest, sig := signedManifest(t, key, files)

	fsys := NewMemFS()
	entries := []Entry{
		gzEntry(t, "plugin/main.lua", files["plugin/main.lua"]),
		gzEntry(t, "plugin/data/a.bin", files["plugin/data/a.bin"]),
		gzEntry(t, "README", files["README"]),
	}
	if err := Install(fsys, "dest", seqOf(entries...), manifest, sig, key.pub); err != nil {
		t.Fatal(err)
	}
	for path, want := range files {
		got, err := fsys.ReadFile("dest/" + path)
		if err != nil {
			t.Fatalf("installed file %q: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("installed file %q: content mismatch", path)
		}
	}
	if _, err := fsys.Stat("dest.staging"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("staging directory left behind after publish")
	}
}

func TestRollbackOnDuplicatePath(t *testing.T) {
	key := newKey(t)
	good := []byte("good bytes")
	manifest, sig := signedManifest(t, key, map[string][]byte{"a.txt": good})

	fsys := NewMemFS()
	// The archive stream repeats a path; installer and signer must not
	// disagree about which copy wins.
	entries := []Entry{
		BytesEntry("a.txt", good),
		BytesEntry("a.txt", []byte("evil overwrite")),
	}
	err := Install(fsys, "dest", seqOf(entries...), manifest, sig, key.pub)
	if !errors.Is(err, ErrDuplicatePath) {
		t.Fatalf("want ErrDuplicatePath, got %v", err)
	}
	assertNotInstalled(t, fsys, "dest")
}

func TestRollbackOnUnlistedEntry(t *testing.T) {
	key := newKey(t)
	listed := []byte("listed")
	manifest, sig := signedManifest(t, key, map[string][]byte{"listed.txt": listed})

	fsys := NewMemFS()
	entries := []Entry{
		BytesEntry("listed.txt", listed),
		BytesEntry("hidden/payload.bin", []byte("not in manifest")),
	}
	err := Install(fsys, "dest", seqOf(entries...), manifest, sig, key.pub)
	if !errors.Is(err, ErrUnlistedEntry) {
		t.Fatalf("want ErrUnlistedEntry, got %v", err)
	}
	assertNotInstalled(t, fsys, "dest")
}

func TestRollbackOnCorruptedLastEntry(t *testing.T) {
	key := newKey(t)
	files := map[string][]byte{
		"first.txt":  []byte("first is fine"),
		"second.txt": []byte("second is fine"),
		"last.txt":   []byte("last is corrupted"),
	}
	manifest, sig := signedManifest(t, key, files)

	fsys := NewMemFS()
	entries := []Entry{
		BytesEntry("first.txt", files["first.txt"]),
		BytesEntry("second.txt", files["second.txt"]),
		BytesEntry("last.txt", []byte("tampered content")),
	}
	err := Install(fsys, "dest", seqOf(entries...), manifest, sig, key.pub)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
	// Even though the first two entries verified, nothing is published.
	assertNotInstalled(t, fsys, "dest")
}

func TestRollbackOnStreamError(t *testing.T) {
	key := newKey(t)
	files := map[string][]byte{"a.txt": []byte("a"), "b.txt": []byte("b")}
	manifest, sig := signedManifest(t, key, files)

	boom := errors.New("archive read failed")
	entries := func(yield func(Entry, error) bool) {
		if !yield(BytesEntry("a.txt", files["a.txt"]), nil) {
			return
		}
		yield(Entry{}, boom)
	}
	fsys := NewMemFS()
	err := Install(fsys, "dest", entries, manifest, sig, key.pub)
	if !errors.Is(err, boom) {
		t.Fatalf("want stream error, got %v", err)
	}
	assertNotInstalled(t, fsys, "dest")
}

func TestRejectBadSignature(t *testing.T) {
	key := newKey(t)
	other := newKey(t)
	files := map[string][]byte{"a.txt": []byte("a")}
	manifest, _ := signedManifest(t, key, files)
	forged := manifest.Sign(other.priv)

	fsys := NewMemFS()
	err := Install(fsys, "dest", seqOf(BytesEntry("a.txt", files["a.txt"])),
		manifest, forged, key.pub)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
	assertNotInstalled(t, fsys, "dest")
}

func TestRejectMissingEntry(t *testing.T) {
	key := newKey(t)
	files := map[string][]byte{"a.txt": []byte("a"), "b.txt": []byte("b")}
	manifest, sig := signedManifest(t, key, files)

	fsys := NewMemFS()
	err := Install(fsys, "dest", seqOf(BytesEntry("a.txt", files["a.txt"])),
		manifest, sig, key.pub)
	if !errors.Is(err, ErrMissingEntry) {
		t.Fatalf("want ErrMissingEntry, got %v", err)
	}
	assertNotInstalled(t, fsys, "dest")
}

func TestRejectExistingDestination(t *testing.T) {
	key := newKey(t)
	files := map[string][]byte{"a.txt": []byte("a")}
	manifest, sig := signedManifest(t, key, files)

	fsys := NewMemFS()
	if err := fsys.MkdirAll("dest"); err != nil {
		t.Fatal(err)
	}
	err := Install(fsys, "dest", seqOf(BytesEntry("a.txt", files["a.txt"])),
		manifest, sig, key.pub)
	if err == nil {
		t.Fatal("expected error for existing destination")
	}
}

func TestPathValidation(t *testing.T) {
	cases := []struct {
		path string
		want error
	}{
		{"", ErrEmptySegment},
		{"/abs.txt", ErrEmptySegment},
		{"trailing/", ErrEmptySegment},
		{"double//slash.txt", ErrEmptySegment},
		{"./rel.txt", ErrDotSegment},
		{"../escape.txt", ErrDotSegment},
		{"a/./b.txt", ErrDotSegment},
		{"a/../b.txt", ErrDotSegment},
		{`win\style.txt`, ErrBackslash},
		{`C:\temp\x`, ErrBackslash},
	}
	for _, tc := range cases {
		if err := ValidatePath(tc.path); !errors.Is(err, tc.want) {
			t.Errorf("ValidatePath(%q) = %v, want %v", tc.path, err, tc.want)
		}
	}
	for _, ok := range []string{"a.txt", "dir/sub/file.bin", "a.b/c-d/e_f"} {
		if err := ValidatePath(ok); err != nil {
			t.Errorf("ValidatePath(%q) = %v, want nil", ok, err)
		}
	}
}

func TestPathConflicts(t *testing.T) {
	key := newKey(t)
	files := map[string][]byte{"a": []byte("x"), "a/b.txt": []byte("y")}

	// Manifest construction itself must reject the file/dir conflict.
	entries := make([]ManifestEntry, 0, len(files))
	for path, data := range files {
		entries = append(entries, ManifestEntry{
			Path: path, Size: int64(len(data)), SHA256: sha256.Sum256(data),
		})
	}
	if _, err := NewManifest(entries); !errors.Is(err, ErrPathConflict) {
		t.Fatalf("NewManifest: want ErrPathConflict, got %v", err)
	}

	// A conflict introduced by the archive stream is rejected too.
	good := map[string][]byte{"ok.txt": []byte("ok")}
	manifest, sig := signedManifest(t, key, good)
	fsys := NewMemFS()
	err := Install(fsys, "dest", seqOf(
		BytesEntry("ok.txt", good["ok.txt"]),
		BytesEntry("ok.txt/evil", []byte("conflict")),
	), manifest, sig, key.pub)
	if !errors.Is(err, ErrPathConflict) {
		t.Fatalf("Install: want ErrPathConflict, got %v", err)
	}
	assertNotInstalled(t, fsys, "dest")
}

func TestManifestSortedDeterministic(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	m1, err := NewManifest([]ManifestEntry{
		{Path: "b", Size: 1, SHA256: sum},
		{Path: "a", Size: 1, SHA256: sum},
	})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := NewManifest([]ManifestEntry{
		{Path: "a", Size: 1, SHA256: sum},
		{Path: "b", Size: 1, SHA256: sum},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := newKey(t)
	if !slices.Equal(m1.Sign(key.priv), m1.Sign(key.priv)) {
		t.Fatal("signing is not deterministic")
	}
	if !bytes.Equal(m1.canonical(), m2.canonical()) {
		t.Fatal("canonical encoding depends on input order")
	}
}
