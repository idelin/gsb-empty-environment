package netsec

// Policy selects how overlapping TCP bytes are resolved, mirroring the
// target host's reassembly behavior so the sensor rebuilds the same
// stream the victim will see.
type Policy int

const (
	// FirstWins keeps the bytes of the segment that arrived first.
	FirstWins Policy = iota
	// LastWins keeps the bytes of the segment that arrived last.
	LastWins
)

// Conflict is a half-open wire sequence range [Start, End) where
// overlapping segments carried different bytes. End may wrap past
// 2^32-1 to 0 like any TCP sequence number.
type Conflict struct {
	Start uint32
	End   uint32
}

// Stream reassembles one direction of a TCP flow.
type Stream struct {
	policy Policy
	base   uint32 // wire sequence number of absolute offset 0
	init   bool
	data   map[int64]byte
	min    int64
	max    int64
}

// NewStream returns a Stream that resolves overlaps with policy.
func NewStream(policy Policy) *Stream {
	return &Stream{policy: policy, data: make(map[int64]byte)}
}

// abs maps a wire sequence number to an absolute offset relative to the
// stream base using signed 32-bit arithmetic, so comparisons stay
// correct across the 2^32 wrap boundary.
func (s *Stream) abs(seq uint32) int64 {
	return int64(int32(seq - s.base))
}

// wire maps an absolute offset back to a wire sequence number.
func (s *Stream) wire(pos int64) uint32 {
	return s.base + uint32(pos)
}

// Add ingests one segment in arrival order. It returns the conflict
// ranges, in wire sequence numbers, where payload bytes disagreed with
// bytes already seen; identical overlaps are not conflicts.
func (s *Stream) Add(seq uint32, payload []byte) []Conflict {
	if len(payload) == 0 {
		return nil
	}
	if !s.init {
		s.base = seq
		s.init = true
		s.min = 0
		s.max = -1
	}
	start := s.abs(seq)
	var conflicts []Conflict
	for i, b := range payload {
		pos := start + int64(i)
		if old, ok := s.data[pos]; ok {
			if old != b {
				conflicts = appendConflict(conflicts, s.wire(pos))
			}
			if s.policy == LastWins {
				s.data[pos] = b
			}
		} else {
			s.data[pos] = b
		}
		if pos < s.min {
			s.min = pos
		}
		if pos > s.max {
			s.max = pos
		}
	}
	return conflicts
}

// appendConflict merges seq into the trailing range when contiguous,
// otherwise starts a new range.
func appendConflict(cs []Conflict, seq uint32) []Conflict {
	if n := len(cs); n > 0 && cs[n-1].End == seq {
		cs[n-1].End = seq + 1
		return cs
	}
	return append(cs, Conflict{Start: seq, End: seq + 1})
}

// Bytes returns the reassembled stream starting at the lowest sequence
// number seen, stopping at the first gap.
func (s *Stream) Bytes() []byte {
	if !s.init {
		return nil
	}
	out := make([]byte, 0, s.max-s.min+1)
	for pos := s.min; pos <= s.max; pos++ {
		b, ok := s.data[pos]
		if !ok {
			break
		}
		out = append(out, b)
	}
	return out
}
