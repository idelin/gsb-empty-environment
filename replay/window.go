// Package replay implements the receive-side data plane of a rekeying
// tunnel.
//
// It combines two pieces:
//
//   - [Window], a fixed-width ring bitmap anti-replay window following the
//     RFC 6479 word-at-a-time clearing rules, one independent window per
//     session; and
//   - [KeyRing], which keeps the previous, current and not-yet-confirmed
//     next session slots alive and performs an atomic generation switch
//     only when the first fully authenticated, replay-free packet of the
//     next session arrives.
package replay

import "fmt"

// Window is a fixed-width anti-replay window backed by a ring bitmap of
// 64-bit words. It is not safe for concurrent use; callers (typically a
// [Session] owned by a [KeyRing]) must serialize access.
//
// The window covers the inclusive range [maxCounter-W+1, maxCounter],
// where W is the bitmap width in bits. Sequence numbers are treated as
// monotonically increasing 64-bit counters.
type Window struct {
	width       uint64
	words       []uint64
	maxCounter  uint64
	initialized bool
}

// NewWindow creates an anti-replay window of width bits. width must be a
// positive multiple of 64 (for example 2048).
func NewWindow(width uint64) (*Window, error) {
	if width == 0 || width%64 != 0 {
		return nil, fmt.Errorf("replay: window width %d must be a positive multiple of 64", width)
	}
	return &Window{
		width: width,
		words: make([]uint64, width/64),
	}, nil
}

// Width returns the window width in bits.
func (w *Window) Width() uint64 { return w.width }

// Max returns the highest accepted sequence number. The result is only
// meaningful after the first successful Check.
func (w *Window) Max() uint64 { return w.maxCounter }

// Initialized reports whether at least one sequence number has been
// accepted.
func (w *Window) Initialized() bool { return w.initialized }

// Words returns a copy of the underlying bitmap words, word zero holding
// ring bits 0..63, word one bits 64..127 and so on.
func (w *Window) Words() []uint64 {
	out := make([]uint64, len(w.words))
	copy(out, w.words)
	return out
}

// Seen reports whether seq currently occupies a set bit inside the
// window. Sequence numbers outside the window are reported as unseen.
func (w *Window) Seen(seq uint64) bool {
	if !w.initialized || seq > w.maxCounter || w.maxCounter-seq >= w.width {
		return false
	}
	word, bit := w.ringPosition(seq)
	return w.words[word]&(uint64(1)<<bit) != 0
}

// Check accepts seq when it is a fresh sequence number inside the window
// and records it. Duplicate bits and sequence numbers below the window
// floor (maxCounter-W+1) are rejected as replays.
func (w *Window) Check(seq uint64) bool {
	if !w.initialized {
		w.maxCounter = seq
		w.initialized = true
		w.setBit(seq)
		return true
	}

	if seq > w.maxCounter {
		w.advance(seq)
		w.setBit(seq)
		return true
	}

	// seq <= maxCounter: reject when it falls below the window floor.
	if w.maxCounter-seq >= w.width {
		return false
	}

	word, bit := w.ringPosition(seq)
	if w.words[word]&(uint64(1)<<bit) != 0 {
		return false
	}
	w.words[word] |= uint64(1) << bit
	return true
}

// advance moves the window so that newMax becomes the highest accepted
// sequence number, precisely clearing every bitmap word (or word part)
// crossed by the jump.
func (w *Window) advance(newMax uint64) {
	diff := newMax - w.maxCounter

	// A jump of W or more leaves every previously recorded bit outside
	// the new window: clear the whole table; only the new bit is set by
	// the caller afterwards.
	if diff >= w.width {
		for i := range w.words {
			w.words[i] = 0
		}
		w.maxCounter = newMax
		return
	}

	// Following RFC 6479, the ring positions of the counters that leave
	// the window are cleared word by word before the new maximum is
	// recorded. Those are the diff-1 counters oldMax+1 .. newMax-1; the
	// position of newMax itself is overwritten by setBit below, so it is
	// deliberately excluded (the RFC clears it only up to the bit before
	// the new highest counter). The range is contiguous on the ring and
	// wraps at most once because diff < width.
	start := (w.maxCounter + 1) % w.width
	span := diff - 1
	w.clearRingRange(start, span)
	w.maxCounter = newMax
}

// clearRingRange clears span ring bit positions beginning at start,
// wrapping at the ring end. start < width and span < width. It performs
// the RFC 6479 precise word clearing: the word containing start and the
// word containing the final position are masked partially, while every
// fully spanned word between them is zeroed outright.
func (w *Window) clearRingRange(start, span uint64) {
	if span == 0 {
		return
	}

	// Walk the range in ring bit positions one word at a time. A chunk
	// reaches either the end of the current word or the end of the range,
	// so wrap-around inside a single word is handled by the mask alone.
	for remaining, pos := span, start; remaining > 0; {
		word, bit := pos/64, pos%64
		take := remaining
		if avail := 64 - bit; take > avail {
			take = avail
		}

		if take == 64 {
			w.words[word] = 0
		} else {
			w.words[word] &^= lowMask(take) << bit
		}

		remaining -= take
		pos += take
		if pos >= w.width {
			pos -= w.width
		}
	}
}

func (w *Window) ringPosition(seq uint64) (word, bit uint64) {
	idx := seq % w.width
	return idx / 64, idx % 64
}

func (w *Window) setBit(seq uint64) {
	word, bit := w.ringPosition(seq)
	w.words[word] |= uint64(1) << bit
}

// lowMask returns a uint64 with its lowest bits bits set. bits is
// assumed to be in [1, 64].
func lowMask(bits uint64) uint64 {
	if bits >= 64 {
		return ^uint64(0)
	}
	return uint64(1)<<bits - 1
}
