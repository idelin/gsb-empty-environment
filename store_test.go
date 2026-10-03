package roaring

import (
	"errors"
	"testing"
)

func mustAdd(t *testing.T, s *Store, key uint16, vals ...uint16) {
	t.Helper()
	if err := s.Add(key, vals...); err != nil {
		t.Fatalf("add: %v", err)
	}
}

func mustAddRange(t *testing.T, s *Store, key uint16, lo, hi uint16) {
	t.Helper()
	if err := s.AddRange(key, lo, hi); err != nil {
		t.Fatalf("addrange: %v", err)
	}
}

func TestRecoverAfterCleanShutdown(t *testing.T) {
	fs := NewSimFS()
	s, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, 1, 1, 2, 3)
	mustAddRange(t, s, 7, 100, 2000)
	want := s.Bitmap()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	if !want.Equals(s2.Bitmap()) {
		t.Fatal("state mismatch after clean shutdown")
	}
}

func TestRecoverAfterPowerLoss(t *testing.T) {
	fs := NewSimFS()
	s, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, 1, 1, 2, 3)
	mustAddRange(t, s, 7, 100, 2000)
	mustAdd(t, s, 9, 65535, 0, 12345)
	want := s.Bitmap()

	// Sudden power loss. Every committed op was fsynced, so nothing is lost.
	// (The old store process is "dead": we deliberately do not Close it.)
	fs.Crash()

	s2, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	if !want.Equals(s2.Bitmap()) {
		t.Fatal("state mismatch after power loss")
	}
}

func TestRecoverAfterPowerLossAcrossCheckpoint(t *testing.T) {
	fs := NewSimFS()
	s, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, 1, 10, 20, 30)
	mustAddRange(t, s, 4, 0, 5000)
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	// WAL must be empty right after a checkpoint.
	if data, err := fs.ReadFile(walFile); err != nil && !errors.Is(err, errNotExist) {
		t.Fatal(err)
	} else if err == nil && len(data) != 0 {
		t.Fatalf("WAL not reset after checkpoint (%d bytes)", len(data))
	}
	mustAdd(t, s, 5, 5, 6, 7)
	mustAddRange(t, s, 6, 100, 200)
	want := s.Bitmap()

	fs.Crash()

	s2, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	if !want.Equals(s2.Bitmap()) {
		t.Fatal("state mismatch after crash across checkpoint")
	}
}

func TestTornWALTailIsDiscarded(t *testing.T) {
	for _, mode := range []string{"partial-record", "bad-crc"} {
		t.Run(mode, func(t *testing.T) {
			fs := NewSimFS()
			s, err := OpenStore(fs)
			if err != nil {
				t.Fatal(err)
			}
			mustAdd(t, s, 1, 10, 20)
			mustAddRange(t, s, 2, 5, 500)
			want := s.Bitmap()

			// A record half-flushed to a sector when power failed.
			torn := frameRecord(encodeAddRecord(3, []uint16{1, 2, 3}))
			if mode == "partial-record" {
				torn = torn[:len(torn)-3]
			} else {
				torn[4] ^= 0xff // corrupt the CRC field
			}
			fs.SimulateTornWrite(walFile, torn)
			fs.Crash()

			s2, err := OpenStore(fs)
			if err != nil {
				t.Fatal(err)
			}
			if !want.Equals(s2.Bitmap()) {
				t.Fatal("torn tail leaked into recovered state")
			}
			// The store must remain usable: the WAL was truncated to the
			// valid prefix, so new appends land at a clean offset.
			mustAdd(t, s2, 4, 4, 4)
			wantAfter := s2.Bitmap()
			fs.Crash()
			s3, err := OpenStore(fs)
			if err != nil {
				t.Fatal(err)
			}
			if !wantAfter.Equals(s3.Bitmap()) {
				t.Fatal("store not usable after torn-tail recovery")
			}
		})
	}
}

