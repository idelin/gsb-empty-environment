package publish

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func genFiles(tag string, n int) map[string][]byte {
	files := make(map[string][]byte, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("file-%02d.txt", i)
		files[name] = []byte(tag + ":" + name)
	}
	return files
}

func mustOpen(t *testing.T, root string) *Store {
	t.Helper()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func mustPublish(t *testing.T, s *Store, files map[string][]byte) string {
	t.Helper()
	id, err := s.Publish(files)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return id
}

// assertConsistentSnapshot verifies that every file in the snapshot
// carries the same generation tag, i.e. the reader never sees a mixture
// of two versions.
func assertConsistentSnapshot(t *testing.T, sn *Snapshot) {
	t.Helper()
	files := sn.Files()
	if len(files) == 0 {
		t.Fatalf("snapshot %s has no files", sn.Version)
	}
	var tag string
	for i, name := range files {
		data, err := sn.ReadFile(name)
		if err != nil {
			t.Fatalf("snapshot %s read %q: %v", sn.Version, name, err)
		}
		got := strings.SplitN(string(data), ":", 2)[0]
		if i == 0 {
			tag = got
			continue
		}
		if got != tag {
			t.Fatalf("MIXED VERSION in snapshot %s: %q has tag %q, want %q",
				sn.Version, name, got, tag)
		}
	}
}

func TestPublishAndAcquire(t *testing.T) {
	root := t.TempDir()
	s := mustOpen(t, root)
	if got := s.Current(); got != "" {
		t.Fatalf("fresh store current = %q, want empty", got)
	}
	if _, err := s.Acquire(); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("Acquire on empty store: %v", err)
	}

	id1 := mustPublish(t, s, genFiles("gen1", 3))
	if s.Current() != id1 {
		t.Fatalf("current = %q, want %q", s.Current(), id1)
	}
	sn, err := s.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if sn.Version != id1 {
		t.Fatalf("snapshot version = %q, want %q", sn.Version, id1)
	}
	assertConsistentSnapshot(t, sn)
}

func TestOldSnapshotKeepsWorkingAfterPublish(t *testing.T) {
	root := t.TempDir()
	s := mustOpen(t, root)
	id1 := mustPublish(t, s, genFiles("gen1", 4))

	old, err := s.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	// Open a real file handle before the switch.
	fh, err := old.Open("file-00.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()

	id2 := mustPublish(t, s, genFiles("gen2", 4))
	if id2 == id1 {
		t.Fatal("version ids must differ")
	}

	// The old snapshot still serves the old version.
	all, err := old.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range all {
		if !strings.HasPrefix(string(data), "gen1:") {
			t.Fatalf("old snapshot %q = %q, want gen1 content", name, data)
		}
	}
	buf := make([]byte, 64)
	n, _ := fh.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "gen1:") {
		t.Fatalf("old open handle read %q, want gen1 content", buf[:n])
	}

	// New acquisitions see the new version.
	fresh, err := s.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Version != id2 {
		t.Fatalf("fresh snapshot version = %q, want %q", fresh.Version, id2)
	}
	assertConsistentSnapshot(t, fresh)
}

func TestConcurrentPublishAndReadNeverMixed(t *testing.T) {
	root := t.TempDir()
	s := mustOpen(t, root)
	mustPublish(t, s, genFiles("gen000", 6))

	const generations = 30
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for g := 1; g <= generations; g++ {
			if _, err := s.Publish(genFiles(fmt.Sprintf("gen%03d", g), 6)); err != nil {
				t.Errorf("Publish: %v", err)
				return
			}
		}
	}()
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				sn, err := s.Acquire()
				if err != nil {
					t.Errorf("Acquire: %v", err)
					return
				}
				assertConsistentSnapshot(t, sn)
			}
		}()
	}
	wg.Wait()
}

