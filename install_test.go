package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"testing"
)

type testFile struct {
	path    string
	content []byte
}

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// buildManifest encodes files (which must have canonical, unique paths) into
// the manifest wire format, sorted by path byte order.
func buildManifest(t *testing.T, files []testFile) []byte {
	t.Helper()
	sorted := append([]testFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].path < sorted[j].path })
	var out []byte
	for _, f := range sorted {
		sum := sha256.Sum256(f.content)
		out = append(out, []byte(strconv.Itoa(len(f.path))+":"+f.path)...)
		out = append(out, sum[:]...)
	}
	return out
}

func makeEntries(files []testFile) []Entry {
	entries := make([]Entry, 0, len(files))
	for _, f := range files {
		entries = append(entries, Entry{
			Path:           f.path,
			DeclaredSHA256: sha256.Sum256(f.content),
			Content:        bytes.NewReader(f.content),
		})
	}
	return entries
}

// chunkReader yields content one byte at a time to exercise chunked reads.
type chunkReader struct {
	data []byte
	pos  int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	p[0] = r.data[r.pos]
	r.pos++
	return 1, nil
}

// collector records install callbacks.
type collector struct {
	order  []string
	files  map[string][]byte
	called bool
}

func (c *collector) install(path string, content io.Reader) error {
	c.called = true
	data, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	if c.files == nil {
		c.files = map[string][]byte{}
	}
	c.order = append(c.order, path)
	c.files[path] = data
	return nil
}

var noLimits = Limits{}

func TestValidSignatureEndToEnd(t *testing.T) {
	pub, priv := testKey(t)
	files := []testFile{
		{path: "b/two.txt", content: []byte("second")},
		{path: "a/one.txt", content: []byte("first")},
		{path: "empty.txt", content: []byte{}},
	}
	manifest := buildManifest(t, files)
	sig := ed25519.Sign(priv, manifest)

	entries := makeEntries(files)
	// Feed one file through a 1-byte-chunk reader to prove chunked reads work.
	entries[0].Content = &chunkReader{data: files[0].content}
	// Raw paths need not be canonical; normalization must map them back.
	entries[1].Path = "./a//one.txt"

	c := &collector{}
	err := VerifyAndInstall(entries, manifest, sig, pub,
		Limits{MaxFileBytes: 1 << 20, MaxTotalBytes: 4 << 20, MaxEntries: 16}, c.install)
	if err != nil {
		t.Fatalf("VerifyAndInstall: %v", err)
	}
	wantOrder := []string{"a/one.txt", "b/two.txt", "empty.txt"}
	if fmt.Sprint(c.order) != fmt.Sprint(wantOrder) {
		t.Fatalf("install order = %v, want %v", c.order, wantOrder)
	}
	for _, f := range files {
		if !bytes.Equal(c.files[f.path], f.content) {
			t.Fatalf("installed content of %s = %q, want %q", f.path, c.files[f.path], f.content)
		}
	}
}

func TestDuplicateNameOverwriteRejected(t *testing.T) {
	pub, priv := testKey(t)
	// Attacker ships a benign entry first and a malicious twin second, hoping
	// the later entry silently overwrites the earlier one.
	manifest := buildManifest(t, []testFile{{path: "a.txt", content: []byte("legit")}})
	sig := ed25519.Sign(priv, manifest)

	entries := makeEntries([]testFile{
		{path: "a.txt", content: []byte("legit")},
		{path: "a.txt", content: []byte("evil")},
	})
	c := &collector{}
	err := VerifyAndInstall(entries, manifest, sig, pub, noLimits, c.install)
	if !errors.Is(err, ErrDuplicatePath) {
		t.Fatalf("err = %v, want ErrDuplicatePath", err)
	}
	if c.called {
		t.Fatal("install callback fired for a rejected package")
	}
}

