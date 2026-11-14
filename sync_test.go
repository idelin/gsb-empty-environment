package netsec

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLegalSyncLifecycle(t *testing.T) {
	layout := newTestLayout(t)

	requireNoError(t, layout.root.MkdirAll("docs/2026", 0o755))
	requireNoError(t, layout.root.WriteFile("docs/2026/report.txt", []byte("version-1"), 0o640))
	got, err := layout.root.ReadFile("docs/2026/report.txt")
	requireNoError(t, err)
	if string(got) != "version-1" {
		t.Fatalf("read: got %q", got)
	}

	path := filepath.Join(layout.rootDir, "docs", "2026", "report.txt")
	before, err := os.Stat(path)
	requireNoError(t, err)
	requireNoError(t, layout.root.WriteFile("docs/2026/report.txt", []byte("version-2"), 0))
	after, err := os.Stat(path)
	requireNoError(t, err)
	if os.SameFile(before, after) {
		t.Fatal("atomic replacement reused the old inode")
	}
	if after.Mode().Perm() != 0o640 {
		t.Fatalf("atomic replacement changed mode to %o", after.Mode().Perm())
	}
	got, err = layout.root.ReadFile("docs/2026/report.txt")
	requireNoError(t, err)
	if string(got) != "version-2" {
		t.Fatalf("read after replace: got %q", got)
	}

	entries, err := os.ReadDir(filepath.Join(layout.rootDir, "docs", "2026"))
	requireNoError(t, err)
	if len(entries) != 1 || entries[0].Name() != "report.txt" {
		t.Fatalf("temporary file leaked: %v", entryNames(entries))
	}

	srcBefore, err := os.Stat(path)
	requireNoError(t, err)
	requireNoError(t, layout.root.Rename("docs/2026/report.txt", "docs/report-final.txt"))
	dstInfo, err := os.Stat(filepath.Join(layout.rootDir, "docs", "report-final.txt"))
	requireNoError(t, err)
	if !os.SameFile(srcBefore, dstInfo) {
		t.Fatal("rename did not preserve inode")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("old path still exists: %v", err)
	}

	requireNoError(t, layout.root.Remove("docs/report-final.txt"))
	if _, err := os.Stat(filepath.Join(layout.rootDir, "docs", "report-final.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("removed file still exists: %v", err)
	}
	requireNoError(t, layout.root.Remove("docs/2026"))
	requireNoError(t, layout.root.Remove("docs"))

	if _, err := layout.root.OpenFile("new.txt", os.O_WRONLY|os.O_CREATE, 0o644); !errors.Is(err, ErrUnsafeWrite) {
		t.Fatalf("OpenFile with write flags: got %v, want ErrUnsafeWrite", err)
	}
}

func TestWriteFromAndNestedDirectories(t *testing.T) {
	layout := newTestLayout(t)
	requireNoError(t, layout.root.MkdirAll("a/b/c", 0o755))
	requireNoError(t, layout.root.WriteFrom("a/b/c/file.txt", strings.NewReader("streamed"), 0o600))
	got, err := layout.root.ReadFile("a/b/c/file.txt")
	requireNoError(t, err)
	if string(got) != "streamed" {
		t.Fatalf("got %q", got)
	}
	info, err := layout.root.Stat("a/b/c/file.txt")
	requireNoError(t, err)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestRemoveAllStaysInsideRoot(t *testing.T) {
	layout := newTestLayout(t)
	requireNoError(t, layout.root.MkdirAll("tree/a/b", 0o755))
	requireNoError(t, layout.root.WriteFile("tree/a/b/file.txt", []byte("data"), 0o644))
	requireNoError(t, layout.root.WriteFile("tree/top.txt", []byte("data"), 0o644))
	requireNoError(t, layout.root.RemoveAll("tree"))
	if _, err := os.Stat(filepath.Join(layout.rootDir, "tree")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("tree still exists: %v", err)
	}
	requireNoError(t, layout.root.RemoveAll("tree"))

	outsideDir := filepath.Join(layout.outside, "keep")
	requireNoError(t, os.MkdirAll(outsideDir, 0o755))
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	requireNoError(t, os.WriteFile(outsideFile, []byte("secret"), 0o644))
	requireNoError(t, layout.root.MkdirAll("with-link", 0o755))
	requireNoError(t, os.Symlink(outsideDir, filepath.Join(layout.rootDir, "with-link", "escape")))

	if err := layout.root.RemoveAll("with-link"); err == nil {
		t.Fatal("RemoveAll through an escaping symlink succeeded")
	}
	got, err := os.ReadFile(outsideFile)
	requireNoError(t, err)
	if string(got) != "secret" {
		t.Fatalf("outside file changed: %q", got)
	}
}

func TestConcurrentAtomicWritesRemainConsistent(t *testing.T) {
	layout := newTestLayout(t)
	requireNoError(t, layout.root.WriteFile("shared.txt", []byte("initial"), 0o644))

	const writers = 24
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- layout.root.WriteFile("shared.txt", []byte(strings.Repeat(string(rune('a'+i%26)), 32)), 0o644)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		requireNoError(t, err)
	}

	got, err := layout.root.ReadFile("shared.txt")
	requireNoError(t, err)
	if len(got) != 32 {
		t.Fatalf("unexpected final content length: %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[0] {
			t.Fatalf("final content is interleaved: %q", got)
		}
	}
	entries, err := os.ReadDir(layout.rootDir)
	requireNoError(t, err)
	if len(entries) != 1 || entries[0].Name() != "shared.txt" {
		t.Fatalf("temporary files leaked: %v", entryNames(entries))
	}
}

func TestFailedWriteCleansTemporaryFile(t *testing.T) {
	layout := newTestLayout(t)
	writeErr := errors.New("writer failed")
	err := layout.root.WriteFunc("failing.txt", 0o644, func(w io.Writer) error {
		if _, err := io.WriteString(w, "partial"); err != nil {
			return err
		}
		return writeErr
	})
	if !errors.Is(err, writeErr) {
		t.Fatalf("WriteFunc error = %v, want %v", err, writeErr)
	}
	entries, err := os.ReadDir(layout.rootDir)
	requireNoError(t, err)
	if len(entries) != 0 {
		t.Fatalf("failed write left files behind: %v", entryNames(entries))
	}
}

func TestClosedRootRejectsOperations(t *testing.T) {
	layout := newTestLayout(t)
	requireNoError(t, layout.root.Close())
	if _, err := layout.root.ReadFile("x"); !errors.Is(err, ErrRootClosed) {
		t.Fatalf("ReadFile after Close: %v", err)
	}
	if err := layout.root.WriteFile("x", []byte("x"), 0o644); !errors.Is(err, ErrRootClosed) {
		t.Fatalf("WriteFile after Close: %v", err)
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
