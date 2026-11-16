package netsec

import (
	"reflect"
	"testing"
)

func TestOutOfOrder(t *testing.T) {
	s := NewStream(FirstWins)
	if c := s.Add(1000, []byte("hello ")); len(c) != 0 {
		t.Fatalf("unexpected conflicts: %v", c)
	}
	s.Add(1012, []byte("gopher"))
	s.Add(1006, []byte("world "))
	if got := string(s.Bytes()); got != "hello world gopher" {
		t.Fatalf("got %q", got)
	}
}

func TestConsistentOverlapNoAlert(t *testing.T) {
	for _, p := range []Policy{FirstWins, LastWins} {
		s := NewStream(p)
		s.Add(1000, []byte("hello world"))
		if c := s.Add(1006, []byte("world!")); len(c) != 0 {
			t.Fatalf("policy %v: identical overlap raised %v", p, c)
		}
		if got := string(s.Bytes()); got != "hello world!" {
			t.Fatalf("policy %v: got %q", p, got)
		}
	}
}

func TestConflictingOverlap(t *testing.T) {
	first := NewStream(FirstWins)
	first.Add(1000, []byte("AAAA"))
	c := first.Add(1002, []byte("BB"))
	if want := []Conflict{{Start: 1002, End: 1004}}; !reflect.DeepEqual(c, want) {
		t.Fatalf("first-wins conflicts = %v, want %v", c, want)
	}
	if got := string(first.Bytes()); got != "AAAA" {
		t.Fatalf("first-wins stream = %q", got)
	}

	last := NewStream(LastWins)
	last.Add(1000, []byte("AAAA"))
	c = last.Add(1002, []byte("BB"))
	if want := []Conflict{{Start: 1002, End: 1004}}; !reflect.DeepEqual(c, want) {
		t.Fatalf("last-wins conflicts = %v, want %v", c, want)
	}
	if got := string(last.Bytes()); got != "AABB" {
		t.Fatalf("last-wins stream = %q", got)
	}
}

func TestDisjointConflictRanges(t *testing.T) {
	s := NewStream(FirstWins)
	s.Add(100, []byte("abcdef"))
	c := s.Add(101, []byte("XcXe"))
	want := []Conflict{{Start: 101, End: 102}, {Start: 103, End: 104}}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("conflicts = %v, want %v", c, want)
	}
}

func TestWraparound(t *testing.T) {
	s := NewStream(FirstWins)
	s.Add(0xFFFFFFFE, []byte("ab"))
	s.Add(0, []byte("cd")) // wraps past 2^32-1
	if got := string(s.Bytes()); got != "abcd" {
		t.Fatalf("got %q", got)
	}

	// Retransmit across the wrap boundary with one differing byte.
	c := s.Add(0xFFFFFFFF, []byte("bX"))
	want := []Conflict{{Start: 0, End: 1}}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("conflicts = %v, want %v", c, want)
	}
	if got := string(s.Bytes()); got != "abcd" {
		t.Fatalf("first-wins stream = %q", got)
	}
}
