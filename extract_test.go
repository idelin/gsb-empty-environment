package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	files := map[string][]byte{
		"top.txt":     []byte("top"),
		"a/b/c/deep":  []byte("deep"),
		"a/other.txt": []byte("other"),
	}
	blob, err := PackBytes(files)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := Decode(bytesReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(tmp, "out")
	if err := Extract(entries, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	// No staging leftovers next to dest.
	matches, _ := filepath.Glob(filepath.Join(tmp, ".sar-stage-*"))
	if len(matches) != 0 {
		t.Fatalf("staging dirs left behind: %v", matches)
	}
}

func TestExtractFailureLeavesNoPartialOutput(t *testing.T) {
	tmp := t.TempDir()
	// Second entry has a traversal path: Decode would reject it, so build
	// the entry list directly to exercise Extract's own defenses.
	good := mustEntry(t, "ok.txt", "ok")
	evil := mustEntry(t, "../escape.txt", "boom")
	dest := filepath.Join(tmp, "out")
	if err := Extract([]Entry{good, evil}, dest); err == nil {
		t.Fatal("Extract accepted traversal entry")
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("partial output exists at %q", dest)
	}
	matches, _ := filepath.Glob(filepath.Join(tmp, ".sar-stage-*"))
	if len(matches) != 0 {
		t.Fatalf("staging dirs left behind: %v", matches)
	}
	// Nothing may have escaped into the parent either.
	if _, err := os.Lstat(filepath.Join(tmp, "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("traversal entry escaped staging root")
	}
}

func TestExtractRefusesExistingDest(t *testing.T) {
	tmp := t.TempDir()
	entries := []Entry{mustEntry(t, "a.txt", "a")}

	existing := filepath.Join(tmp, "dir")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Extract(entries, existing); err == nil {
		t.Fatal("Extract overwrote existing directory")
	}

	link := filepath.Join(tmp, "link")
	if err := os.Symlink(tmp, link); err != nil {
		t.Fatal(err)
	}
	if err := Extract(entries, link); err == nil {
		t.Fatal("Extract published onto a symlink")
	}
}

func TestExtractRejectsSymlinkParent(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	linkParent := filepath.Join(tmp, "linkparent")
	if err := os.Symlink(real, linkParent); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{mustEntry(t, "a.txt", "a")}
	if err := Extract(entries, filepath.Join(linkParent, "out")); err == nil {
		t.Fatal("Extract accepted symlink destination parent")
	}
}

func TestSecureJoin(t *testing.T) {
	base := t.TempDir()
	ok, err := secureJoin(base, "a/b/c.txt")
	if err != nil {
		t.Fatalf("secureJoin valid path: %v", err)
	}
	if rel, _ := filepath.Rel(base, ok); rel != filepath.Join("a", "b", "c.txt") {
		t.Fatalf("secureJoin returned %q", ok)
	}
	for _, bad := range []string{"../x", "/abs", "..", "a/../../b", "a\\b"} {
		if _, err := secureJoin(base, bad); err == nil {
			t.Errorf("secureJoin(%q) accepted", bad)
		}
	}

	// A symlink inside the tree must be rejected as a path component.
	inside := filepath.Join(base, "sub")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(inside, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := secureJoin(base, "sub/link/evil.txt"); err == nil {
		t.Fatal("secureJoin followed symlink component")
	}
}

func TestExtractDuplicatePathsRejected(t *testing.T) {
	tmp := t.TempDir()
	entries := []Entry{mustEntry(t, "a.txt", "1"), mustEntry(t, "a.txt", "2")}
	if err := Extract(entries, filepath.Join(tmp, "out")); err == nil {
		t.Fatal("Extract accepted duplicate paths")
	}
}
