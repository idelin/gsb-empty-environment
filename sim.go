package tsxform

import (
	"container/heap"
	"fmt"
	"math"
)

// Record is a fragment that has been transmitted on the link. Depart is the
// tick transmission started, Done the tick it completed; all times are
// integer ticks of the discrete event model.
type Record struct {
	Fragment
	Depart int64
	Done   int64
}

// fragHeap orders pending arrivals by (IdealTick, Seq, FragIndex) so that
// cross-stream merges break ideal-time ties by declared input order.
type fragHeap []Fragment

func (h fragHeap) Len() int { return len(h) }
func (h fragHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.IdealTick != b.IdealTick {
		return a.IdealTick < b.IdealTick
	}
	if a.Seq != b.Seq {
		return a.Seq < b.Seq
	}
	return a.FragIndex < b.FragIndex
}
func (h fragHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *fragHeap) Push(x interface{}) { *h = append(*h, x.(Fragment)) }
func (h *fragHeap) Pop() interface{} {
	old := *h
	n := len(old)
	f := old[n-1]
	*h = old[:n-1]
	return f
}

type txState struct {
	frag   Fragment
	depart int64
	done   int64
}

// Sim is a discrete event model of one FIFO link fed by a priority merge of
// the scaled streams. The link transmits one fragment at a time; a fragment
// of Size bytes occupies the link for ceil(Size/BytesPerTick) ticks.
type Sim struct {
	bw       int64
	now      int64
	arrivals fragHeap
	queue    []Fragment
	inflight *txState
	out      []Record
}

// NewSim builds a link with the given bandwidth in bytes per tick.
func NewSim(bytesPerTick int64) (*Sim, error) {
	if bytesPerTick <= 0 {
		return nil, fmt.Errorf("sim: bytes-per-tick must be positive, got %d", bytesPerTick)
	}
	return &Sim{bw: bytesPerTick}, nil
}

// Submit schedules a fragment arrival at its ideal tick.
func (s *Sim) Submit(f Fragment) { heap.Push(&s.arrivals, f) }

// Now returns the current simulation tick.
func (s *Sim) Now() int64 { return s.now }

// Out returns the transmitted records in departure order.
func (s *Sim) Out() []Record { return s.out }

func (s *Sim) start(f Fragment) {
	if f.IdealTick > s.now {
		s.now = f.IdealTick
	}
	dur := (f.Size + s.bw - 1) / s.bw
	s.inflight = &txState{frag: f, depart: s.now, done: s.now + dur}
}

// RunTo advances the model up to tick end. Callers must have submitted
// every arrival with IdealTick <= end before calling.
func (s *Sim) RunTo(end int64) {
	for {
		if s.inflight == nil && len(s.queue) > 0 {
			f := s.queue[0]
			s.queue = s.queue[1:]
			s.start(f)
			continue
		}
		nextArr := int64(-1)
		if len(s.arrivals) > 0 {
			nextArr = s.arrivals[0].IdealTick
		}
		if s.inflight == nil {
			if nextArr < 0 || nextArr > end {
				if end > s.now {
					s.now = end
				}
				return
			}
			s.start(heap.Pop(&s.arrivals).(Fragment))
			continue
		}
		if nextArr >= 0 && nextArr < s.inflight.done && nextArr <= end {
			f := heap.Pop(&s.arrivals).(Fragment)
			if f.IdealTick > s.now {
				s.now = f.IdealTick
			}
			s.queue = append(s.queue, f)
			continue
		}
		if s.inflight.done <= end {
			s.now = s.inflight.done
			s.out = append(s.out, Record{Fragment: s.inflight.frag, Depart: s.inflight.depart, Done: s.inflight.done})
			s.inflight = nil
			continue
		}
		s.now = end
		return
	}
}

// Finish drains all pending arrivals and the queue.
func (s *Sim) Finish() { s.RunTo(math.MaxInt64 / 4) }
