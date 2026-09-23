package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"testing"
)

func mustEntry(t *testing.T, name, content string) Entry {
	t.Helper()
	c := []byte(content)
	return Entry{Path: name, Size: uint64(len(c)), Digest: sha256.Sum256(c), Content: c}
}

func TestRoundTrip(t *testing.T) {
	in := []Entry{
		mustEntry(t, "hello.txt", "hello world"),
		mustEntry(t, "dir/sub/note.md", "# note"),
		mustEntry(t, "empty.bin", ""),
	}
	var buf bytes.Buffer
	if err := Encode(&buf, in); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := Decode(&buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("got %d entries, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i].Path != in[i].Path || !bytes.Equal(out[i].Content, in[i].Content) || out[i].Digest != in[i].Digest {
			t.Fatalf("entry %d mismatch: %+v vs %+v", i, out[i], in[i])
		}
	}
}

func TestNormalizePathRejects(t *testing.T) {
	bad := []string{
		"", ".", "..", "../x", "a/../../b", "/etc/passwd", "/abs",
		"a\\b", "nul\x00byte", string([]byte{0xff, 0xfe}),
		strings.Repeat("a", MaxPathLen+1),
	}
	for _, p := range bad {
		if _, err := NormalizePath(p); err == nil {
			t.Errorf("NormalizePath(%q) accepted, want error", p)
		}
	}
	good := map[string]string{
		"a.txt":       "a.txt",
		"./a.txt":     "a.txt",
		"a//b/./c":    "a/b/c",
		"a/../b.txt":  "b.txt", // cleans inside root, never escapes
		"dir/file.md": "dir/file.md",
	}
	for in, want := range good {
		got, err := NormalizePath(in)
		if err != nil {
			t.Errorf("NormalizePath(%q) error: %v", in, err)
		} else if got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEncodeRejectsDuplicateAndBadPaths(t *testing.T) {
	dup := []Entry{mustEntry(t, "a.txt", "1"), mustEntry(t, "./a.txt", "2")}
	if err := Encode(&bytes.Buffer{}, dup); err == nil {
		t.Fatal("Encode accepted duplicate normalized paths")
	}
	trav := []Entry{mustEntry(t, "../evil", "x")}
	if err := Encode(&bytes.Buffer{}, trav); err == nil {
		t.Fatal("Encode accepted traversal path")
	}
}

// buildRawArchive hand-crafts an archive body after the magic, for feeding
// malformed inputs to Decode.
func buildRawArchive(t *testing.T, count uint32, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write(magic[:])
	binary.Write(&buf, binary.LittleEndian, count)
	buf.Write(body)
	return buf.Bytes()
}

func rawEntry(t *testing.T, path string, size uint64, digest []byte, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(len(path)))
	buf.WriteString(path)
	binary.Write(&buf, binary.LittleEndian, size)
	buf.Write(digest)
	buf.Write(content)
	return buf.Bytes()
}

func TestDecodeRejectsTraversalAndAbsolute(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	for _, p := range []string{"../evil", "/etc/passwd", "a/../../b", "..", "a\\b"} {
		body := rawEntry(t, p, 1, sum[:], []byte("x"))
		if _, err := Decode(bytes.NewReader(buildRawArchive(t, 1, body))); err == nil {
			t.Errorf("Decode accepted malicious path %q", p)
		}
	}
}

func TestDecodeRejectsDuplicatePaths(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	e := rawEntry(t, "same.txt", 1, sum[:], []byte("x"))
	body := append(append([]byte{}, e...), e...)
	if _, err := Decode(bytes.NewReader(buildRawArchive(t, 2, body))); err == nil {
		t.Fatal("Decode accepted duplicate paths")
	}
}

func TestDecodeRejectsDigestMismatch(t *testing.T) {
	var zero [sha256.Size]byte
	body := rawEntry(t, "a.txt", 3, zero[:], []byte("abc"))
	if _, err := Decode(bytes.NewReader(buildRawArchive(t, 1, body))); err == nil {
		t.Fatal("Decode accepted wrong digest")
	}
}

func TestDecodeRejectsSizeMismatch(t *testing.T) {
	sum := sha256.Sum256([]byte("abc"))
	// Declared size larger than actual content -> truncated read must fail.
	body := rawEntry(t, "a.txt", 10, sum[:], []byte("abc"))
	if _, err := Decode(bytes.NewReader(buildRawArchive(t, 1, body))); err == nil {
		t.Fatal("Decode accepted truncated content")
	}
	// Declared size smaller than digest/content -> digest check must fail.
	body = rawEntry(t, "a.txt", 1, sum[:], []byte("abc"))
	if _, err := Decode(bytes.NewReader(buildRawArchive(t, 1, body))); err == nil {
		t.Fatal("Decode accepted size/digest inconsistency")
	}
}

func TestDecodeValidatesLengthBeforeAlloc(t *testing.T) {
	// Absurd entry count with empty body: must fail fast on the cap, not
	// by trying to allocate or read 4 billion entries.
	if _, err := Decode(bytes.NewReader(buildRawArchive(t, 1<<31, nil))); err == nil {
		t.Fatal("Decode accepted entry count beyond cap")
	}
	// pathLen = 0 and pathLen > MaxPathLen must be rejected before alloc.
	for _, pl := range []uint16{0, MaxPathLen + 1, 0xffff} {
		var body bytes.Buffer
		binary.Write(&body, binary.LittleEndian, pl)
		if _, err := Decode(bytes.NewReader(buildRawArchive(t, 1, body.Bytes()))); err == nil {
			t.Errorf("Decode accepted pathLen %d", pl)
		}
	}
	// Declared size beyond cap must be rejected before allocation.
	sum := sha256.Sum256(nil)
	body := rawEntry(t, "big.bin", MaxFileSize+1, sum[:], nil)
	if _, err := Decode(bytes.NewReader(buildRawArchive(t, 1, body))); err == nil {
		t.Fatal("Decode accepted oversized file")
	}
}

func TestDecodeRejectsBadMagicAndTruncation(t *testing.T) {
	if _, err := Decode(bytes.NewReader([]byte("not-an-archive"))); err == nil {
		t.Fatal("Decode accepted bad magic")
	}
	good, err := PackBytes(map[string][]byte{"a.txt": []byte("data")})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{4, 9, len(good) / 2, len(good) - 1} {
		if _, err := Decode(bytes.NewReader(good[:n])); err == nil {
			t.Errorf("Decode accepted archive truncated to %d bytes", n)
		}
	}
}