func TestNormalizationCollisionRejected(t *testing.T) {
	pub, priv := testKey(t)
	cases := [][]string{
		{"dir/file.txt", "dir/./file.txt"},
		{"dir/file.txt", "./dir//file.txt"},
		{"dir/file.txt", "dir/sub/../file.txt"},
		{"dir/file.txt", "dir///./file.txt"},
	}
	for _, paths := range cases {
		manifest := buildManifest(t, []testFile{{path: "dir/file.txt", content: []byte("x")}})
		sig := ed25519.Sign(priv, manifest)
		entries := makeEntries([]testFile{
			{path: paths[0], content: []byte("x")},
			{path: paths[1], content: []byte("y")},
		})
		c := &collector{}
		err := VerifyAndInstall(entries, manifest, sig, pub, noLimits, c.install)
		if !errors.Is(err, ErrDuplicatePath) {
			t.Fatalf("paths %v: err = %v, want ErrDuplicatePath", paths, err)
		}
		if c.called {
			t.Fatalf("paths %v: install callback fired for a rejected package", paths)
		}
	}
}

func TestZeroByteFile(t *testing.T) {
	pub, priv := testKey(t)
	files := []testFile{{path: "empty.bin", content: []byte{}}}
	manifest := buildManifest(t, files)
	sig := ed25519.Sign(priv, manifest)

	c := &collector{}
	err := VerifyAndInstall(makeEntries(files), manifest, sig, pub, noLimits, c.install)
	if err != nil {
		t.Fatalf("VerifyAndInstall: %v", err)
	}
	data, ok := c.files["empty.bin"]
	if !ok {
		t.Fatal("zero-byte file was not installed")
	}
	if len(data) != 0 {
		t.Fatalf("installed %d bytes, want 0", len(data))
	}
}

func TestTruncatedStreamRejected(t *testing.T) {
	pub, priv := testKey(t)
	full := []byte("the complete file content")
	manifest := buildManifest(t, []testFile{{path: "f.bin", content: full}})
	sig := ed25519.Sign(priv, manifest)

	// The declared digest matches the full content, but the stream is cut short.
	entries := []Entry{{
		Path:           "f.bin",
		DeclaredSHA256: sha256.Sum256(full),
		Content:        bytes.NewReader(full[:10]),
	}}
	c := &collector{}
	err := VerifyAndInstall(entries, manifest, sig, pub, noLimits, c.install)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
	if c.called {
		t.Fatal("install callback fired for a rejected package")
	}
}

func TestBadSignatureRejected(t *testing.T) {
	pub, priv := testKey(t)
	files := []testFile{{path: "a.txt", content: []byte("data")}}
	manifest := buildManifest(t, files)
	sig := ed25519.Sign(priv, manifest)
	sig[0] ^= 0xff // corrupt the signature

	c := &collector{}
	err := VerifyAndInstall(makeEntries(files), manifest, sig, pub, noLimits, c.install)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
	if c.called {
		t.Fatal("install callback fired for a rejected package")
	}
}

func TestExtraAndMissingEntriesRejected(t *testing.T) {
	pub, priv := testKey(t)
	manifest := buildManifest(t, []testFile{{path: "listed.txt", content: []byte("x")}})
	sig := ed25519.Sign(priv, manifest)

	// Extra entry not covered by the manifest.
	c := &collector{}
	err := VerifyAndInstall(makeEntries([]testFile{
		{path: "listed.txt", content: []byte("x")},
		{path: "smuggled.txt", content: []byte("y")},
	}), manifest, sig, pub, noLimits, c.install)
	if !errors.Is(err, ErrExtraEntry) {
		t.Fatalf("extra: err = %v, want ErrExtraEntry", err)
	}
	if c.called {
		t.Fatal("install callback fired for a rejected package")
	}

	// Manifest entry missing from the package.
	c = &collector{}
	err = VerifyAndInstall(nil, manifest, sig, pub, noLimits, c.install)
	if !errors.Is(err, ErrExtraEntry) && !errors.Is(err, ErrMissingEntry) {
		t.Fatalf("missing: err = %v, want ErrExtraEntry/ErrMissingEntry", err)
	}
	if c.called {
		t.Fatal("install callback fired for a rejected package")
	}
}

