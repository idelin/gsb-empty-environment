package resample

// Sequencer reassembles a contiguous, in-order sample stream from input
// fragments that carry their original sample indices and may arrive out of
// order, duplicated, or overlapping.
//
// Only the contiguous confirmed prefix is released: if index k is missing,
// samples after k are held back instead of being filled with zeros. Held
// samples are exactly those past the first gap, so memory is bounded by the
// reordering window, not by the stream length.
type Sequencer struct {
	next    int64             // first index not yet confirmed
	pending map[int64]float64 // received samples at indices >= next
}

// NewSequencer returns a Sequencer expecting the stream to start at the
// given index (usually 0).
func NewSequencer(start int64) *Sequencer {
	return &Sequencer{next: start, pending: make(map[int64]float64)}
}

// Add ingests a fragment: len(p) consecutive samples whose first sample has
// absolute index start. It returns the newly confirmed contiguous prefix
// (possibly empty), in stream order. Samples that are duplicates, stale
// (already confirmed), or still blocked by a gap are not emitted.
func (s *Sequencer) Add(start int64, p []float64) []float64 {
	for j, v := range p {
		idx := start + int64(j)
		if idx < s.next {
			continue // stale: already confirmed
		}
		if _, dup := s.pending[idx]; dup {
			continue // duplicate: first copy wins
		}
		s.pending[idx] = v
	}
	var out []float64
	for {
		v, ok := s.pending[s.next]
		if !ok {
			break
		}
		delete(s.pending, s.next)
		out = append(out, v)
		s.next++
	}
	return out
}

// Buffered reports how many samples are held back behind the first gap.
func (s *Sequencer) Buffered() int { return len(s.pending) }

// Next reports the first index that has not been confirmed yet.
func (s *Sequencer) Next() int64 { return s.next }
