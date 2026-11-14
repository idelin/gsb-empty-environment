package depverify

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHashZipMatchesHashDir(t *testing.T) {
	modulePath, version := "example.com/mod", "v1.0.0"
	files := map[string]string{
		"go.mod":   "module example.com/mod\n\ngo 1.24.0\n",
		"main.go":  "package mod\n\nfunc V() string { return \"1\" }\n",
		"sub/a.go": "package sub\n",
	}
	blob, err := buildBlob(modulePath, version, files, Source{})
	if err != nil {
		t.Fatal(err)
	}
	zipDigest, err := HashZip(bytes.NewReader(blob.zip), int64(len(blob.zip)))
	if err != nil {
		t.Fatalf("HashZip: %v", err)
	}

	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dirDigest, err := HashDir(dir, modulePath, version)
	if err != nil {
		t.Fatalf("HashDir: %v", err)
	}
	if zipDigest != dirDigest {
		t.Fatalf("digests differ:\nzip=%s\ndir=%s", zipDigest, dirDigest)
	}
	if !strings.HasPrefix(zipDigest, "h1:") || len(zipDigest) <= len("h1:") {
		t.Fatalf("digest not h1 encoded: %q", zipDigest)
	}
}

func TestHashIsContentSensitiveAndStable(t *testing.T) {
	mk := func(body string) string {
		blob, err := buildBlob("example.com/mod", "v1.0.0",
			map[string]string{"main.go": body}, Source{})
		if err != nil {
			t.Fatal(err)
		}
		d, err := HashZip(bytes.NewReader(blob.zip), int64(len(blob.zip)))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	base := mk("package mod\n")
	again := mk("package mod\n")
	tampered := mk("package mod // evil\n")
	if base != again {
		t.Fatal("identical content produced different digests")
	}
	if base == tampered {
		t.Fatal("changed content produced the same digest")
	}

	// Filename changes must also change the hash (names are part of Hash1).
	renamed, err := buildBlob("example.com/mod", "v1.0.0",
		map[string]string{"other.go": "package mod\n"}, Source{})
	if err != nil {
		t.Fatal(err)
	}
	renamedDigest, err := HashZip(bytes.NewReader(renamed.zip), int64(len(renamed.zip)))
	if err != nil {
		t.Fatal(err)
	}
	if renamedDigest == base {
		t.Fatal("renamed file did not change digest")
	}
}

func TestHash1Vector(t *testing.T) {
	// Lock the encoding to the x/mod/dirhash layout:
	// h1:base64(sha256("name  hex(sha256(content))\n")).
	content := []byte("hello module")
	sum := sha256.Sum256(content)
	inner := "data.txt  " + hex.EncodeToString(sum[:]) + "\n"
	outer := sha256.Sum256([]byte(inner))
	want := "h1:" + base64.StdEncoding.EncodeToString(outer[:])

	got, err := Hash1([]string{"data.txt"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(content)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Hash1 = %s, want %s", got, want)
	}
}
