package main

import "testing"

func feed(s *Stream, seq int, data string) {
	s.Insert(seq, []byte(data))
}

func TestInsertInOrderMerges(t *testing.T) {
	var s Stream
	s.SetBase(1)
	feed(&s, 1, "hello")
	feed(&s, 6, "world")
	if got := s.Render(); got != `"helloworld"` {
		t.Fatalf("render = %s", got)
	}
}

func TestInsertOutOfOrderMerges(t *testing.T) {
	var s Stream
	s.SetBase(1)
	feed(&s, 6, "world")
	feed(&s, 1, "hello")
	if got := s.Render(); got != `"helloworld"` {
		t.Fatalf("render = %s", got)
	}
}

func TestRetransmissionIdenticalNoConflict(t *testing.T) {
	var s Stream
	s.SetBase(101)
	feed(&s, 101, "hello")
	feed(&s, 101, "hello") // exact retransmission
	feed(&s, 103, "llo")   // fully covered retransmission
	if len(s.conflicts) != 0 {
		t.Fatalf("conflicts = %v", s.conflicts)
	}
	if got := s.Render(); got != `"hello"` {
		t.Fatalf("render = %s", got)
	}
}

func TestOverlapConflictKeepsFirstSeen(t *testing.T) {
	var s Stream
	s.SetBase(101)
	feed(&s, 101, "hello")
	feed(&s, 104, "LO") // disagrees at 104 ('l' vs 'L') and 105 ('o' vs 'O')
	if len(s.conflicts) != 2 {
		t.Fatalf("conflicts = %v", s.conflicts)
	}
	c := s.conflicts[0]
	if c.Seq != 104 || c.Existing != 'l' || c.Incoming != 'L' {
		t.Fatalf("conflict[0] = %+v", c)
	}
	if got := s.Render(); got != `"hello"` {
		t.Fatalf("first-seen bytes must win, render = %s", got)
	}
}

func TestPartialOverlapConflictAndExtension(t *testing.T) {
	var s Stream
	s.SetBase(1)
	feed(&s, 1, "abcd")
	feed(&s, 3, "cXefg") // agrees at 3, conflicts at 4, extends 5..7
	if len(s.conflicts) != 1 || s.conflicts[0].Seq != 4 {
		t.Fatalf("conflicts = %v", s.conflicts)
	}
	if got := s.Render(); got != `"abcdefg"` {
		t.Fatalf("render = %s", got)
	}
}

func TestGapRenderedNotFilled(t *testing.T) {
	var s Stream
	s.SetBase(101)
	feed(&s, 101, "hello")
	feed(&s, 108, "end") // [106,108) missing
	if got := s.Render(); got != `"hello"[GAP 2]"end"` {
		t.Fatalf("render = %s", got)
	}
}

func TestGapUpToFin(t *testing.T) {
	var s Stream
	s.SetBase(1)
	feed(&s, 1, "ab")
	s.SetFin(5) // bytes 3..4 never arrived
	if got := s.Render(); got != `"ab"[GAP 2]` {
		t.Fatalf("render = %s", got)
	}
}

func TestDataBeyondFinNotRendered(t *testing.T) {
	var s Stream
	s.SetBase(1)
	feed(&s, 1, "hello")
	s.SetFin(4)
	if got := s.Render(); got != `"hel"` {
		t.Fatalf("render = %s", got)
	}
}

func TestInsertSpanningMultipleIntervals(t *testing.T) {
	var s Stream
	s.SetBase(0)
	feed(&s, 0, "ab")
	feed(&s, 4, "ef")
	feed(&s, 0, "abcdef") // bridges the gap, agrees everywhere
	if len(s.conflicts) != 0 {
		t.Fatalf("conflicts = %v", s.conflicts)
	}
	if got := s.Render(); got != `"abcdef"` {
		t.Fatalf("render = %s", got)
	}
}

func TestEmptyStream(t *testing.T) {
	var s Stream
	if got := s.Render(); got != "(no data)" {
		t.Fatalf("render = %s", got)
	}
	s.SetBase(10)
	s.SetFin(10)
	if got := s.Render(); got != "(empty)" {
		t.Fatalf("render = %s", got)
	}
}
