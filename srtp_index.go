package netsec

import "errors"

// MinReplayWindow is the smallest replay window a caller may request.
const MinReplayWindow = 64

// ErrWindowTooSmall is returned when the requested replay window is
// narrower than MinReplayWindow.
var ErrWindowTooSmall = errors.New("netsec: replay window must be at least 64 packets")

// Tracker estimates SRTP packet indices per RFC 3711 Appendix A and
// maintains the rollover counter (ROC), the highest authenticated
// sequence number s_l, and a replay window.
//
// The intended receive-path flow is:
//
//	roc := tr.EstimateROC(seq)          // read-only guess
//	idx := tr.EstimateIndex(seq)        // 48-bit packet index for AEAD
//	if !authTagVerifies(pkt, idx) {     // forgery: state untouched
//	    drop(pkt)
//	} else if !tr.Accept(seq, roc) {    // replay or too old
//	    drop(pkt)
//	}
//
// State (s_l, ROC, replay window) is mutated only by Accept, so forged
// packets with wildly jumping sequence numbers cannot advance the ROC.
// Tracker is not safe for concurrent use; serialize per SSRC.
type Tracker struct {
	sl      uint16   // s_l: seq of highest authenticated packet
	roc     uint32   // ROC of highest authenticated packet
	highest uint64   // highest authenticated 48-bit index
	width   uint64   // replay window width in packets
	window  []uint64 // ring bitset, slot = index % width
	init    bool     // whether any packet has been accepted
}

// NewTracker returns a Tracker with a replay window of width packets.
// Widths below MinReplayWindow are rejected.
func NewTracker(width uint32) (*Tracker, error) {
	if width < MinReplayWindow {
		return nil, ErrWindowTooSmall
	}
	return &Tracker{
		width:  uint64(width),
		window: make([]uint64, (uint64(width)+63)/64),
	}, nil
}

// EstimateROC guesses the ROC for a packet with the given sequence
// number using the algorithm of RFC 3711 Appendix A. It is read-only:
// unauthenticated packets must never be passed to Accept, so guessing
// can never corrupt the tracker.
func (t *Tracker) EstimateROC(seq uint16) uint32 {
	if !t.init {
		return 0
	}
	seq32, sl32 := int32(seq), int32(t.sl)
	if t.sl < 32768 {
		if seq32-sl32 > 32768 {
			return t.roc - 1 // mod 2^32
		}
		return t.roc
	}
	if sl32-32768 > seq32 {
		return t.roc + 1 // mod 2^32
	}
	return t.roc
}

// EstimateIndex returns the 48-bit packet index ROC<<16 | seq for the
// estimated ROC. Use it as the AEAD/integrity input before Accept.
func (t *Tracker) EstimateIndex(seq uint16) uint64 {
	return uint64(t.EstimateROC(seq))<<16 | uint64(seq)
}

// Accept commits a packet whose authentication tag has already been
// verified against the index built from EstimateROC. seq and roc must
// be the values used for that verification, with no intervening Accept
// calls. It reports whether the packet is new; replayed packets and
// packets older than the replay window are rejected and leave state
// unchanged. On sequence-number wraparound the stored ROC advances by
// exactly one.
func (t *Tracker) Accept(seq uint16, roc uint32) bool {
	idx := uint64(roc)<<16 | uint64(seq)
	if !t.init {
		t.init = true
		t.highest = idx
		t.sl = seq
		t.roc = roc
		t.mark(idx)
		return true
	}
	if idx <= t.highest {
		if t.highest-idx >= t.width {
			return false // too old: outside the replay window
		}
		if t.seen(idx) {
			return false // replay inside the window
		}
		t.mark(idx)
		return true
	}
	// New highest packet: advance s_l and ROC to this packet's values.
	// When seq wrapped past 0 the estimate is roc+1, so the stored ROC
	// increases by exactly one.
	t.highest = idx
	t.sl = seq
	t.roc = roc
	t.mark(idx)
	return true
}

// ROC returns the ROC of the highest authenticated packet.
func (t *Tracker) ROC() uint32 { return t.roc }

// SeqL returns s_l, the sequence number of the highest authenticated
// packet.
func (t *Tracker) SeqL() uint16 { return t.sl }

// Highest returns the highest authenticated 48-bit packet index.
func (t *Tracker) Highest() uint64 { return t.highest }

func (t *Tracker) seen(idx uint64) bool {
	slot := idx % t.width
	return t.window[slot/64]&(1<<(slot%64)) != 0
}

func (t *Tracker) mark(idx uint64) {
	slot := idx % t.width
	t.window[slot/64] |= 1 << (slot % 64)
}
