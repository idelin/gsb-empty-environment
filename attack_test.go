package safearch

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnpackRejectsTraversal(t *testing.T) {
	names := []string{
		"../evil.txt",
		"a/../../evil.txt",
		"/abs/evil.txt",
		`C:\evil.txt`,
		"a//b.txt",
		"./hidden",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dest := filepath.Join(parent, "out")
			arc := assemble(rawFile(name, 0o644, []byte("pwned")))
			mustErrIs(t, Unpack(bytes.NewReader(arc), dest, Limits{}), ErrUnsafePath)
			if _, err := os.Lstat(dest); !os.IsNotExist(err) {
				t.Fatalf("dest exists after failed unpack: %v", err)
			}
			// Nothing may have escaped into the parent directory.
			ds, err := os.ReadDir(parent)
			must(t, err)
			if len(ds) != 0 {
				t.Fatalf("unexpected files next to dest: %v", ds)
			}
		})
	}
}

func TestUnpackRejectsDuplicates(t *testing.T) {
	cases := map[string][]rawEntry{
		"same file twice": {
			rawFile("a.txt", 0o644, []byte("one")),
			rawFile("a.txt", 0o644, []byte("two")),
		},
		"same dir twice": {
			rawDir("d", 0o755),
			rawDir("d", 0o755),
		},
		"file then child": {
			rawFile("a", 0o644, []byte("x")),
			rawFile("a/b", 0o644, []byte("y")),
		},
		"dir covers earlier file": {
			rawFile("a/b", 0o644, []byte("y")),
			rawDir("a", 0o755),
		},
		"dir then file inside as file": {
			rawDir("a", 0o755),
			rawFile("a", 0o644, []byte("x")),
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			arc := assemble(entries...)
			mustErrIs(t, Unpack(bytes.NewReader(arc), newDest(t), Limits{}), ErrDuplicatePath)
		})
	}
}

func TestLimitsEnforced(t *testing.T) {
	t.Run("max file size", func(t *testing.T) {
		arc := assemble(rawFile("f", 0o644, []byte("12345")))
		mustErrIs(t, Unpack(bytes.NewReader(arc), newDest(t), Limits{MaxFileSize: 4}), ErrSize)
	})
	t.Run("max total size", func(t *testing.T) {
		arc := assemble(
			rawFile("a", 0o644, []byte("12")),
			rawFile("b", 0o644, []byte("34")),
		)
		mustErrIs(t, Unpack(bytes.NewReader(arc), newDest(t), Limits{MaxTotalSize: 3}), ErrSize)
	})
	t.Run("max entries", func(t *testing.T) {
		arc := assemble(
			rawFile("a", 0o644, []byte("1")),
			rawFile("b", 0o644, []byte("2")),
		)
		mustErrIs(t, Unpack(bytes.NewReader(arc), newDest(t), Limits{MaxEntries: 1}), ErrSize)
	})
	t.Run("huge declared size rejected before allocation", func(t *testing.T) {
		arc := assemble(rawFileSized("bomb", 0o644, 1<<62, nil))
		mustErrIs(t, Unpack(bytes.NewReader(arc), newDest(t), Limits{}), ErrSize)
	})
}

func TestPackRejectsSymlinks(t *testing.T) {
	src := t.TempDir()
	must(t, os.WriteFile(filepath.Join(src, "real.txt"), []byte("data"), 0o644))
	must(t, os.Symlink("real.txt", filepath.Join(src, "link.txt")))
	mustErrIs(t, Pack(src, &bytes.Buffer{}), ErrUnsupported)
}

func TestPublishNeverOverwrites(t *testing.T) {
	src := t.TempDir()
	must(t, os.WriteFile(filepath.Join(src, "f.txt"), []byte("v1"), 0o644))

	// PackFile must not overwrite an existing archive.
	dir := t.TempDir()
	arcPath := filepath.Join(dir, "a.sar")
	must(t, PackFile(src, arcPath))
	mustErrIs(t, PackFile(src, arcPath), ErrExists)

	// Unpack must not overwrite an existing directory.
	arc := assemble(rawFile("f.txt", 0o644, []byte("v2")))
	dest := newDest(t)
	must(t, os.Mkdir(dest, 0o755))
	mustErrIs(t, Unpack(bytes.NewReader(arc), dest, Limits{}), ErrExists)

	// Unpack twice: second run refuses.
	dest2 := newDest(t)
	must(t, Unpack(bytes.NewReader(arc), dest2, Limits{}))
	mustErrIs(t, Unpack(bytes.NewReader(arc), dest2, Limits{}), ErrExists)
}

func TestFailureLeavesNoPartialOutput(t *testing.T) {
	t.Run("unpack tampered archive", func(t *testing.T) {
		good := assemble(
			rawFile("ok.txt", 0o644, []byte("fine")),
			rawFile("bad.txt", 0o644, []byte("tampered")),
		)
		bad := bytes.Clone(good)
		i := bytes.Index(bad, []byte("tampered"))
		bad[i] ^= 0xff

		parent := t.TempDir()
		dest := filepath.Join(parent, "out")
		before, err := filepath.Glob(filepath.Join(os.TempDir(), "sar-unpack-*"))
		must(t, err)
		mustErrIs(t, Unpack(bytes.NewReader(bad), dest, Limits{}), ErrDigest)
		if _, err := os.Lstat(dest); !os.IsNotExist(err) {
			t.Fatalf("partial output left at dest: %v", err)
		}
		after, err := filepath.Glob(filepath.Join(os.TempDir(), "sar-unpack-*"))
		must(t, err)
		if len(after) > len(before) {
			t.Fatalf("staging dirs leaked: %v", after[len(before):])
		}
	})
	t.Run("pack failing source", func(t *testing.T) {
		src := t.TempDir()
		must(t, os.Symlink("nowhere", filepath.Join(src, "dangling")))
		dir := t.TempDir()
		dst := filepath.Join(dir, "x.sar")
		mustErrIs(t, PackFile(src, dst), ErrUnsupported)
		if _, err := os.Lstat(dst); !os.IsNotExist(err) {
			t.Fatalf("partial archive left at dst: %v", err)
		}
		leftovers, err := filepath.Glob(filepath.Join(dir, ".sar-tmp-*"))
		must(t, err)
		if len(leftovers) != 0 {
			t.Fatalf("temp files leaked: %v", leftovers)
		}
	})
}

func TestPackFileRoundTrip(t *testing.T) {
	src := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(src, "d"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "d", "f.txt"), []byte("content"), 0o644))
	dir := t.TempDir()
	arcPath := filepath.Join(dir, "out.sar")
	must(t, PackFile(src, arcPath))

	f, err := os.Open(arcPath)
	must(t, err)
	defer f.Close()
	dest := newDest(t)
	must(t, Unpack(f, dest, Limits{}))
	data, err := os.ReadFile(filepath.Join(dest, "d", "f.txt"))
	must(t, err)
	if string(data) != "content" {
		t.Fatalf("got %q", data)
	}
}

func TestErrorsAreSentinels(t *testing.T) {
	// Callers can classify failures with errors.Is.
	arc := assemble(rawFile("../x", 0o644, []byte("y")))
	err := Unpack(bytes.NewReader(arc), newDest(t), Limits{})
	if !errors.Is(err, ErrUnsafePath) || errors.Is(err, ErrDigest) {
		t.Fatalf("unexpected classification: %v", err)
	}
}
