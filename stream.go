package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Conflict records one byte position where a retransmitted/overlapping
// segment disagreed with bytes already accepted. First-seen bytes win.
type Conflict struct {
	Seq      int
	Existing byte
	Incoming byte
}

// interval is a contiguous run of received bytes covering
// [start, start+len(data)).
type interval struct {
	start int
	data  []byte
}

func (iv interval) end() int { return iv.start + len(iv.data) }

// Stream reassembles one direction of one connection epoch.
type Stream struct {
	base      int // first expected sequence number (from SYN, or first segment)
	baseSet   bool
	ivals     []interval // sorted, disjoint, non-adjacent
	hasFin    bool
	finSeq    int // sequence number of FIN; stream data ends at finSeq
	conflicts []Conflict
}

func (s *Stream) SetBase(seq int) {
	if !s.baseSet {
		s.base, s.baseSet = seq, true
	}
}

func (s *Stream) SetFin(seq int) {
	if !s.hasFin {
		s.hasFin, s.finSeq = true, seq
		if !s.baseSet {
			s.base, s.baseSet = seq, true
		}
	}
}

// Highest returns the highest sequence number covered by received data.
func (s *Stream) Highest() int {
	hi := s.base
	for _, iv := range s.ivals {
		if iv.end() > hi {
			hi = iv.end()
		}
	}
	return hi
}

// Insert adds a segment. Overlapping bytes that agree are harmless
// retransmissions; overlapping bytes that disagree are recorded as
// conflicts and the first-seen bytes are kept. Never overwrites.
func (s *Stream) Insert(seq int, data []byte) {
	if len(data) == 0 {
		return
	}
	if !s.baseSet {
		s.base, s.baseSet = seq, true
	}
	end := seq + len(data)
	cursor := seq
	done := false
	result := make([]interval, 0, len(s.ivals)+1)
	for _, iv := range s.ivals {
		ivEnd := iv.end()
		if ivEnd <= cursor { // entirely before the uncovered part
			result = append(result, iv)
			continue
		}
		if iv.start >= end { // entirely after the new segment
			if !done {
				if cursor < end {
					result = append(result, interval{cursor, dup(data[cursor-seq:])})
				}
				done = true
			}
			result = append(result, iv)
			continue
		}
		// [cursor,end) overlaps [iv.start,ivEnd)
		if cursor < iv.start { // uncovered prefix of the new segment
			result = append(result, interval{cursor, dup(data[cursor-seq : iv.start-seq])})
		}
		lo := maxInt(cursor, iv.start)
		hi := minInt(end, ivEnd)
		for p := lo; p < hi; p++ {
			existing := iv.data[p-iv.start]
			incoming := data[p-seq]
			if existing != incoming {
				s.conflicts = append(s.conflicts, Conflict{Seq: p, Existing: existing, Incoming: incoming})
			}
		}
		result = append(result, iv) // keep first-seen bytes
		cursor = ivEnd
		if cursor >= end {
			done = true
		}
	}
	if !done && cursor < end {
		result = append(result, interval{cursor, dup(data[cursor-seq:])})
	}
	s.ivals = mergeAdjacent(result)
}

// Render formats the reassembled stream. Missing ranges are emitted as
// [GAP n] markers; bytes are never invented to fill holes.
func (s *Stream) Render() string {
	if !s.baseSet {
		return "(no data)"
	}
	limit := s.Highest()
	if s.hasFin {
		limit = s.finSeq
	}
	var b strings.Builder
	pos := s.base
	for _, iv := range s.ivals {
		start, data := iv.start, iv.data
		if start+len(data) <= pos {
			continue
		}
		if start < pos {
			data = data[pos-start:]
			start = pos
		}
		if start >= limit {
			break
		}
		if start > pos {
			fmt.Fprintf(&b, "[GAP %d]", start-pos)
		}
		if start+len(data) > limit {
			data = data[:limit-start]
		}
		b.WriteString(strconv.Quote(string(data)))
		pos = start + len(data)
	}
	if pos < limit {
		fmt.Fprintf(&b, "[GAP %d]", limit-pos)
	}
	if b.Len() == 0 {
		return "(empty)"
	}
	return b.String()
}

func mergeAdjacent(ivals []interval) []interval {
	out := ivals[:0]
	for _, iv := range ivals {
		if n := len(out); n > 0 && out[n-1].end() == iv.start {
			out[n-1].data = append(out[n-1].data, iv.data...)
			continue
		}
		out = append(out, iv)
	}
	return out
}

func dup(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
