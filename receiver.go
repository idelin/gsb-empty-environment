package seqwin

import (
	"errors"
	"fmt"
	"sort"
)

// ErrTooOld reports a packet behind the cumulative acknowledgment point.
var ErrTooOld = errors.New("seqwin: packet is older than the receive window")

// ErrTooFarAhead reports a packet beyond the receive window.
var ErrTooFarAhead = errors.New("seqwin: packet is beyond the receive window")

// Range is an inclusive sequence-number interval [Start, End].
// End may be numerically smaller than Start when the interval wraps
// around the sequence space (e.g. {Start: 254, End: 1} in 8-bit).
type Range struct {
	Start, End uint32
}

// Receiver tracks received sequence numbers inside a sliding window and
// produces compact acknowledgment ranges. The window size must not exceed
// half the sequence space, otherwise old/new would be undecidable.
type Receiver struct {
	space  Space
	window uint32
	start  uint32 // first expected sequence number (anchor of the cumulative range)
	next   uint32 // next expected sequence number (cumulative ack point)
	ahead  map[uint32]struct{}
	count  int // distinct accepted packets
}

// NewReceiver creates a Receiver expecting `start` next, with the given
// window size (1..half the sequence space).
func NewReceiver(space Space, start, window uint32) (*Receiver, error) {
	if window == 0 || window > space.half {
		return nil, fmt.Errorf("seqwin: window must be in [1, %d]", space.half)
	}
	return &Receiver{
		space:  space,
		window: window,
		start:  start,
		next:   start,
		ahead:  make(map[uint32]struct{}),
	}, nil
}

// Receive ingests one packet. accepted is true only for packets that add
// new information; duplicates return (false, nil). Packets behind the
// window are rejected with ErrTooOld, packets past the window with
// ErrTooFarAhead, and a distance of exactly half the space with
// ErrAmbiguous.
func (r *Receiver) Receive(seq uint32) (accepted bool, err error) {
	d, ok := r.space.Diff(seq, r.next)
	if !ok {
		return false, ErrAmbiguous
	}
	switch {
	case d < 0:
		return false, ErrTooOld
	case uint32(d) >= r.window:
		return false, ErrTooFarAhead
	}
	if d == 0 {
		r.count++
		r.advance()
		return true, nil
	}
	if _, dup := r.ahead[seq]; dup {
		return false, nil
	}
	r.ahead[seq] = struct{}{}
	r.count++
	return true, nil
}

// advance moves the cumulative ack point over contiguously received packets.
func (r *Receiver) advance() {
	for {
		r.next = r.space.Add(r.next, 1)
		if _, ok := r.ahead[r.next]; !ok {
			return
		}
		delete(r.ahead, r.next)
	}
}

// Next returns the next expected sequence number (cumulative ack point).
func (r *Receiver) Next() uint32 { return r.next }

// Count returns the number of distinct accepted packets. Duplicates and
// rejected packets are not counted.
func (r *Receiver) Count() int { return r.count }

// AckRanges returns compact inclusive ranges covering everything received
// so far. The first range (when present) is the cumulative range ending at
// Next()-1; later ranges describe out-of-order blocks beyond gaps.
func (r *Receiver) AckRanges() []Range {
	var ranges []Range
	if r.next != r.start {
		ranges = append(ranges, Range{Start: r.start, End: r.space.Add(r.next, r.space.mod-1)})
	}
	if len(r.ahead) == 0 {
		return ranges
	}
	seqs := make([]uint32, 0, len(r.ahead))
	for s := range r.ahead {
		seqs = append(seqs, s)
	}
	// Order by distance ahead of the cumulative ack point.
	sort.Slice(seqs, func(i, j int) bool {
		di, _ := r.space.Diff(seqs[i], r.next)
		dj, _ := r.space.Diff(seqs[j], r.next)
		return di < dj
	})
	lo, prev := seqs[0], seqs[0]
	for _, s := range seqs[1:] {
		if s == r.space.Add(prev, 1) {
			prev = s
			continue
		}
		ranges = append(ranges, Range{Start: lo, End: prev})
		lo, prev = s, s
	}
	return append(ranges, Range{Start: lo, End: prev})
}
