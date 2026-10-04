package carc

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	t.Helper()
	d, err := ioutil.TempDir("", "carc-test-")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func writeArchive(t *testing.T, dir string, build func(w *Writer) error) string {
	t.Helper()
	p := filepath.Join(dir, "test.carc")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := build(w); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := ioutil.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertNoLeftovers(t *testing.T, dir, destBase string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "."+destBase+".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary items left behind: %v", matches)
	}
	if _, err := os.Lstat(filepath.Join(dir, destBase)); !os.IsNotExist(err) {
		t.Fatalf("destination %q should not exist after failure", destBase)
	}
}

func TestExtractBasic(t *testing.T) {
	tmp := tempDir(t)
	defer os.RemoveAll(tmp)
	arc := writeArchive(t, tmp, func(w *Writer) error {
		for _, op := range []func() error{
			func() error { return w.AddDir("sub") },
			func() error { return w.AddDir("sub/nested") },
			func() error { return w.AddFile("sub/hello.txt", []byte("hello")) },
			func() error { return w.AddFile("top.txt", []byte("top")) },
			func() error { return w.AddSymlink("link", "sub") },
			func() error { return w.AddFile("link/via.txt", []byte("via")) },
			func() error { return w.AddHardlink("hard.txt", "top.txt") },
		} {
			if err := op(); err != nil {
				return err
			}
		}
		return nil
	})
	dest := filepath.Join(tmp, "out")
	if err := Extract(arc, dest); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dest, "sub", "hello.txt")); got != "hello" {
		t.Fatalf("hello.txt = %q", got)
	}
	if got := readFile(t, filepath.Join(dest, "top.txt")); got != "top" {
		t.Fatalf("top.txt = %q", got)
	}
	// File written through an in-root symlink lands in the real directory.
	if got := readFile(t, filepath.Join(dest, "sub", "via.txt")); got != "via" {
		t.Fatalf("via.txt = %q", got)
	}
	if fi, err := os.Lstat(filepath.Join(dest, "link")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link should be a symlink: %v", err)
	}
	// Hardlink shares the inode with its target.
	topFi, err := os.Stat(filepath.Join(dest, "top.txt"))
	if err != nil {
		t.Fatal(err)
	}
	hardFi, err := os.Stat(filepath.Join(dest, "hard.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(topFi, hardFi) {
		t.Fatal("hard.txt should be a hardlink to top.txt")
	}
}

