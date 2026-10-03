package store

import (
	"path/filepath"
	"testing"

	"aries/wal"
)

func newStore(t *testing.T) (*Store, *wal.WAL) {
	t.Helper()
	w, err := wal.Open(filepath.Join(t.TempDir(), "s.wal"))
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	return New(w), w
}

func TestCrashLosesUnflushedPages(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Write(1, 0, []byte("data"), 10); err != nil {
		t.Fatalf("write: %v", err)
	}
	s.Crash()
	data, lsn := s.DiskPage(1)
	if lsn != 0 || string(data[:4]) == "data" {
		t.Fatalf("unflushed page survived crash: lsn=%d", lsn)
	}
}

func TestFlushPersistsAcrossCrash(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Write(1, 0, []byte("data"), 10); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := s.FlushPage(1); err != nil {
		t.Fatalf("flush: %v", err)
	}
	s.Crash()
	data, lsn := s.DiskPage(1)
	if string(data[:4]) != "data" || lsn != 10 {
		t.Fatalf("flushed page lost: data=%q lsn=%d", data[:4], lsn)
	}
}

// TestWALRule: flushing a dirty page must first make the log durable
// up to the page's LSN.
func TestWALRuleEnforced(t *testing.T) {
	s, w := newStore(t)
	lsn, err := w.Append(&wal.Record{Type: wal.RecUpdate, TxnID: 1, PageID: 1, After: []byte("x")})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Write(1, 0, []byte("x"), lsn); err != nil {
		t.Fatalf("write: %v", err)
	}
	if w.FlushedLSN() > lsn {
		t.Fatalf("test setup: log already flushed")
	}
	if err := s.FlushPage(1); err != nil {
		t.Fatalf("flush page: %v", err)
	}
	if w.FlushedLSN() <= lsn {
		t.Fatalf("WAL rule violated: pageLSN %d not covered by flushedLSN %d",
			lsn, w.FlushedLSN())
	}
}

func TestWriteOutOfRange(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Write(1, PageSize, []byte("x"), 1); err == nil {
		t.Fatalf("expected out-of-range error")
	}
	if err := s.Write(1, -1, []byte("x"), 1); err == nil {
		t.Fatalf("expected negative-offset error")
	}
}
