package rootedpublish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// noTempLeft asserts that no publisher temporary object remains under dir.
func noTempLeft(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".rootedpublish.tmp.") {
			t.Errorf("temporary object leaked: %s/%s", dir, e.Name())
		}
	}
}

// blockingHook returns a hook that blocks on the first invocation at point and
// a release function that unblocks it.
func blockingHook(point HookPoint) (Hook, func()) {
	proceed := make(chan struct{})
	started := make(chan struct{})
	once := make(chan struct{})
	hook := func(ctx context.Context, hp HookPoint, relPath string) error {
		if hp == point {
			select {
			case <-once:
			default:
				close(started)
				close(once)
				select {
				case <-proceed:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		return nil
	}
	return hook, func() { close(proceed) }
}

func TestPublishBasicAtomicReplace(t *testing.T) {
	root := t.TempDir()
	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	target := filepath.Join(root, "docs", "report.txt")
	ctx := context.Background()

	err = pub.Publish(ctx, "docs/report.txt", strings.NewReader("v1"), Options{CreateDirs: true})
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if got := readFile(t, target); got != "v1" {
		t.Fatalf("content = %q, want v1", got)
	}

	err = pub.Publish(ctx, "docs/report.txt", strings.NewReader("v2-contents"), Options{})
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if got := readFile(t, target); got != "v2-contents" {
		t.Fatalf("content = %q, want v2-contents", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("perm = %o, want 644", perm)
	}
	noTempLeft(t, filepath.Join(root, "docs"))
}

func TestPublishCustomMode(t *testing.T) {
	root := t.TempDir()
	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	err = pub.Publish(context.Background(), "f.bin", strings.NewReader("x"),
		Options{Mode: 0o600})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "f.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
}

func TestSplitRelRejectsEscapes(t *testing.T) {
	bad := []string{
		"", ".", "/abs/path", "..", "../etc/passwd",
		"a/../../b", "a/./b", "a//b", "a/b/..", "a/b/../",
		"nul\x00byte",
	}
	for _, p := range bad {
		if _, _, err := splitRel(p); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("splitRel(%q) err = %v, want ErrInvalidPath", p, err)
		}
	}
	good := map[string]struct {
		dirs []string
		name string
	}{
		"a.txt":       {nil, "a.txt"},
		"d/a.txt":     {[]string{"d"}, "a.txt"},
		"d/e/f/a.txt": {[]string{"d", "e", "f"}, "a.txt"},
	}
	for p, want := range good {
		dirs, name, err := splitRel(p)
		if err != nil {
			t.Errorf("splitRel(%q) unexpected err %v", p, err)
			continue
		}
		if name != want.name || strings.Join(dirs, "/") != strings.Join(want.dirs, "/") {
			t.Errorf("splitRel(%q) = (%v,%q), want (%v,%q)", p, dirs, name, want.dirs, want.name)
		}
	}
}

// TestSymlinkComponentRejected plants a symlink as an intermediate path
// component and proves the publisher refuses it instead of following it out of
// the root.
func TestSymlinkComponentRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel.txt")
	writeFile(t, sentinel, "sentinel-original")

	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	err = pub.Publish(context.Background(), "link/sentinel.txt",
		strings.NewReader("pwned"), Options{CreateDirs: true})
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("err = %v, want ErrSymlink", err)
	}
	if got := readFile(t, sentinel); got != "sentinel-original" {
		t.Fatalf("sentinel = %q, root was escaped!", got)
	}
}

// TestSymlinkFinalTargetRejected plants a symlink at the final target name.
func TestSymlinkFinalTargetRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel.txt")
	writeFile(t, sentinel, "sentinel-original")
	if err := os.Symlink(sentinel, filepath.Join(root, "target")); err != nil {
		t.Fatal(err)
	}

	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	err = pub.Publish(context.Background(), "target", strings.NewReader("pwned"), Options{})
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("err = %v, want ErrSymlink", err)
	}
	if got := readFile(t, sentinel); got != "sentinel-original" {
		t.Fatalf("sentinel = %q, root was escaped!", got)
	}
	noTempLeft(t, root)
}

