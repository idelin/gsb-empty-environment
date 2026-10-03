package store

import "testing"

func TestFencingTokenRejectsStaleWrites(t *testing.T) {
	s := New()

	if err := s.Put("k", "v1", 1); err != nil {
		t.Fatalf("first write should succeed: %v", err)
	}
	s.SetFence(2) // a higher epoch was observed (takeover happened)

	if err := s.Put("k", "v2", 1); err != ErrStaleFencingToken {
		t.Fatalf("stale token write must be rejected, got %v", err)
	}
	if v, _ := s.Get("k"); v != "v1" {
		t.Fatalf("stale write must not modify data, got %q", v)
	}
	if err := s.Put("k", "v3", 2); err != nil {
		t.Fatalf("write with current epoch should succeed: %v", err)
	}
	if err := s.Put("k", "v4", 0); err != ErrStaleFencingToken {
		t.Fatalf("token 0 (no lease) must be rejected, got %v", err)
	}
}

func TestFenceIsMonotonic(t *testing.T) {
	s := New()
	s.SetFence(5)
	s.SetFence(3)
	if s.Fence() != 5 {
		t.Fatalf("fence must never decrease, got %d", s.Fence())
	}
}
