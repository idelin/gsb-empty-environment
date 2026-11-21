// Package antireplay implements a replay window for a transport that puts only
// the low 32 bits of a 64-bit sequence number on the wire.
//
// The receiver has to recover the high bits of every sequence number. It does
// so by picking the 64-bit number whose low 32 bits match the received value
// and that is closest to Highest()+1. Ties pick the smaller number. This is
// well defined only when the window is narrower than half the truncated range,
// hence the restriction that the width is less than 2^31.
//
// Verification is deliberately separate from the window state. A caller can
// verify packets concurrently; only after the verifier reports success is the
// window updated, under a lock. If verification fails the window is untouched.
package antireplay

import (
	"errors"
	"fmt"
	"sync"
)

const (
	// MaxWindow is the largest permitted window width. A width of 2^31 or more
	// would make the truncated-sequence candidates ambiguous.
	MaxWindow = 1<<31 - 1

	productionFullBits  = 64
	productionTruncBits = 32
)

var (
	// ErrInvalidWindow is returned by a constructor when width is not in
	// [1, MaxWindow].
	ErrInvalidWindow = errors.New("antireplay: invalid window width")

	// ErrTooOld means the recovered sequence number is older than the left
	// edge of the replay window.
	ErrTooOld = errors.New("antireplay: sequence number is too old")

	// ErrReplay means this exact sequence number has already been accepted.
	ErrReplay = errors.New("antireplay: sequence number was already accepted")

	// ErrOverflow means the nearest 64-bit candidate would be above
	// math.MaxUint64, so there is no valid sequence number to accept.
	ErrOverflow = errors.New("antireplay: sequence number overflow")

	// ErrUnderflow means the nearest candidate would be below zero, so there
	// is no valid uint64 sequence number to accept.
	ErrUnderflow = errors.New("antireplay: sequence number underflow")

	// ErrVerification wraps an error returned by the caller's verifier.
	ErrVerification = errors.New("antireplay: verification failed")

	// ErrNilVerifier is returned when Accept is called without a verifier.
	ErrNilVerifier = errors.New("antireplay: nil verifier")
)

// VerifyFunc checks a packet using the recovered full 64-bit sequence number.
// A nil error means the packet is authentic. Any other error is returned to the
// caller wrapped with ErrVerification, and the replay window is not changed.
type VerifyFunc func(seq uint64) error

// Window is an anti-replay window. Its zero value is not usable; construct one
// with New or NewWithHighest.
//
// Window is safe for concurrent use by multiple goroutines.
type Window struct {
	mu sync.Mutex

	fullBits  uint
	truncBits uint
	maxFull   uint64
	mask      uint64
	modulus   uint64

	width   uint
	highest uint64
	seen    []uint64 // ring bitmap indexed by seq % width
}

// New creates an empty replay window of width entries. The initial highest
// sequence number is zero, but zero is not marked as seen, so a first packet
// with truncated sequence zero is accepted. Use NewWithHighest when resuming a
// session that has already authenticated packets.
func New(width int) (*Window, error) {
	return newWindow(productionFullBits, productionTruncBits, width, 0, false)
}

// NewWithHighest creates a replay window for a session whose highest already
// authenticated sequence number is highest. That sequence is marked as seen,
// so replaying it is rejected immediately. The next expected sequence is
// highest+1 (unless highest is math.MaxUint64).
func NewWithHighest(width int, highest uint64) (*Window, error) {
	return newWindow(productionFullBits, productionTruncBits, width, highest, true)
}

// newWindow constructs a window for arbitrary bit widths. Production callers
// use 64 full bits and 32 truncated bits; the smaller widths are useful for
// exhaustive tests of the same arithmetic.
func newWindow(fullBits, truncBits uint, width int, highest uint64, markHighest bool) (*Window, error) {
	if width <= 0 || uint64(width) > MaxWindow {
		return nil, fmt.Errorf("%w: %d (must be in [1, %d])", ErrInvalidWindow, width, MaxWindow)
	}
	if fullBits == 0 || fullBits > 64 || truncBits == 0 || truncBits > 32 || truncBits > fullBits {
		return nil, fmt.Errorf("%w: full=%d truncated=%d", ErrInvalidWindow, fullBits, truncBits)
	}

	maxFull := ^uint64(0)
	if fullBits < 64 {
		maxFull = (uint64(1) << fullBits) - 1
	}
	if highest > maxFull {
		return nil, fmt.Errorf("%w: highest %d exceeds %d-bit range", ErrInvalidWindow, highest, fullBits)
	}

	w := &Window{
		fullBits:  fullBits,
		truncBits: truncBits,
		maxFull:   maxFull,
		mask:      (uint64(1) << truncBits) - 1,
		modulus:   uint64(1) << truncBits,
		width:     uint(width),
		highest:   highest,
		seen:      make([]uint64, (uint(width)+63)/64),
	}
	if markHighest {
		w.setBitLocked(highest)
	}
	return w, nil
}

// Width returns the configured window width.
func (w *Window) Width() int {
	if w == nil {
		return 0
	}
	return int(w.width)
}

// Highest returns the highest authenticated sequence number. If no packet has
// been accepted it is the constructor's initial value (zero for New).
func (w *Window) Highest() uint64 {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.highest
}

