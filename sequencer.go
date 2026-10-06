package resample

// Fragment is a piece of the input stream carrying the absolute index of
// its first sample. Fragments may arrive out of order, may overlap, and may
// be delivered more than once.
type Fragment struct {
	Start   int64
	Samples []float64
}

// Sequencer reassembles fragments into the contiguous confirmed prefix of
// the stream. Only samples extending the confirmed prefix are released;
// gaps are never filled with zeros or any other placeholder — samples
// beyond a gap are held until the gap is closed by later fragments.
//
// Memory: pending holds only samples that arrived ahead of a gap, so for
// in-order delivery Pending() stays at zero; usage is bounded by the
// reordering window, not by the confirmed stream length.
type Sequencer struct {
	next    int64
	pending map[int64]float64
}

// NewSequencer returns a Sequencer expecting the stream to start at
// absolute index 0.
func NewSequencer() *Sequencer {
	return &Sequencer{pending: make(map[int64]float64)}
}

// Add ingests a fragment and returns the newly confirmed contiguous prefix
// (possibly empty). Duplicate or already-confirmed samples are ignored;
// conflicting duplicates are overwritten by the latest delivery.
func (s *Sequencer) Add(f Fragment) []float64 {
	for j, v := range f.Samples {
		idx := f.Start + int64(j)
		if idx < s.next {
			continue
		}
		s.pending[idx] = v
	}
	var ready []float64
	for {
		v, ok := s.pending[s.next]
		if !ok {
			break
		}
		delete(s.pending, s.next)
		s.next++
		ready = append(ready, v)
	}
	return ready
}

// Next returns the absolute index of the first unconfirmed sample, i.e. the
// length of the confirmed prefix.
func (s *Sequencer) Next() int64 { return s.next }

// Pending returns the number of buffered samples held behind a gap.
func (s *Sequencer) Pending() int { return len(s.pending) }

// SequencerState is a restorable snapshot of a Sequencer.
type SequencerState struct {
	Next    int64
	Pending map[int64]float64
}

// Checkpoint returns a deep copy of the sequencer's state.
func (s *Sequencer) Checkpoint() SequencerState {
	pending := make(map[int64]float64, len(s.pending))
	for k, v := range s.pending {
		pending[k] = v
	}
	return SequencerState{Next: s.next, Pending: pending}
}

// RestoreSequencer rebuilds a Sequencer from a checkpoint. The state is
// deep-copied, so the caller may keep using or mutating it afterwards.
func RestoreSequencer(st SequencerState) *Sequencer {
	s := NewSequencer()
	s.next = st.Next
	for k, v := range st.Pending {
		s.pending[k] = v
	}
	return s
}
