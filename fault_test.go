package atomicpub

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reopen simulates a process restart over the same on-disk root.
func reopen(t *testing.T, dir string, fs FS) *Store {
	t.Helper()
	s, err := Open(dir, fs)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return s
}

func readBoth(t *testing.T, s *Store) (string, string) {
	t.Helper()
	r, err := s.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer r.Close()
	a, err := r.Read("a.txt")
	if err != nil {
		t.Fatalf("read a: %v", err)
	}
	b, err := r.Read("b.txt")
	if err != nil {
		t.Fatalf("read b: %v", err)
	}
	return string(a), string(b)
}

func assertConsistent(t *testing.T, s *Store, wantTag string) {
	t.Helper()
	a, b := readBoth(t, s)
	ta, tb := strings.TrimPrefix(a, "a-"), strings.TrimPrefix(b, "b-")
	if ta != tb {
		t.Fatalf("mixed version visible: a=%q b=%q", a, b)
	}
	if wantTag != "" && ta != wantTag {
		t.Fatalf("tag=%q want %q", ta, wantTag)
	}
}

// failOn returns a hook that fails the first operation matching pred.
func failOn(pred func(op, path string) bool, err error) (func(op, path string) error, *bool) {
	fired := new(bool)
	return func(op, path string) error {
		if !*fired && pred(op, path) {
			*fired = true
			return err
		}
		return nil
	}, fired
}

func TestWriteFailureDuringPublish(t *testing.T) {
	dir := t.TempDir()
	ffs := NewFaultFS(nil)
	ffs.inner = osFS{}
	s := reopen(t, dir, ffs)
	if _, err := s.Publish(versionedFiles("one")); err != nil {
		t.Fatal(err)
	}

	hook, _ := failOn(func(op, path string) bool {
		return op == "write" && strings.HasSuffix(path, "b.txt")
	}, errors.New("disk full"))
	ffs.SetHook(hook)

	if _, err := s.Publish(versionedFiles("two")); err == nil {
		t.Fatal("publish should fail")
	}
	ffs.SetHook(nil)

	// Readers still see the complete old version.
	assertConsistent(t, s, "one")

	// After restart, staging is cleaned and the old version is still live.
	s = reopen(t, dir, ffs)
	rep := s.Report()
	if len(rep.RemovedStaging) != 1 {
		t.Fatalf("staging not cleaned: %+v", rep)
	}
	assertConsistent(t, s, "one")
}

func TestCommitRenameFailure(t *testing.T) {
	dir := t.TempDir()
	ffs := NewFaultFS(osFS{})
	s := reopen(t, dir, ffs)
	if _, err := s.Publish(versionedFiles("one")); err != nil {
		t.Fatal(err)
	}

	hook, _ := failOn(func(op, path string) bool {
		return op == "rename" && strings.Contains(path, "tmp-v-")
	}, errors.New("rename denied"))
	ffs.SetHook(hook)
	if _, err := s.Publish(versionedFiles("two")); err == nil {
		t.Fatal("publish should fail")
	}
	ffs.SetHook(nil)

	assertConsistent(t, s, "one")
	s = reopen(t, dir, ffs)
	if len(s.Report().RemovedStaging) != 1 {
		t.Fatalf("staging not cleaned: %+v", s.Report())
	}
	assertConsistent(t, s, "one")
}

