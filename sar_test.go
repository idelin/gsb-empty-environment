package safearch

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// --- raw archive builders (test-only) ---

type rawEntry struct {
	head   []byte // bytes covered by the overall digest
	digest []byte // per-entry digest trailer (nil for dirs)
}

func le16(n int) []byte {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], uint16(n))
	return b[:]
}

func le32(v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return b[:]
}

func le64(v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return b[:]
}

func rawFile(name string, mode uint32, data []byte) rawEntry {
	var b bytes.Buffer
	b.WriteByte(typFile)
	b.Write(le16(len(name)))
	b.WriteString(name)
	b.Write(le32(mode))
	b.Write(le64(uint64(len(data))))
	b.Write(data)
	sum := sha256.Sum256(data)
	return rawEntry{head: b.Bytes(), digest: sum[:]}
}

func rawFileSized(name string, mode uint32, declared uint64, data []byte) rawEntry {
	var b bytes.Buffer
	b.WriteByte(typFile)
	b.Write(le16(len(name)))
	b.WriteString(name)
	b.Write(le32(mode))
	b.Write(le64(declared))
	b.Write(data)
	sum := sha256.Sum256(data)
	return rawEntry{head: b.Bytes(), digest: sum[:]}
}

func rawDir(name string, mode uint32) rawEntry {
	var b bytes.Buffer
	b.WriteByte(typDir)
	b.Write(le16(len(name)))
	b.WriteString(name)
	b.Write(le32(mode))
	return rawEntry{head: b.Bytes()}
}

func assemble(entries ...rawEntry) []byte {
	var hashed bytes.Buffer
	hashed.WriteString(Magic)
	hashed.WriteByte(Version)
	for _, e := range entries {
		hashed.Write(e.head)
	}
	hashed.WriteByte(typEnd)
	overall := sha256.Sum256(hashed.Bytes())

	var out bytes.Buffer
	out.WriteString(Magic)
	out.WriteByte(Version)
	for _, e := range entries {
		out.Write(e.head)
		out.Write(e.digest)
	}
	out.WriteByte(typEnd)
	out.Write(overall[:])
	return out.Bytes()
}

// --- helpers ---

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustErrIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}

func newDest(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "out")
}

func snapshotTree(t *testing.T, root string) map[string][2]string {
	t.Helper()
	out := map[string][2]string{}
	must(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		perm := fi.Mode().Perm().String()
		if fi.IsDir() {
			out[key] = [2]string{"dir", perm}
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[key] = [2]string{perm, string(sum[:])}
		return nil
	}))
	return out
}

// --- tests ---

