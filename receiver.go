package seqwin

import "fmt"

// Verdict is the outcome of delivering one packet to a Receiver.
type Verdict int

const (
	// AcceptedNew: first delivery of this sequence number.
	AcceptedNew Verdict = iota
	// Duplicate: already seen; does not grow the ack count.
	Duplicate
	// RejectedOld: behind the window; too old to track.
	RejectedOld
	// RejectedFuture: ahead of the window; cannot buffer.
	RejectedFuture
	// RejectedAmbiguous: modular distance is exactly half the space.
	RejectedAmbiguous
	// RejectedInvalid: not a member of the sequence space.
	RejectedInvalid
)

func (v Verdict) String() string {
	switch v {
	case AcceptedNew:
		return "accepted-new"
	case Duplicate:
		return "duplicate"
	case RejectedOld:
		return "rejected-old"
	case RejectedFuture:
		return "rejected-future"
	case RejectedAmbiguous:
		return "rejected-ambiguous"
	default:
		return "rejected-invalid"
	}
}

// Range is an inclusive run of received sequence numbers [Start, End].
type Range struct {
	Start, End uint64
}

func (r Range) String() string {
	if r.Start == r.End {
		return fmt.Sprintf("%d", r.Start)
	}
	return fmt.Sprintf("%d-%d", r.Start, r.End)
}

// Receiver tracks a sliding receive window over a Space. Sequence numbers
// wrap around; packets older than the window are rejected, and a distance
// of exactly half the space is rejected as ambiguous rather than guessed.
type Receiver struct {
	space  *Space
	window uint64
	next   uint64 // next expected (cumulative ack frontier)
	slots  []bool // ring buffer of size window, slot i covers next+i
	off    uint64 // ring offset of next
	used   uint64 // number of valid slots (== window always; kept for clarity)
	acked  uint64 // distinct packets accepted (duplicates excluded)
	seen   bool   // any packet ever accepted
}

// NewReceiver builds a window receiver expecting sequence number 0 next.
// window must be in [1, Half] so every in-window distance stays below the
// ambiguous half-space boundary.
func NewReceiver(space *Space, window uint64) (*Receiver, error) {
	return NewReceiverAt(space, window, 0)
}

// NewReceiverAt is NewReceiver with an explicit starting frontier, which
// allows a window that straddles the wrap point of the space.
func NewReceiverAt(space *Space, window, start uint64) (*Receiver, error) {
	if window == 0 || window > space.Half {
		return nil, fmt.Errorf("seqwin: window %d out of range [1,%d]", window, space.Half)
	}
	if !space.Valid(start) {
		return nil, fmt.Errorf("seqwin: start %d outside %d-bit space", start, space.Bits)
	}
	return &Receiver{space: space, window: window, next: start, slots: make([]bool, window)}, nil
}

// Next returns the next expected sequence number (cumulative ack frontier).
func (r *Receiver) Next() uint64 { return r.next }

// Acked returns the count of distinct accepted packets.
func (r *Receiver) Acked() uint64 { return r.acked }

// Deliver classifies one incoming sequence number and updates the window.
func (r *Receiver) Deliver(s uint64) Verdict {
	if !r.space.Valid(s) {
		return RejectedInvalid
	}
	rel := r.space.Dist(r.next, s)
	switch {
	case rel == r.space.Half:
		// Exactly half the space away: cannot tell old from new.
		return RejectedAmbiguous
	case rel < r.window:
		idx := (r.off + rel) % r.window
		if r.slots[idx] {
			return Duplicate
		}
		r.slots[idx] = true
		r.acked++
		r.seen = true
		if rel == 0 {
			r.advance()
		}
		return AcceptedNew
	case rel > r.space.Half:
		return RejectedOld
	default:
		return RejectedFuture
	}
}

// advance slides the window over contiguously received slots.
func (r *Receiver) advance() {
	for r.slots[r.off] {
		r.slots[r.off] = false
		r.off = (r.off + 1) % r.window
		r.next = r.space.Add(r.next, 1)
	}
}

// Cumulative returns the highest contiguously received sequence number
// (Next-1, wrap-aware) and whether anything has been received yet.
func (r *Receiver) Cumulative() (uint64, bool) {
	if !r.seen {
		return 0, false
	}
	return r.space.Add(r.next, r.space.Mask), true // next-1 mod 2^Bits
}

// AckRanges compacts the out-of-order received slots into inclusive
// ranges, ordered by distance from Next. Wraps across zero are kept as
// absolute sequence numbers, so a range may cross 2^Bits-1 -> 0.
func (r *Receiver) AckRanges() []Range {
	var ranges []Range
	var open bool
	for i := uint64(0); i < r.window; i++ {
		if r.slots[(r.off+i)%r.window] {
			if !open {
				ranges = append(ranges, Range{Start: r.space.Add(r.next, i)})
				open = true
			}
		} else if open {
			ranges[len(ranges)-1].End = r.space.Add(r.next, i-1)
			open = false
		}
	}
	if open {
		ranges[len(ranges)-1].End = r.space.Add(r.next, r.window-1)
	}
	return ranges
}