// TestCrashBetweenCommitAndPointerFlip injects a restart exactly when the
// pointer flip rename runs: the new version is committed on disk but the
// pointer still names the old one.
func TestCrashBetweenCommitAndPointerFlip(t *testing.T) {
	dir := t.TempDir()
	ffs := NewFaultFS(osFS{})
	s := reopen(t, dir, ffs)
	if _, err := s.Publish(versionedFiles("one")); err != nil {
		t.Fatal(err)
	}

	hook, _ := failOn(func(op, path string) bool {
		return op == "rename" && strings.Contains(path, pointerTmp)
	}, ErrCrash)
	ffs.SetHook(hook)
	if _, err := s.Publish(versionedFiles("two")); !errors.Is(err, ErrCrash) {
		t.Fatalf("want crash, got %v", err)
	}
	ffs.SetHook(nil)

	// Restart: a failed publish must not become visible.
	s = reopen(t, dir, ffs)
	rep := s.Report()
	if rep.PointerRepaired {
		t.Fatalf("old pointer was valid; no repair expected: %+v", rep)
	}
	assertConsistent(t, s, "one")

	// Publishing again works and reuses no stale state.
	if _, err := s.Publish(versionedFiles("three")); err != nil {
		t.Fatal(err)
	}
	assertConsistent(t, s, "three")
}

// TestCrashAtEveryStep restarts the publish at every single filesystem
// operation and verifies readers only ever see a complete version.
func TestCrashAtEveryStep(t *testing.T) {
	for step := 0; step < 14; step++ {
		t.Run(fmt.Sprintf("step-%02d", step), func(t *testing.T) {
			dir := t.TempDir()
			ffs := NewFaultFS(osFS{})
			s := reopen(t, dir, ffs)
			if _, err := s.Publish(versionedFiles("one")); err != nil {
				t.Fatal(err)
			}

			count := 0
			ffs.SetHook(func(op, path string) error {
				if op == "readdir" || op == "stat" || op == "read" {
					return nil // let reads through; crash only on mutations
				}
				if count == step {
					count++
					return ErrCrash
				}
				count++
				return nil
			})
			s.Publish(versionedFiles("two")) // result irrelevant; state must be sound
			ffs.SetHook(nil)

			s = reopen(t, dir, ffs)
			assertConsistent(t, s, "") // either "one" or "two", never mixed

			if _, err := s.Publish(versionedFiles("three")); err != nil {
				t.Fatalf("publish after crash: %v", err)
			}
			assertConsistent(t, s, "three")
		})
	}
}

// corruptWriteFS writes corrupted bytes for one file to simulate a bad disk.
type corruptWriteFS struct {
	FS
	victim string
}

func (c corruptWriteFS) WriteFile(path string, data []byte) error {
	if strings.HasSuffix(path, c.victim) && len(data) > 0 {
		data = append([]byte(nil), data...)
		data[0] ^= 0xff
	}
	return c.FS.WriteFile(path, data)
}

func TestCorruptWriteCaughtByValidation(t *testing.T) {
	dir := t.TempDir()
	s := reopen(t, dir, nil)
	if _, err := s.Publish(versionedFiles("one")); err != nil {
		t.Fatal(err)
	}

	bad := reopen(t, dir, corruptWriteFS{FS: osFS{}, victim: "b.txt"})
	if _, err := bad.Publish(versionedFiles("two")); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}

	// Nothing committed; old version intact across a restart.
	s = reopen(t, dir, nil)
	assertConsistent(t, s, "one")
	if len(s.Report().RemovedStaging) != 1 {
		t.Fatalf("corrupt staging should be cleaned: %+v", s.Report())
	}
}