func TestRejectBadNames(t *testing.T) {
	bad := []string{
		"/etc/passwd",
		"//abs",
		"../escape",
		"a/../../escape",
		"..",
		".",
		"",
	}
	for _, name := range bad {
		tmp := tempDir(t)
		arc := writeArchive(t, tmp, func(w *Writer) error {
			if name == "" {
				// Writer refuses empty names; encode it manually.
				_, err := w.w.Write([]byte{TypeFile, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
				return err
			}
			return w.AddFile(name, []byte("x"))
		})
		dest := filepath.Join(tmp, "out")
		if err := Extract(arc, dest); err == nil {
			t.Fatalf("name %q: expected error", name)
		}
		assertNoLeftovers(t, tmp, "out")
		os.RemoveAll(tmp)
	}
}

func TestRejectTypeConflict(t *testing.T) {
	cases := []struct {
		name  string
		first func(w *Writer) error
		then  func(w *Writer) error
	}{
		{"file-then-dir", func(w *Writer) error { return w.AddFile("x", []byte("1")) }, func(w *Writer) error { return w.AddDir("x") }},
		{"dir-then-file", func(w *Writer) error { return w.AddDir("x") }, func(w *Writer) error { return w.AddFile("x", []byte("1")) }},
		{"symlink-then-file", func(w *Writer) error { return w.AddSymlink("x", "y") }, func(w *Writer) error { return w.AddFile("x", []byte("1")) }},
		{"file-then-hardlink", func(w *Writer) error { return w.AddFile("x", []byte("1")) }, func(w *Writer) error { return w.AddHardlink("x", "x") }},
	}
	for _, c := range cases {
		tmp := tempDir(t)
		arc := writeArchive(t, tmp, func(w *Writer) error {
			if err := c.first(w); err != nil {
				return err
			}
			return c.then(w)
		})
		dest := filepath.Join(tmp, "out")
		err := Extract(arc, dest)
		if err == nil || !strings.Contains(err.Error(), "conflicting types") {
			t.Fatalf("%s: expected type conflict, got %v", c.name, err)
		}
		assertNoLeftovers(t, tmp, "out")
		os.RemoveAll(tmp)
	}
}

func TestRejectDuplicateFile(t *testing.T) {
	tmp := tempDir(t)
	defer os.RemoveAll(tmp)
	arc := writeArchive(t, tmp, func(w *Writer) error {
		if err := w.AddFile("a.txt", []byte("1")); err != nil {
			return err
		}
		return w.AddFile("a.txt", []byte("2"))
	})
	if err := Extract(arc, filepath.Join(tmp, "out")); err == nil {
		t.Fatal("expected duplicate target error")
	}
	assertNoLeftovers(t, tmp, "out")
}

func TestSymlinkEscapeRejected(t *testing.T) {
	tmp := tempDir(t)
	defer os.RemoveAll(tmp)
	outside := filepath.Join(tmp, "outside")
	if err := os.Mkdir(outside, 0755); err != nil {
		t.Fatal(err)
	}
	arc := writeArchive(t, tmp, func(w *Writer) error {
		if err := w.AddSymlink("link", "../outside"); err != nil {
			return err
		}
		return w.AddFile("link/pwn.txt", []byte("pwn"))
	})
	dest := filepath.Join(tmp, "out")
	if err := Extract(arc, dest); err == nil {
		t.Fatal("expected escape error")
	}
	if _, err := os.Lstat(filepath.Join(outside, "pwn.txt")); !os.IsNotExist(err) {
		t.Fatal("file escaped the extraction root")
	}
	assertNoLeftovers(t, tmp, "out")
}

func TestAbsoluteSymlinkEscapeRejected(t *testing.T) {
	tmp := tempDir(t)
	defer os.RemoveAll(tmp)
	outside := filepath.Join(tmp, "outside")
	if err := os.Mkdir(outside, 0755); err != nil {
		t.Fatal(err)
	}
	arc := writeArchive(t, tmp, func(w *Writer) error {
		if err := w.AddSymlink("abs", outside); err != nil {
			return err
		}
		return w.AddFile("abs/pwn.txt", []byte("pwn"))
	})
	if err := Extract(arc, filepath.Join(tmp, "out")); err == nil {
		t.Fatal("expected escape error")
	}
	if _, err := os.Lstat(filepath.Join(outside, "pwn.txt")); !os.IsNotExist(err) {
		t.Fatal("file escaped the extraction root via absolute symlink")
	}
	assertNoLeftovers(t, tmp, "out")
}

// A sibling directory whose name shares a string prefix with the
// destination ("out-evil" vs "out") must not fool containment checks.
func TestSamePrefixDirEscapeRejected(t *testing.T) {
	tmp := tempDir(t)
	defer os.RemoveAll(tmp)
	evil := filepath.Join(tmp, "out-evil")
	if err := os.Mkdir(evil, 0755); err != nil {
		t.Fatal(err)
	}
	arc := writeArchive(t, tmp, func(w *Writer) error {
		if err := w.AddSymlink("link", "../out-evil"); err != nil {
			return err
		}
		return w.AddFile("link/x.txt", []byte("x"))
	})
	dest := filepath.Join(tmp, "out")
	if err := Extract(arc, dest); err == nil {
		t.Fatal("expected escape error for same-prefix sibling")
	}
	if _, err := os.Lstat(filepath.Join(evil, "x.txt")); !os.IsNotExist(err) {
		t.Fatal("file escaped into same-prefix sibling directory")
	}
	assertNoLeftovers(t, tmp, "out")
}

func TestSymlinkLoopRejected(t *testing.T) {
	tmp := tempDir(t)
	defer os.RemoveAll(tmp)
	arc := writeArchive(t, tmp, func(w *Writer) error {
		if err := w.AddSymlink("a", "b"); err != nil {
			return err
		}
		if err := w.AddSymlink("b", "a"); err != nil {
			return err
		}
		if err := w.AddSymlink("self", "self"); err != nil {
			return err
		}
		return w.AddFile("a/x.txt", []byte("x"))
	})
	err := Extract(arc, filepath.Join(tmp, "out"))
	if err == nil || !strings.Contains(err.Error(), "too many levels") {
		t.Fatalf("expected symlink loop error, got %v", err)
	}
	assertNoLeftovers(t, tmp, "out")
}

// Link created before the file: writing through an in-root symlink is
// allowed and lands inside the root.
func TestLinkThenFileInsideRoot(t *testing.T) {
	tmp := tempDir(t)
	defer os.RemoveAll(tmp)
	arc := writeArchive(t, tmp, func(w *Writer) error {
		if err := w.AddDir("real"); err != nil {
			return err
		}
		if err := w.AddSymlink("alias", "real"); err != nil {
			return err
		}
		return w.AddFile("alias/data.txt", []byte("data"))
	})
	dest := filepath.Join(tmp, "out")
	if err := Extract(arc, dest); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dest, "real", "data.txt")); got != "data" {
		t.Fatalf("data.txt = %q", got)
	}
}