func TestRoundTrip(t *testing.T) {
	src := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(src, "sub", "deep"), 0o755))
	must(t, os.MkdirAll(filepath.Join(src, "emptydir"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hello world"), 0o644))
	must(t, os.WriteFile(filepath.Join(src, "empty"), nil, 0o640))
	must(t, os.WriteFile(filepath.Join(src, "sub", "deep", "bin.dat"),
		bytes.Repeat([]byte{0, 1, 2, 255}, 4096), 0o600))

	var buf bytes.Buffer
	must(t, Pack(src, &buf))

	// Deterministic: packing twice yields identical bytes.
	var buf2 bytes.Buffer
	must(t, Pack(src, &buf2))
	if !bytes.Equal(buf.Bytes(), buf2.Bytes()) {
		t.Fatal("pack is not deterministic")
	}

	entries, err := List(bytes.NewReader(buf.Bytes()), Limits{})
	must(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	want := []string{"empty", "emptydir", "hello.txt", "sub", "sub/deep", "sub/deep/bin.dat"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}

	dest := newDest(t)
	must(t, Unpack(bytes.NewReader(buf.Bytes()), dest, Limits{}))

	gotTree := snapshotTree(t, dest)
	wantTree := snapshotTree(t, src)
	if !reflect.DeepEqual(gotTree, wantTree) {
		t.Fatalf("tree mismatch:\ngot  %v\nwant %v", gotTree, wantTree)
	}
}

func TestEmptyArchive(t *testing.T) {
	src := t.TempDir()
	var buf bytes.Buffer
	must(t, Pack(src, &buf))
	dest := newDest(t)
	must(t, Unpack(bytes.NewReader(buf.Bytes()), dest, Limits{}))
	fi, err := os.Stat(dest)
	must(t, err)
	if !fi.IsDir() {
		t.Fatal("dest is not a directory")
	}
	ds, err := os.ReadDir(dest)
	must(t, err)
	if len(ds) != 0 {
		t.Fatalf("dest not empty: %v", ds)
	}
}

func TestValidatePath(t *testing.T) {
	bad := []string{
		"", "..", "../x", "a/../b", "a/b/..", "/abs", "/",
		"a//b", "./a", "a/./b", `a\b`, `C:\x`, "C:/x",
		string([]byte{0xff, 0xfe}) + "/x",
		string(bytes.Repeat([]byte{'a'}, maxNameLen+1)),
	}
	for _, p := range bad {
		if _, err := ValidatePath(p); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("ValidatePath(%q) = %v, want ErrUnsafePath", p, err)
		}
	}
	good := map[string]string{
		"a":       "a",
		"a/b/c":   "a/b/c",
		"a.b":     "a.b",
		"..x":     "..x",
		"x..":     "x..",
		"a/../..": "", // still bad, checked below
	}
	for in, want := range good {
		if want == "" {
			continue
		}
		got, err := ValidatePath(in)
		if err != nil || got != want {
			t.Errorf("ValidatePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestFormatGuards(t *testing.T) {
	good := assemble(rawFile("a.txt", 0o644, []byte("payload-data")))

	t.Run("bad magic", func(t *testing.T) {
		b := bytes.Clone(good)
		b[0] = 'X'
		mustErrIs(t, Unpack(bytes.NewReader(b), newDest(t), Limits{}), ErrBadMagic)
	})
	t.Run("bad version", func(t *testing.T) {
		b := bytes.Clone(good)
		b[len(Magic)] = 99
		mustErrIs(t, Unpack(bytes.NewReader(b), newDest(t), Limits{}), ErrBadVersion)
	})
	t.Run("truncated", func(t *testing.T) {
		mustErrIs(t, Unpack(bytes.NewReader(good[:len(good)-10]), newDest(t), Limits{}), ErrFormat)
	})
	t.Run("trailing garbage", func(t *testing.T) {
		b := append(bytes.Clone(good), 0x00)
		mustErrIs(t, Unpack(bytes.NewReader(b), newDest(t), Limits{}), ErrFormat)
	})
	t.Run("unknown entry type", func(t *testing.T) {
		var b bytes.Buffer
		b.WriteString(Magic)
		b.WriteByte(Version)
		b.WriteByte(77)
		mustErrIs(t, Unpack(bytes.NewReader(b.Bytes()), newDest(t), Limits{}), ErrFormat)
	})
	t.Run("declared size exceeds payload", func(t *testing.T) {
		arc := assemble(rawFileSized("big", 0o644, 1<<40, []byte("x")))
		mustErrIs(t, Unpack(bytes.NewReader(arc), newDest(t), Limits{}), ErrSize)
	})
	t.Run("payload shorter than declared", func(t *testing.T) {
		arc := assemble(rawFileSized("f", 0o644, 100, []byte("short")))
		lim := Limits{MaxFileSize: 1 << 20, MaxTotalSize: 1 << 20}
		mustErrIs(t, Unpack(bytes.NewReader(arc), newDest(t), lim), ErrFormat)
	})
	t.Run("mode with setuid bits", func(t *testing.T) {
		arc := assemble(rawFile("x", 0o104755, []byte("y")))
		mustErrIs(t, Unpack(bytes.NewReader(arc), newDest(t), Limits{}), ErrFormat)
	})
}

func TestDigestTamper(t *testing.T) {
	good := assemble(
		rawFile("a.txt", 0o644, []byte("first-file")),
		rawFile("b.txt", 0o600, []byte("second-file")),
	)
	t.Run("payload byte flipped", func(t *testing.T) {
		b := bytes.Clone(good)
		i := bytes.Index(b, []byte("first-file"))
		if i < 0 {
			t.Fatal("payload not found")
		}
		b[i] ^= 0xff
		mustErrIs(t, Unpack(bytes.NewReader(b), newDest(t), Limits{}), ErrDigest)
	})
	t.Run("overall digest flipped", func(t *testing.T) {
		b := bytes.Clone(good)
		b[len(b)-1] ^= 0xff
		mustErrIs(t, Unpack(bytes.NewReader(b), newDest(t), Limits{}), ErrDigest)
	})
	t.Run("entry digest flipped", func(t *testing.T) {
		b := bytes.Clone(good)
		i := bytes.Index(b, []byte("first-file"))
		b[i+len("first-file")] ^= 0xff // first byte of per-entry digest
		mustErrIs(t, Unpack(bytes.NewReader(b), newDest(t), Limits{}), ErrDigest)
	})
	t.Run("name byte flipped", func(t *testing.T) {
		b := bytes.Clone(good)
		i := bytes.Index(b, []byte("a.txt"))
		b[i] = 'z' // still a valid path, but overall digest must catch it
		mustErrIs(t, Unpack(bytes.NewReader(b), newDest(t), Limits{}), ErrDigest)
	})
}

func TestListStreamsPayloads(t *testing.T) {
	// A huge declared file must not be buffered in memory by List; give it
	// a small limit so the scan stops at validation.
	arc := assemble(rawFileSized("huge", 0o644, 1<<62, nil))
	_, err := List(bytes.NewReader(arc), Limits{MaxFileSize: 1 << 20})
	mustErrIs(t, err, ErrSize)
}