func TestDiskFullRollback(t *testing.T) {
	fs := NewSimFS()
	s, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, 1, 1, 2, 3)
	mustAddRange(t, s, 2, 10, 100)
	before := s.Bitmap()

	fs.DiskFull = true
	if err := s.Add(1, 100); !errors.Is(err, ErrDiskFull) {
		t.Fatalf("expected ErrDiskFull, got %v", err)
	}
	if err := s.AddRange(2, 200, 300); !errors.Is(err, ErrDiskFull) {
		t.Fatalf("expected ErrDiskFull, got %v", err)
	}
	// Failed operations must leave in-memory state untouched.
	if !before.Equals(s.Bitmap()) {
		t.Fatal("in-memory state changed after failed add")
	}
	// Checkpoint under disk-full must fail without damaging old state.
	if err := s.Checkpoint(); !errors.Is(err, ErrDiskFull) {
		t.Fatalf("expected ErrDiskFull from checkpoint, got %v", err)
	}
	if _, err := fs.ReadFile(tmpFile); !errors.Is(err, errNotExist) {
		t.Fatal("temporary snapshot left behind after failed checkpoint")
	}

	// Self-healing: once the disk has space again, everything works and
	// recovery replays exactly the successful operations.
	fs.DiskFull = false
	mustAdd(t, s, 1, 100)
	want := s.Bitmap()
	fs.Crash()
	s2, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	if !want.Equals(s2.Bitmap()) {
		t.Fatal("recovery mismatch after disk-full rollback")
	}
}

func TestCheckpointCrashWindows(t *testing.T) {
	build := func(t *testing.T) (*SimFS, *Store) {
		t.Helper()
		fs := NewSimFS()
		s, err := OpenStore(fs)
		if err != nil {
			t.Fatal(err)
		}
		mustAdd(t, s, 1, 1, 2, 3)
		mustAddRange(t, s, 2, 10, 1000)
		return fs, s
	}

	t.Run("crash after tmp write before rename", func(t *testing.T) {
		fs, s := build(t)
		want := s.Bitmap()
		// First half of a checkpoint: tmp snapshot written and synced.
		f, err := fs.OpenFile(tmpFile, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(marshalSnapshot(s.bm)); err != nil {
			t.Fatal(err)
		}
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
		fs.Crash() // power lost before the rename
		s2, err := OpenStore(fs)
		if err != nil {
			t.Fatal(err)
		}
		if !want.Equals(s2.Bitmap()) {
			t.Fatal("state mismatch: old snapshot + WAL should be intact")
		}
	})

	t.Run("crash after rename before wal reset", func(t *testing.T) {
		fs, s := build(t)
		want := s.Bitmap()
		f, err := fs.OpenFile(tmpFile, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(marshalSnapshot(s.bm)); err != nil {
			t.Fatal(err)
		}
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
		if err := fs.Rename(tmpFile, snapshotFile); err != nil {
			t.Fatal(err)
		}
		fs.Crash() // power lost before the WAL was reset
		s2, err := OpenStore(fs)
		if err != nil {
			t.Fatal(err)
		}
		// WAL records are replayed on top of the new snapshot; adds are
		// idempotent so the result is still correct.
		if !want.Equals(s2.Bitmap()) {
			t.Fatal("state mismatch: replay after rename should be idempotent")
		}
	})
}

func TestCorruptSnapshotDetected(t *testing.T) {
	fs := NewSimFS()
	s, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, 1, 1, 2, 3)
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	fs.CorruptCommitted(snapshotFile)
	if _, err := OpenStore(fs); err == nil {
		t.Fatal("expected error for corrupted snapshot")
	}
}

func TestStoreOnRealFilesystem(t *testing.T) {
	dir := t.TempDir()
	fs := OSFS{Dir: dir}
	s, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, 1, 10, 20)
	mustAddRange(t, s, 3, 0, 999)
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, 4, 4)
	want := s.Bitmap()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(fs)
	if err != nil {
		t.Fatal(err)
	}
	if !want.Equals(s2.Bitmap()) {
		t.Fatal("state mismatch on real filesystem")
	}
}