func TestHardlinkRules(t *testing.T) {
	// Hardlink to a path never extracted from this archive.
	tmp := tempDir(t)
	arc := writeArchive(t, tmp, func(w *Writer) error {
		return w.AddHardlink("h", "ghost")
	})
	if err := Extract(arc, filepath.Join(tmp, "out")); err == nil {
		t.Fatal("expected error for hardlink to unknown object")
	}
	assertNoLeftovers(t, tmp, "out")
	os.RemoveAll(tmp)

	// Hardlink target escaping the root.
	tmp = tempDir(t)
	arc = writeArchive(t, tmp, func(w *Writer) error {
		if err := w.AddFile("ok.txt", []byte("ok")); err != nil {
			return err
		}
		return w.AddHardlink("h", "../outside-file")
	})
	if err := Extract(arc, filepath.Join(tmp, "out")); err == nil {
		t.Fatal("expected error for escaping hardlink target")
	}
	assertNoLeftovers(t, tmp, "out")
	os.RemoveAll(tmp)

	// Hardlink to a directory is not a verified file object.
	tmp = tempDir(t)
	arc = writeArchive(t, tmp, func(w *Writer) error {
		if err := w.AddDir("d"); err != nil {
			return err
		}
		return w.AddHardlink("h", "d")
	})
	if err := Extract(arc, filepath.Join(tmp, "out")); err == nil {
		t.Fatal("expected error for hardlink to directory")
	}
	assertNoLeftovers(t, tmp, "out")
	os.RemoveAll(tmp)
}

func TestFailureCleansUp(t *testing.T) {
	tmp := tempDir(t)
	defer os.RemoveAll(tmp)
	arc := writeArchive(t, tmp, func(w *Writer) error {
		if err := w.AddDir("sub"); err != nil {
			return err
		}
		if err := w.AddFile("sub/ok.txt", []byte("ok")); err != nil {
			return err
		}
		return w.AddFile("../evil.txt", []byte("evil"))
	})
	if err := Extract(arc, filepath.Join(tmp, "out")); err == nil {
		t.Fatal("expected extraction to fail")
	}
	assertNoLeftovers(t, tmp, "out")
	entries, err := ioutil.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "test.carc" {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("unexpected leftovers in %s: %v", tmp, names)
	}
}

func TestDestinationExists(t *testing.T) {
	tmp := tempDir(t)
	defer os.RemoveAll(tmp)
	arc := writeArchive(t, tmp, func(w *Writer) error {
		return w.AddFile("a.txt", []byte("a"))
	})
	dest := filepath.Join(tmp, "out")
	if err := os.Mkdir(dest, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Extract(arc, dest); err == nil {
		t.Fatal("expected error for existing destination")
	}
}

func TestInvalidArchive(t *testing.T) {
	tmp := tempDir(t)
	defer os.RemoveAll(tmp)
	p := filepath.Join(tmp, "bad.carc")
	if err := ioutil.WriteFile(p, []byte("not an archive"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Extract(p, filepath.Join(tmp, "out")); err == nil {
		t.Fatal("expected error for bad magic")
	}
	assertNoLeftovers(t, tmp, "out")
}