// TestFaultInjectionMatrix injects a failure (plain error) and a crash
// (ErrCrash + reopen) at every step of the publish protocol. After each
// fault, readers must still see a complete version — old or new, never
// mixed — and a subsequent clean publish must succeed.
func TestFaultInjectionMatrix(t *testing.T) {
	ops := []Op{OpWriteDataFile, OpWriteManifest, OpValidate, OpRenameStage, OpWritePointer, OpRenamePointer}
	for _, op := range ops {
		for _, crash := range []bool{false, true} {
			name := string(op)
			if crash {
				name += "/crash"
			} else {
				name += "/error"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				s := mustOpen(t, root)
				id1 := mustPublish(t, s, genFiles("gen1", 5))

				oldSnap, err := s.Acquire()
				if err != nil {
					t.Fatal(err)
				}

				triggered := false
				injected := errors.New("injected fault")
				if crash {
					injected = ErrCrash
				}
				s.SetHook(func(o Op) error {
					if o == op && !triggered {
						triggered = true
						return injected
					}
					return nil
				})
				if _, err := s.Publish(genFiles("gen2", 5)); err == nil {
					t.Fatal("Publish should have failed")
				} else if !errors.Is(err, injected) {
					t.Fatalf("Publish error = %v, want %v", err, injected)
				}
				s.SetHook(nil)

				if crash {
					// Simulate a restart: reopen runs recovery.
					s = mustOpen(t, root)
				}

				// The pointer swap is the last step, so the current
				// version must still be the complete old one.
				if got := s.Current(); got != id1 {
					t.Fatalf("current after fault = %q, want %q", got, id1)
				}
				sn, err := s.Acquire()
				if err != nil {
					t.Fatal(err)
				}
				assertConsistentSnapshot(t, sn)
				// The pre-fault snapshot is still fully usable.
				assertConsistentSnapshot(t, oldSnap)

				// A clean retry succeeds and switches readers over.
				id2 := mustPublish(t, s, genFiles("gen2", 5))
				if got := s.Current(); got != id2 {
					t.Fatalf("current after retry = %q, want %q", got, id2)
				}
				sn2, err := s.Acquire()
				if err != nil {
					t.Fatal(err)
				}
				assertConsistentSnapshot(t, sn2)
				data, err := sn2.ReadFile("file-00.txt")
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(string(data), "gen2:") {
					t.Fatalf("new snapshot reads %q, want gen2 content", data)
				}
				// Old snapshot still pinned to gen1.
				assertConsistentSnapshot(t, oldSnap)
			})
		}
	}
}

func TestValidationRejectsCorruptStaging(t *testing.T) {
	root := t.TempDir()
	s := mustOpen(t, root)
	id1 := mustPublish(t, s, genFiles("gen1", 3))

	// Corrupt a staged file just before validation runs.
	s.SetHook(func(o Op) error {
		if o == OpValidate {
			matches, _ := filepath.Glob(filepath.Join(root, stagingDirName, "*", "file-00.txt"))
			if len(matches) != 1 {
				t.Fatalf("expected one staged file, got %v", matches)
			}
			return os.WriteFile(matches[0], []byte("corrupted"), 0o644)
		}
		return nil
	})
	if _, err := s.Publish(genFiles("gen2", 3)); err == nil {
		t.Fatal("Publish should fail validation")
	} else if !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("error = %v, want validation failure", err)
	}
	s.SetHook(nil)

	if got := s.Current(); got != id1 {
		t.Fatalf("current = %q, want %q", got, id1)
	}
	// The corrupt directory never became visible.
	entries, err := os.ReadDir(filepath.Join(root, versionsDirName))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("versions dir has %d entries, want 1", len(entries))
	}
	// Recovery recognizes the uncommitted staging directory and
	// quarantines it instead of deleting it.
	s = mustOpen(t, root)
	rep := s.Report()
	if len(rep.UncommittedStaging) != 1 {
		t.Fatalf("UncommittedStaging = %v, want 1 entry", rep.UncommittedStaging)
	}
	if len(rep.Quarantined) == 0 {
		t.Fatal("expected quarantined staging dir")
	}
	for _, q := range rep.Quarantined {
		if _, err := os.Lstat(q); err != nil {
			t.Fatalf("quarantined dir %q missing: %v", q, err)
		}
	}
	if got := s.Current(); got != id1 {
		t.Fatalf("current after recovery = %q, want %q", got, id1)
	}
}
