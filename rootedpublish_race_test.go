package rootedpublish

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// failingReader returns data then errReader on the second Read.
var errReader = errors.New("simulated stream failure")

type failingReader struct{}

func (failingReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return 1, errReader
}

// TestReaderFailureCleansUp verifies that a broken upload stream aborts the
// publish, removes the temporary object and preserves the previous version.
func TestReaderFailureCleansUp(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "uploads")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "data.txt")
	writeFile(t, target, "old-version")

	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	err = pub.Publish(context.Background(), "uploads/data.txt",
		failingReader{}, Options{})
	if !errors.Is(err, errReader) {
		t.Fatalf("err = %v, want errReader", err)
	}
	if got := readFile(t, target); got != "old-version" {
		t.Fatalf("content = %q, want old-version", got)
	}
	noTempLeft(t, parent)
}

// TestCancelDuringStreamCleansUp cancels the context while payload bytes are
// still being read.
func TestCancelDuringStreamCleansUp(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "uploads")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "data.txt")
	writeFile(t, target, "old-version")

	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- pub.Publish(ctx, "uploads/data.txt", pr, Options{})
	}()

	if _, err := pw.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = pw.Close()

	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := readFile(t, target); got != "old-version" {
		t.Fatalf("content = %q, want old-version", got)
	}
	noTempLeft(t, parent)
}

// openDirAT opens a directory descriptor for a path known to contain no
// symlinks (test scaffolding inside t.TempDir).
func openDirAT(t *testing.T, p string) int {
	t.Helper()
	fd, err := syscall.Open(p, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open dir %s: %v", p, err)
	}
	return fd
}

// TestRenameExchangeLinkRace blocks after the payload is written and exchanges
// the finished target entry with a symlink using renamex_np(RENAME_EXCHANGE),
// i.e. the attacker swaps "link" and "target" through the parent directory.
// renameat then replaces whatever entry sits at the target name atomically; the
// outside sentinel is never opened for writing and remains intact.
func TestRenameExchangeLinkRace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel.txt")
	writeFile(t, sentinel, "sentinel-original")

	parent := filepath.Join(root, "uploads")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(parent, "data.txt")
	writeFile(t, oldPath, "old-version")
	if err := os.Symlink(sentinel, filepath.Join(parent, "evil-link")); err != nil {
		t.Fatal(err)
	}

	hook, release := blockingHook(HookAfterWrite)
	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- pub.Publish(context.Background(), "uploads/data.txt",
			strings.NewReader("new-version"), Options{Hook: hook})
	}()

	time.Sleep(100 * time.Millisecond)
	parentFD := openDirAT(t, parent)
	if err := renameExchange(parentFD, "data.txt", "evil-link"); err != nil {
		_ = syscall.Close(parentFD)
		t.Fatalf("renamex_np exchange: %v", err)
	}
	_ = syscall.Close(parentFD)
	release()

	if err := <-errCh; err != nil {
		t.Fatalf("publish: %v", err)
	}

	// The outside sentinel was never written through.
	if got := readFile(t, sentinel); got != "sentinel-original" {
		t.Fatalf("sentinel = %q, root was escaped!", got)
	}
	// The target name now holds the published regular file.
	if got := readFile(t, oldPath); got != "new-version" {
		t.Fatalf("target content = %q, want new-version", got)
	}
	if fi, err := os.Lstat(oldPath); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("target must be a regular file, got %v (err=%v)", fi, err)
	}
	noTempLeft(t, parent)
}

// TestFailurePointsPreserveOldVersion injects an error at every controllable
// synchronization point and verifies that the old version stays in place, no
// temp object leaks, and the outside sentinel is untouched.
func TestFailurePointsPreserveOldVersion(t *testing.T) {
	points := []HookPoint{HookResolvedParent, HookTempCreated, HookAfterWrite}
	for _, point := range points {
		t.Run(string(point), func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			sentinel := filepath.Join(outside, "sentinel.txt")
			writeFile(t, sentinel, "sentinel-original")

			parent := filepath.Join(root, "uploads")
			if err := os.Mkdir(parent, 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(parent, "data.txt")
			writeFile(t, target, "old-version")

			injected := errors.New("injected failure")
			pub, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer pub.Close()

			err = pub.Publish(context.Background(), "uploads/data.txt",
				strings.NewReader("new-version"), Options{
					Hook: func(ctx context.Context, hp HookPoint, relPath string) error {
						if hp == point {
							return injected
						}
						return nil
					},
				})
			if !errors.Is(err, injected) {
				t.Fatalf("err = %v, want injected", err)
			}

			if got := readFile(t, sentinel); got != "sentinel-original" {
				t.Fatalf("sentinel = %q, want sentinel-original", got)
			}
			if got := readFile(t, target); got != "old-version" {
				t.Fatalf("content = %q after failure at %s, want old-version", got, point)
			}
			noTempLeft(t, parent)
		})
	}
}

// TestAfterRenameHookObservesCommit proves the post-rename hook fires with the
// new content committed even if it returns an error, and that nothing leaks.
func TestAfterRenameHookObservesCommit(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "uploads")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "data.txt")
	writeFile(t, target, "old-version")

	injected := errors.New("ignored post-commit error")
	fired := false
	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	err = pub.Publish(context.Background(), "uploads/data.txt",
		strings.NewReader("new-version"), Options{
			Hook: func(ctx context.Context, hp HookPoint, relPath string) error {
				if hp == HookAfterRename {
					fired = true
					return injected
				}
				return nil
			},
		})
	if err != nil {
		t.Fatalf("post-rename hook error must be ignored, got %v", err)
	}
	if !fired {
		t.Fatal("HookAfterRename did not fire")
	}
	if got := readFile(t, target); got != "new-version" {
		t.Fatalf("content = %q, want new-version", got)
	}
	noTempLeft(t, parent)
}

// TestContextCancellationCleansUp cancels the context while the hook blocks and
// verifies the temporary file is removed and the old version survives.
func TestContextCancellationCleansUp(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "uploads")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "data.txt")
	writeFile(t, target, "old-version")

	hook, release := blockingHook(HookTempCreated)
	defer release()

	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- pub.Publish(ctx, "uploads/data.txt",
			strings.NewReader("new-version"), Options{Hook: hook})
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := readFile(t, target); got != "old-version" {
		t.Fatalf("content = %q, want old-version", got)
	}
	noTempLeft(t, parent)
}

// TestConcurrentPublishes exercises many writers replacing distinct and shared
// targets concurrently; every final file must contain one complete payload.
func TestConcurrentPublishes(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	const writers = 12
	var wg sync.WaitGroup
	errCh := make(chan error, writers*3)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				payload := strings.Repeat(string(rune('a'+w)), 64)
				target := filepath.Join("shared", "data.txt")
				if err := pub.Publish(context.Background(), target,
					strings.NewReader(payload), Options{}); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent publish: %v", err)
	}

	got := readFile(t, filepath.Join(root, "shared", "data.txt"))
	if len(got) != 64 {
		t.Fatalf("final length = %d, want 64 (torn write)", len(got))
	}
	first := got[0]
	for i := range got {
		if got[i] != first {
			t.Fatalf("final payload mixes writers: %q", got)
		}
	}
	noTempLeft(t, filepath.Join(root, "shared"))
}

// TestNotDirectoryError ensures a file where a directory is expected yields a
// typed error rather than a raw errno.
func TestNotDirectoryError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "afile"), "x")
	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	err = pub.Publish(context.Background(), "afile/data.txt",
		strings.NewReader("y"), Options{CreateDirs: true})
	if !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("err = %v, want ErrNotDirectory", err)
	}
}