// TestParentDirSwapRace reproduces the classic TOCTOU: while Publish is blocked
// after resolving the parent directory, an attacker renames the real parent
// directory away and drops a replacement whose target is a symlink out of the
// root. All later operations use the already opened descriptor, so the write
// lands in the original directory and the outside sentinel is untouched.
func TestParentDirSwapRace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel.txt")
	writeFile(t, sentinel, "sentinel-original")

	realParent := filepath.Join(root, "uploads")
	if err := os.Mkdir(realParent, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(realParent, "data.txt")
	writeFile(t, oldPath, "old-version")

	evilParent := filepath.Join(root, "uploads-evil")
	if err := os.Mkdir(evilParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, filepath.Join(evilParent, "data.txt")); err != nil {
		t.Fatal(err)
	}

	hook, release := blockingHook(HookResolvedParent)
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
	if err := os.Rename(realParent, filepath.Join(root, "uploads-hidden")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(evilParent, realParent); err != nil {
		t.Fatal(err)
	}
	release()

	if err := <-errCh; err != nil {
		t.Fatalf("publish: %v", err)
	}

	if got := readFile(t, sentinel); got != "sentinel-original" {
		t.Fatalf("sentinel = %q, root was escaped!", got)
	}
	moved := filepath.Join(root, "uploads-hidden", "data.txt")
	if got := readFile(t, moved); got != "new-version" {
		t.Fatalf("published content = %q, want new-version", got)
	}
	link, err := os.Readlink(filepath.Join(realParent, "data.txt"))
	if err != nil {
		t.Fatalf("evil link altered: %v", err)
	}
	if link != sentinel {
		t.Fatalf("evil link = %q, want %q", link, sentinel)
	}
	noTempLeft(t, root)
	noTempLeft(t, filepath.Join(root, "uploads-hidden"))
}

// TestCreateTempRefusesSymlinkAndExisting verifies the two race-proof
// properties of the temporary-file open: O_NOFOLLOW refuses an attacker-planted
// symlink and O_EXCL refuses an existing entry, so the publisher can never be
// redirected through a temporary name.
func TestCreateTempRefusesSymlinkAndExisting(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel.txt")
	writeFile(t, sentinel, "sentinel-original")

	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	parentFD, err := pub.resolveParent(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(parentFD)

	// Plant a symlink at a name and prove the exact open flags used by
	// createTemp refuse it (ELOOP) rather than following it.
	linkName := "attacker-evil-link"
	if err := os.Symlink(sentinel, filepath.Join(root, linkName)); err != nil {
		t.Fatal(err)
	}
	if _, err := openAT(parentFD, linkName,
		syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0o600); !errors.Is(err, syscall.ELOOP) && !errors.Is(err, syscall.EEXIST) {
		t.Fatalf("symlink temp open err = %v, want ELOOP or EEXIST (both refuse)", err)
	}
	if got := readFile(t, sentinel); got != "sentinel-original" {
		t.Fatalf("sentinel = %q, root was escaped!", got)
	}

	// A real createTemp call must succeed and leave a 0o600 regular file that
	// cleans up through the descriptor.
	name, fd, err := createTemp(parentFD)
	if err != nil {
		t.Fatalf("createTemp: %v", err)
	}
	if err := syscall.Close(fd); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(root, name)); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("temp must be a regular file: %v err=%v", fi, err)
	} else if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("temp perm = %o, want 600", perm)
	}
	// Re-creating the same name must fail with EEXIST (O_EXCL).
	if _, err := openAT(parentFD, name,
		syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0o600); !errors.Is(err, syscall.EEXIST) {
		t.Fatalf("existing temp open err = %v, want EEXIST", err)
	}
	if err := unlinkAT(parentFD, name, 0); err != nil {
		t.Fatalf("unlink temp via descriptor: %v", err)
	}
	noTempLeft(t, root)
}

// TestConcurrentDistinctPublishesNoTempCollision runs many concurrent
// publishes into distinct files and asserts every one lands intact with no
// temporary-name collision or leaked object.
func TestConcurrentDistinctPublishesNoTempCollision(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}
	pub, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	const n = 40
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := strings.Repeat(string(rune('A'+i%26)), 128)
			target := fmt.Sprintf("uploads/file-%d.txt", i)
			if err := pub.Publish(context.Background(), target,
				strings.NewReader(payload), Options{}); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent publish: %v", err)
	}
	for i := 0; i < n; i++ {
		want := strings.Repeat(string(rune('A'+i%26)), 128)
		got := readFile(t, filepath.Join(root, "uploads", fmt.Sprintf("file-%d.txt", i)))
		if got != want {
			t.Fatalf("file-%d mismatch", i)
		}
	}
	noTempLeft(t, filepath.Join(root, "uploads"))
}