func TestManifestTamperingRejected(t *testing.T) {
	pub, priv := testKey(t)
	files := []testFile{{path: "a.txt", content: []byte("data")}}
	manifest := buildManifest(t, files)

	// Digest in the manifest does not match the shipped content.
	tampered := buildManifest(t, []testFile{{path: "a.txt", content: []byte("other")}})
	tamperedSig := ed25519.Sign(priv, tampered)
	c := &collector{}
	err := VerifyAndInstall(makeEntries(files), tampered, tamperedSig, pub, noLimits, c.install)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
	if c.called {
		t.Fatal("install callback fired for a rejected package")
	}

	// Duplicate paths inside the manifest itself.
	dup := append(append([]byte{}, manifest...), manifest...)
	dupSig := ed25519.Sign(priv, dup)
	err = VerifyAndInstall(makeEntries(files), dup, dupSig, pub, noLimits, func(string, io.Reader) error {
		t.Fatal("install callback fired for a rejected package")
		return nil
	})
	if !errors.Is(err, ErrManifestOrder) {
		t.Fatalf("err = %v, want ErrManifestOrder", err)
	}
}

func TestPathRulesEnforced(t *testing.T) {
	pub, priv := testKey(t)
	manifest := buildManifest(t, []testFile{{path: "ok.txt", content: []byte("x")}})
	sig := ed25519.Sign(priv, manifest)

	badPaths := []struct {
		path string
		want error
	}{
		{"../escape.txt", ErrPathEscapesRoot},
		{"a/../../escape.txt", ErrPathEscapesRoot},
		{"back\\slash.txt", ErrPathBackslash},
		{"nul\x00byte.txt", ErrPathNUL},
		{"bad\xffutf8.txt", ErrPathNotUTF8},
		{".", ErrPathEmpty},
		{"a/..", ErrPathEmpty},
	}
	for _, tc := range badPaths {
		entry := Entry{
			Path:           tc.path,
			DeclaredSHA256: sha256.Sum256([]byte("x")),
			Content:        bytes.NewReader([]byte("x")),
		}
		c := &collector{}
		err := VerifyAndInstall([]Entry{entry}, manifest, sig, pub, noLimits, c.install)
		if !errors.Is(err, tc.want) {
			t.Fatalf("path %q: err = %v, want %v", tc.path, err, tc.want)
		}
		if c.called {
			t.Fatalf("path %q: install callback fired for a rejected package", tc.path)
		}
	}
}

func TestLimitsEnforced(t *testing.T) {
	pub, priv := testKey(t)
	files := []testFile{
		{path: "a.txt", content: bytes.Repeat([]byte("a"), 100)},
		{path: "b.txt", content: bytes.Repeat([]byte("b"), 100)},
	}
	manifest := buildManifest(t, files)
	sig := ed25519.Sign(priv, manifest)

	// Per-file limit.
	c := &collector{}
	err := VerifyAndInstall(makeEntries(files), manifest, sig, pub,
		Limits{MaxFileBytes: 50}, c.install)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("err = %v, want ErrFileTooLarge", err)
	}
	if c.called {
		t.Fatal("install callback fired for a rejected package")
	}

	// Total limit.
	c = &collector{}
	err = VerifyAndInstall(makeEntries(files), manifest, sig, pub,
		Limits{MaxTotalBytes: 150}, c.install)
	if !errors.Is(err, ErrTotalTooLarge) {
		t.Fatalf("err = %v, want ErrTotalTooLarge", err)
	}
	if c.called {
		t.Fatal("install callback fired for a rejected package")
	}

	// Entry count limit.
	c = &collector{}
	err = VerifyAndInstall(makeEntries(files), manifest, sig, pub,
		Limits{MaxEntries: 1}, c.install)
	if !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("err = %v, want ErrTooManyEntries", err)
	}
	if c.called {
		t.Fatal("install callback fired for a rejected package")
	}
}

func TestNoUnicodeOrCaseFolding(t *testing.T) {
	// Paths differing only in case or Unicode normalization stay distinct and
	// do not collide.
	a, err := canonicalizePath("Dir/File.txt")
	if err != nil || a != "Dir/File.txt" {
		t.Fatalf("canonicalizePath case: %q, %v", a, err)
	}
	b, err := canonicalizePath("dir/file.txt")
	if err != nil || b != "dir/file.txt" {
		t.Fatalf("canonicalizePath case: %q, %v", b, err)
	}
	if a == b {
		t.Fatal("case folding must not occur")
	}
	// U+00C5 (Å, precomposed) vs U+0041 U+030A (A + combining ring).
	pre, err := canonicalizePath("\u00c5.txt")
	if err != nil {
		t.Fatal(err)
	}
	decomp, err := canonicalizePath("A\u030a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if pre == decomp {
		t.Fatal("Unicode normalization must not occur")
	}
}
