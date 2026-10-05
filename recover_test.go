package publish

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryBadPointer(t *testing.T) {
	cases := map[string]func(root string) string{
		"garbage content": func(root string) string {
			if err := os.WriteFile(filepath.Join(root, pointerFileName), []byte("!!!not-a-version\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return "!!!not-a-version"
		},
		"missing target": func(root string) string {
			if err := os.WriteFile(filepath.Join(root, pointerFileName), []byte("v999999\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return "v999999"
		},
		"pointer file deleted": func(root string) string {
			if err := os.Remove(filepath.Join(root, pointerFileName)); err != nil {
				t.Fatal(err)
			}
			return ""
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			s := mustOpen(t, root)
			mustPublish(t, s, genFiles("gen1", 2))
			id2 := mustPublish(t, s, genFiles("gen2", 2))

			wantTarget := corrupt(root)

			s = mustOpen(t, root)
			rep := s.Report()
			if !rep.BadPointer {
				t.Fatal("expected BadPointer")
			}
			if rep.PointerTarget != wantTarget {
				t.Fatalf("PointerTarget = %q, want %q", rep.PointerTarget, wantTarget)
			}
			if got := s.Current(); got != id2 {
				t.Fatalf("recovered current = %q, want newest valid %q", got, id2)
			}
			// Pointer file was rebuilt and is parseable.
			data, err := os.ReadFile(filepath.Join(root, pointerFileName))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != id2+"\n" {
				t.Fatalf("pointer file = %q, want %q", data, id2+"\n")
			}
			sn, err := s.Acquire()
			if err != nil {
				t.Fatal(err)
			}
			assertConsistentSnapshot(t, sn)
		})
	}
}

func TestRecoveryCrashBetweenCommitAndPointerSwap(t *testing.T) {
	root := t.TempDir()
	s := mustOpen(t, root)
	id1 := mustPublish(t, s, genFiles("gen1", 3))

	// Crash after the version directory is committed but before the
	// pointer is switched.
	s.SetHook(func(o Op) error {
		if o == OpRenamePointer {
			return ErrCrash
		}
		return nil
	})
	if _, err := s.Publish(genFiles("gen2", 3)); err != ErrCrash {
		t.Fatalf("Publish error = %v, want ErrCrash", err)
	}

	s = mustOpen(t, root)
	// Pointer still names the old version: readers see complete gen1.
	if got := s.Current(); got != id1 {
		t.Fatalf("current = %q, want %q", got, id1)
	}
	sn, err := s.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	assertConsistentSnapshot(t, sn)

	// The fully-committed but unpublished version directory survives and
	// a later publish proceeds normally.
	id3 := mustPublish(t, s, genFiles("gen3", 3))
	if got := s.Current(); got != id3 {
		t.Fatalf("current = %q, want %q", got, id3)
	}
}

func TestRecoveryPreservesUnknownEntries(t *testing.T) {
	root := t.TempDir()
	s := mustOpen(t, root)
	id1 := mustPublish(t, s, genFiles("gen1", 2))

	// Plant unknown entries: a stray dir and file in the root, and a
	// non-version-looking entry inside versions/.
	strayDir := filepath.Join(root, "mystery-dir")
	strayFile := filepath.Join(root, "stray.txt")
	strayVersion := filepath.Join(root, versionsDirName, "not-a-version")
	for _, p := range []string{strayDir, strayVersion} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(strayFile, []byte("leave me alone"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strayDir, "data.bin"), []byte("unknown"), 0o644); err != nil {
		t.Fatal(err)
	}

	s = mustOpen(t, root)
	rep := s.Report()
	if len(rep.UnknownEntries) != 3 {
		t.Fatalf("UnknownEntries = %v, want 3 entries", rep.UnknownEntries)
	}
	// Nothing unknown was deleted or moved.
	for _, p := range []string{strayDir, strayFile, strayVersion, filepath.Join(strayDir, "data.bin")} {
		if _, err := os.Lstat(p); err != nil {
			t.Fatalf("unknown entry %q was touched: %v", p, err)
		}
	}
	if got := s.Current(); got != id1 {
		t.Fatalf("current = %q, want %q", got, id1)
	}
}

func TestRecoveryQuarantinesCorruptVersion(t *testing.T) {
	root := t.TempDir()
	s := mustOpen(t, root)
	id1 := mustPublish(t, s, genFiles("gen1", 2))
	id2 := mustPublish(t, s, genFiles("gen2", 2))

	// Corrupt the newest committed version on disk and point at it.
	victim := filepath.Join(root, versionsDirName, id2, "file-00.txt")
	if err := os.WriteFile(victim, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}

	s = mustOpen(t, root)
	rep := s.Report()
	if !rep.BadPointer {
		t.Fatal("expected BadPointer: pointer targets a corrupt version")
	}
	if len(rep.Quarantined) != 1 {
		t.Fatalf("Quarantined = %v, want 1 entry", rep.Quarantined)
	}
	// Corrupt data was moved aside, not deleted.
	if _, err := os.Lstat(filepath.Join(rep.Quarantined[0], "file-00.txt")); err != nil {
		t.Fatalf("quarantined version missing files: %v", err)
	}
	if got := s.Current(); got != id1 {
		t.Fatalf("recovered current = %q, want %q", got, id1)
	}
	sn, err := s.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	assertConsistentSnapshot(t, sn)
}