// Accept recovers the full sequence number for a received low-32-bit value,
// runs verify, and only then commits the sequence number to the replay window.
//
// The sequence number is recovered from a snapshot of the window. Verification
// runs without holding the window lock, so different packets can be verified
// in parallel. The commit is then serialized: if another goroutine committed
// the same sequence number while verify was running, Accept returns ErrReplay.
// Thus a given sequence number is delivered successfully at most once.
//
// If the returned seq is non-zero and err is nil, the caller may deliver the
// packet. On any error seq is zero.
func (w *Window) Accept(low uint32, verify VerifyFunc) (uint64, error) {
	if w == nil || w.width == 0 || len(w.seen) == 0 {
		return 0, ErrInvalidWindow
	}
	if verify == nil {
		return 0, ErrNilVerifier
	}
	return w.accept(uint64(low), verify)
}

func (w *Window) accept(trunc uint64, verify VerifyFunc) (uint64, error) {
	// Snapshot the decision point under the lock, then release it before doing
	// potentially expensive signature/authentication work.
	w.mu.Lock()
	seq, err := w.reconstructLocked(trunc)
	if err == nil {
		err = w.statusLocked(seq)
	}
	w.mu.Unlock()
	if err != nil {
		return 0, err
	}

	if err := verify(seq); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrVerification, err)
	}

	// Verification succeeded. Re-check and commit atomically. In particular,
	// a concurrent packet may have moved the window or marked this sequence
	// while verification was in flight.
	w.mu.Lock()
	err = w.commitLocked(seq)
	w.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// reconstructLocked maps the truncated sequence number to its full-width
// candidate. The caller must hold w.mu.
//
// Let target = highest+1 (which may be 2^fullBits when highest is all ones),
// M = 2^truncBits and L be the received low bits. Every candidate differs from
// target by delta+k*M, where delta = L-(target mod M). The nearest candidate is
// selected; at an exact tie the smaller candidate is selected. If that
// candidate is outside [0, maxFull] there is no valid sequence number.
func (w *Window) reconstructLocked(trunc uint64) (uint64, error) {
	trunc &= w.mask
	targetLow := (w.highest + 1) & w.mask

	delta := int64(trunc) - int64(targetLow)
	half := int64(w.modulus >> 1)
	offset := delta
	if delta >= half {
		// Positive half-ties select the smaller (negative) candidate.
		offset -= int64(w.modulus)
	} else if delta < -half {
		offset += int64(w.modulus)
	}

	// target = highest+1 is the next sequence after the highest authenticated
	// one. When highest==maxFull, target is 2^fullBits and is not representable
	// as a uint64; handle it separately.
	if w.highest == w.maxFull {
		if offset >= 0 {
			return 0, ErrOverflow
		}
		neg := uint64(-offset)
		if w.fullBits < 64 && neg > (uint64(1)<<w.fullBits) {
			return 0, ErrUnderflow
		}
		// 2^fullBits - neg, written without adding one to maxFull.
		return w.maxFull - (neg - 1), nil
	}

	target := w.highest + 1
	if offset >= 0 {
		add := uint64(offset)
		if add > w.maxFull-target {
			return 0, ErrOverflow
		}
		return target + add, nil
	}

	sub := uint64(-offset)
	if sub > target {
		return 0, ErrUnderflow
	}
	return target - sub, nil
}

// statusLocked reports whether seq can currently be accepted. It does not
// change the window. The caller must hold w.mu.
func (w *Window) statusLocked(seq uint64) error {
	if seq > w.highest {
		return nil
	}
	if w.highest-seq >= uint64(w.width) {
		return ErrTooOld
	}
	if w.bitLocked(seq) {
		return ErrReplay
	}
	return nil
}

// commitLocked atomically advances the window and marks seq as seen. The
// caller must hold w.mu.
func (w *Window) commitLocked(seq uint64) error {
	if seq > w.highest {
		w.advanceLocked(seq)
		w.setBitLocked(seq)
		return nil
	}
	if w.highest-seq >= uint64(w.width) {
		return ErrTooOld
	}
	if w.bitLocked(seq) {
		return ErrReplay
	}
	w.setBitLocked(seq)
	return nil
}

// advanceLocked moves highest to seq. Slots for sequence numbers that leave
// the window (and slots skipped by a large forward jump) are cleared, so stale
// bits can never make a sequence look accepted after the window wraps around
// the ring bitmap.
func (w *Window) advanceLocked(seq uint64) {
	delta := seq - w.highest
	if delta >= uint64(w.width) {
		for i := range w.seen {
			w.seen[i] = 0
		}
	} else {
		for s := w.highest + 1; ; s++ {
			w.clearBitLocked(s)
			if s == seq {
				break
			}
		}
	}
	w.highest = seq
}

func (w *Window) bitLocked(seq uint64) bool {
	slot := seq % uint64(w.width)
	return w.seen[slot/64]&(uint64(1)<<(slot%64)) != 0
}

func (w *Window) setBitLocked(seq uint64) {
	slot := seq % uint64(w.width)
	w.seen[slot/64] |= uint64(1) << (slot % 64)
}

func (w *Window) clearBitLocked(seq uint64) {
	slot := seq % uint64(w.width)
	w.seen[slot/64] &^= uint64(1) << (slot % 64)
}