func TestCorruptPointerRepaired(t *testing.T) {
	dir := t.TempDir()
	s := reopen(t, dir, nil)
	if _, err := s.Publish(versionedFiles("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(versionedFiles("two")); err != nil {
		t.Fatal(err)
	}

	for _, garbage := range []string{"", "garbage\n", "v-999999\n", "v-000001\nextra\n"} {
		if err := os.WriteFile(filepath.Join(dir, pointerName), []byte(garbage), 0o644); err != nil {
			t.Fatal(err)
		}
		s = reopen(t, dir, nil)
		rep := s.Report()
		if !rep.PointerRepaired {
			t.Fatalf("pointer %q not repaired: %+v", garbage, rep)
		}
		assertConsistent(t, s, "two") // newest valid version wins
	}
}

func TestDanglingPointerRepaired(t *testing.T) {
	dir := t.TempDir()
	s := reopen(t, dir, nil)
	if _, err := s.Publish(versionedFiles("one")); err != nil {
		t.Fatal(err)
	}
	// Pointer names a version whose directory was lost.
	if err := os.WriteFile(filepath.Join(dir, pointerName), []byte("v-000007\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s = reopen(t, dir, nil)
	if !s.Report().PointerRepaired {
		t.Fatal("dangling pointer not repaired")
	}
	assertConsistent(t, s, "one")
}

func TestUnknownEntriesNeverDeleted(t *testing.T) {
	dir := t.TempDir()
	s := reopen(t, dir, nil)
	if _, err := s.Publish(versionedFiles("one")); err != nil {
		t.Fatal(err)
	}

	// Plant foreign entries next to the store's own files.
	os.MkdirAll(filepath.Join(dir, "somebody-elses-dir"), 0o755)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644)
	os.MkdirAll(filepath.Join(dir, "v-notanumber"), 0o755)

	s = reopen(t, dir, nil)
	rep := s.Report()
	if len(rep.UnknownEntries) != 3 {
		t.Fatalf("unknown entries: %+v", rep)
	}
	for _, p := range []string{"somebody-elses-dir", "notes.txt", "v-notanumber"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatalf("unknown entry %s was touched: %v", p, err)
		}
	}
	// Publishing must not GC them either.
	if _, err := s.Publish(versionedFiles("two")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"somebody-elses-dir", "notes.txt", "v-notanumber"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatalf("unknown entry %s deleted by gc: %v", p, err)
		}
	}
}

func TestIncompleteVersionKeptButNotUsed(t *testing.T) {
	dir := t.TempDir()
	s := reopen(t, dir, nil)
	if _, err := s.Publish(versionedFiles("one")); err != nil {
		t.Fatal(err)
	}

	// A v-* directory without a manifest: known name pattern, unknown state.
	os.MkdirAll(filepath.Join(dir, "v-000005"), 0o755)
	os.WriteFile(filepath.Join(dir, "v-000005", "a.txt"), []byte("a-five"), 0o644)

	s = reopen(t, dir, nil)
	rep := s.Report()
	if len(rep.CorruptVersions) != 1 || rep.CorruptVersions[0] != "v-000005" {
		t.Fatalf("corrupt versions: %+v", rep)
	}
	// Not deleted, and not selected even though its number is highest.
	if _, err := os.Stat(filepath.Join(dir, "v-000005")); err != nil {
		t.Fatal("incomplete version was deleted")
	}
	assertConsistent(t, s, "one")

	// The next publish must not reuse the corrupt directory's number.
	v, err := s.Publish(versionedFiles("two"))
	if err != nil {
		t.Fatal(err)
	}
	if v != "v-000006" {
		t.Fatalf("version %q, want v-000006", v)
	}
}

func TestCrashDuringRecoveryPointerRepair(t *testing.T) {
	dir := t.TempDir()
	s := reopen(t, dir, nil)
	if _, err := s.Publish(versionedFiles("one")); err != nil {
		t.Fatal(err)
	}
	// Corrupt the pointer, then crash recovery's repair rename.
	os.Remove(filepath.Join(dir, pointerName))

	ffs := NewFaultFS(osFS{})
	hook, _ := failOn(func(op, path string) bool {
		return op == "rename" && strings.Contains(path, pointerTmp)
	}, ErrCrash)
	ffs.SetHook(hook)
	if _, err := Open(dir, ffs); err == nil {
		t.Fatal("recovery should report the crash")
	}
	ffs.SetHook(nil)

	// Next restart must still recover cleanly.
	s = reopen(t, dir, ffs)
	if !s.Report().PointerRepaired {
		t.Fatalf("report: %+v", s.Report())
	}
	assertConsistent(t, s, "one")
}
