package batchlease

import (
	"strings"
	"testing"
)

func TestExclusiveDirLock(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	defer s.Close()

	_, err := Open(dir)
	if err == nil {
		t.Fatal("second Open on a locked directory must fail")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("unexpected lock error: %v", err)
	}
}

func TestReopenEmptyLog(t *testing.T) {
	dir := t.TempDir()
	s1 := openStore(t, dir, nil)
	e1 := s1.Epoch()
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := reopenStore(t, dir)
	defer s2.Close()
	if s2.Epoch() <= e1 {
		t.Fatalf("epoch must increase on every open: %d <= %d", s2.Epoch(), e1)
	}
	if s2.Confirmed() != 0 || len(s2.Leases()) != 0 {
		t.Fatal("empty log replay produced state")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err == nil {
		t.Fatal("second Close must report ErrClosed")
	}
}
