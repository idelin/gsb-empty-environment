package batchlease

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCompactionAndReopen(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	la := mustAcquire(t, s, "batch-A", 1, 4, "A")
	mustConfirm(t, s, la.Token, 1)
	mustConfirm(t, s, la.Token, 2)
	mustAcquire(t, s, "batch-B", 5, 8, "B")
	before := s.LogSize()

	if err := s.Compact(); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if after := s.LogSize(); after >= before {
		t.Fatalf("compaction did not shrink log: before=%d after=%d", before, after)
	}
	if s.Confirmed() != 2 {
		t.Fatalf("confirmed lost across compaction: %d", s.Confirmed())
	}
	if id, ok := s.Identify(6); !ok || id != "batch-B" {
		t.Fatalf("identity after compaction: %q %v", id, ok)
	}

	mustConfirm(t, s, la.Token, 3)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := reopenStore(t, dir)
	defer s2.Close()
	if s2.Confirmed() != 3 || len(s2.Leases()) != 2 {
		t.Fatalf("state after compact+reopen wrong: confirmed=%d leases=%d",
			s2.Confirmed(), len(s2.Leases()))
	}
	if id, ok := s2.Identify(1); !ok || id != "batch-A" {
		t.Fatalf("identity after compact+reopen: %q %v", id, ok)
	}
	persisted, _ := s2.Lease("batch-B")
	if string(persisted.Summary.Hash) != string(h("B")) {
		t.Fatalf("batch-B summary hash changed: %q", persisted.Summary.Hash)
	}
}

// Faults before the atomic rename leave the committed log fully usable and
// only abandon the temporary file.
func TestCompactionInterruptedBeforeRename(t *testing.T) {
	for _, faultOp := range []string{OpCreate, OpWrite, OpSync} {
		t.Run(faultOp, func(t *testing.T) {
			dir := t.TempDir()
			compacting := false
			hook := func(op string) error {
				if compacting && op == faultOp {
					return errors.New("injected pre-rename fault")
				}
				return nil
			}
			s := openStore(t, dir, hook)
			la := mustAcquire(t, s, "batch-A", 1, 3, "A")
			mustConfirm(t, s, la.Token, 1)

			compacting = true
			if err := s.Compact(); err == nil {
				t.Fatal("expected compaction fault")
			}
			compacting = false
			if s.failed {
				t.Fatal("pre-rename compaction fault must not poison the store")
			}
			if s.Confirmed() != 1 || s.LogSize() == 0 {
				t.Fatal("committed log damaged by failed compaction")
			}
			if id, ok := s.Identify(2); !ok || id != "batch-A" {
				t.Fatalf("identity after failed compact: %q %v", id, ok)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}

			s2 := reopenStore(t, dir)
			defer s2.Close()
			if _, err := os.Stat(filepath.Join(dir, tmpName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stale temp not removed on reopen: %v", err)
			}
			if s2.Confirmed() != 1 {
				t.Fatalf("confirmed after interrupted compact reopen: %d", s2.Confirmed())
			}
			if id, ok := s2.Identify(3); !ok || id != "batch-A" {
				t.Fatalf("identity after interrupted compact reopen: %q %v", id, ok)
			}
		})
	}
}

// A fault at rename/dir-sync happens around the atomic replace: either the
// old log or the new snapshot is on disk, both are valid, and reopening
// recovers a consistent state preserving every committed fact.
func TestCompactionInterruptedAtReplace(t *testing.T) {
	for _, faultOp := range []string{OpRename, OpDirSync} {
		t.Run(faultOp, func(t *testing.T) {
			dir := t.TempDir()
			s := openStore(t, dir, nil)
			la := mustAcquire(t, s, "batch-A", 1, 3, "A")
			mustConfirm(t, s, la.Token, 1)

			s.hook = func(op string) error {
				if op == faultOp {
					return errors.New("injected replace fault")
				}
				return nil
			}
			if err := s.Compact(); err == nil {
				t.Fatal("expected replace fault")
			}
			if !s.failed {
				t.Fatal("replace-stage fault must poison the open store")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}

			s2 := reopenStore(t, dir)
			defer s2.Close()
			if s2.Confirmed() != 1 {
				t.Fatalf("confirmed lost across replace fault: %d", s2.Confirmed())
			}
			if id, ok := s2.Identify(2); !ok || id != "batch-A" {
				t.Fatalf("identity across replace fault: %q %v", id, ok)
			}
			if la2, ok := s2.Lease("batch-A"); !ok || la2.Token != la.Token {
				t.Fatalf("lease token changed across replace fault: %+v", la2)
			}
		})
	}
}

// A leftover temp file from an externally killed compaction is discarded on
// Open even when no fault hook is configured.
func TestStaleTempRemovedOnOpen(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	l := mustAcquire(t, s, "batch-A", 1, 2, "A")
	mustConfirm(t, s, l.Token, 1)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tmpName), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2 := reopenStore(t, dir)
	defer s2.Close()
	if _, err := os.Stat(filepath.Join(dir, tmpName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temp survived Open: %v", err)
	}
	if s2.Confirmed() != 1 {
		t.Fatalf("state lost with stale temp present: %d", s2.Confirmed())
	}
}
