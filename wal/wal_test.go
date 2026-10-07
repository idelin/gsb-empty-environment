package wal_test

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"inbox"
	"inbox/wal"
)

// A crash mid-write leaves a torn tail; recovery must truncate it and keep
// all committed records.
func TestTornTailIsTruncated(t *testing.T) {
	dir, err := ioutil.TempDir("", "wal-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "inbox.wal")

	s1, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := inbox.NewProcessor(s1)
	ev := inbox.Event{ID: "ev-1", Payload: []byte("a")}
	if applied, err := p.Process(ev, func(tx inbox.Tx, ev inbox.Event) ([]inbox.Output, error) {
		return []inbox.Output{{Payload: []byte("x")}}, nil
	}); err != nil || !applied {
		t.Fatalf("process: applied=%v err=%v", applied, err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate a torn write: half a record header plus garbage.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0, 0, 0, 42, 1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s2, err := wal.Open(path)
	if err != nil {
		t.Fatalf("reopen after torn tail: %v", err)
	}
	defer s2.Close()
	pending, err := s2.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "ev-1#0" {
		t.Fatalf("pending after recovery = %+v", pending)
	}
	// The store keeps working after recovery.
	p2 := inbox.NewProcessor(s2)
	if applied, err := p2.Process(inbox.Event{ID: "ev-2"}, func(tx inbox.Tx, ev inbox.Event) ([]inbox.Output, error) {
		return []inbox.Output{{Payload: []byte("y")}}, nil
	}); err != nil || !applied {
		t.Fatalf("process after recovery: applied=%v err=%v", applied, err)
	}
	if pending, _ := s2.Pending(); len(pending) != 2 {
		t.Fatalf("pending = %d, want 2", len(pending))
	}
}
