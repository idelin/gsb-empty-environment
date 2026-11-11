package conntrack

// All sequence arithmetic works on uint32 values and relies on natural
// 32-bit wrap-around, matching TCP's modular sequence space.

// seqLT reports whether a precedes b in TCP sequence space (RFC 1982 style
// comparison on a 32-bit serial number).
func seqLT(a, b uint32) bool { return int32(a-b) < 0 }

// seqLE reports whether a equals or precedes b.
func seqLE(a, b uint32) bool { return int32(a-b) <= 0 }

// seqGT reports whether a follows b.
func seqGT(a, b uint32) bool { return int32(a-b) > 0 }

// seqInClosedWindow reports whether seq lies in the closed range
// [lo, lo+size], including the right edge. It is used for zero-length
// segments (pure ACKs, window updates, RSTs and FIN retransmissions), which
// RFC 793 accepts at the window right edge even when the window is closed.
func seqInClosedWindow(seq, lo uint32, size uint32) bool {
	delta := seq - lo
	return delta <= size
}

// seqInRecentWindow reports whether seq falls in the receiver's full window
// spanning either side of rcvNxt: [rcvNxt-rcvWnd, rcvNxt+rcvWnd]. It is used
// for retransmitted control segments (e.g. FINs) whose sequence may now lie
// just behind rcvNxt but inside the still-open receive window.
func seqInRecentWindow(seq, rcvNxt, rcvWnd uint32) bool {
	behind := rcvNxt - seq
	ahead := seq - rcvNxt
	return behind <= rcvWnd || ahead <= rcvWnd
}

// segmentAcceptable implements the RFC 793 receive check for a data-bearing
// segment against the receiver's current window [rcvNxt, rcvNxt+rcvWnd).
// A segment is acceptable when any part of its sequence span overlaps the
// window; a zero-length segment is acceptable at either window edge.
func segmentAcceptable(seq, end, rcvNxt, rcvWnd uint32, length int) bool {
	right := rcvNxt + rcvWnd
	if length == 0 {
		// Zero-length segment: seq must be inside the closed window.
		return seqInClosedWindow(seq, rcvNxt, rcvWnd)
	}
	// Overlap test on the modular number line: seq < right and end > rcvNxt.
	return seqLT(seq, right) && seqGT(end, rcvNxt)
}
