package esp

import "math"

const (
	window64   = 64
	window256  = 256
	window1024 = 1024
)

// ReplayWindow implements the RFC 4303 Appendix A.2 64-bit ESN receive
// window. It is deliberately a two-phase object: Check only computes a
// candidate and inspects the bitmap, while Commit advances the highest seen
// sequence number and bitmap. Commit must only be called after the packet has
// been cryptographically authenticated.
//
// A ReplayWindow is not internally synchronized. Session serializes access to
// its window. Direct users that share a ReplayWindow between goroutines must
// provide their own synchronization.
type ReplayWindow struct {
	size        int
	words       []uint64
	highest     uint64
	initialized bool
}

// NewReplayWindow creates a window with the requested number of bits. The
// supported sizes are 64, 256, and 1024.
func NewReplayWindow(size int) (*ReplayWindow, error) {
	switch size {
	case window64, window256, window1024:
	default:
		return nil, ErrInvalidWindowSize
	}

	return &ReplayWindow{
		size:  size,
		words: make([]uint64, size/64),
	}, nil
}

// Size returns the window size in bits.
func (w *ReplayWindow) Size() int {
	return w.size
}

// Initialized reports whether at least one sequence number has been seen.
func (w *ReplayWindow) Initialized() bool {
	return w.initialized
}

// Highest returns the highest 64-bit sequence number seen and whether the
// window has been initialized.
func (w *ReplayWindow) Highest() (uint64, bool) {
	return w.highest, w.initialized
}

// Initialize resets the window and marks highest as already seen. This is
// useful when the SA was established with a known starting ESN, or for tests
// that exercise a window spanning the 2^32 boundary. Sequence numbers zero and
// 2^64-1 are rejected.
func (w *ReplayWindow) Initialize(highest uint64) error {
	if err := validateSequence(highest); err != nil {
		return err
	}
	w.reset(highest)
	return nil
}

// Reset clears the window to the uninitialized state.
func (w *ReplayWindow) Reset() {
	for i := range w.words {
		w.words[i] = 0
	}
	w.highest = 0
	w.initialized = false
}

// Check reconstructs a candidate 64-bit sequence number from seqLow and the
// current highest value, then performs a read-only anti-replay check.
//
// It is safe to call Check before authenticating a packet. A successful Check
// does NOT reserve or advance the window. Commit must be called only after
// successful authentication and trailer validation.
func (w *ReplayWindow) Check(seqLow uint32) (uint64, error) {
	if !w.initialized {
		seq := uint64(seqLow)
		if err := validateSequence(seq); err != nil {
			return 0, err
		}
		return seq, nil
	}

	seq, err := ReconstructESN(w.highest, seqLow)
	if err != nil {
		return 0, err
	}

	if seq < w.leftEdge() {
		return 0, ErrReplay
	}
	if seq <= w.highest && w.has(seq) {
		return 0, ErrReplay
	}
	return seq, nil
}

// Commit records an authenticated sequence number. It revalidates the
// sequence against the current window so that a concurrent advance cannot
// resurrect an already discarded sequence. It must never be called before the
// packet's ICV or AEAD tag has been verified.
func (w *ReplayWindow) Commit(seq uint64) error {
	if err := validateSequence(seq); err != nil {
		return err
	}

	if !w.initialized {
		w.reset(seq)
		return nil
	}

	if seq < w.leftEdge() {
		return ErrReplay
	}

	if seq <= w.highest {
		if w.has(seq) {
			return ErrReplay
		}
		w.set(seq)
		return nil
	}

	advance := seq - w.highest
	if advance >= uint64(w.size) {
		for i := range w.words {
			w.words[i] = 0
		}
	} else {
		// The bit positions for the newly entered values are the same modulo
		// the window size as the bit positions for the values that just left.
		// Clear them before setting the new highest value.
		for i := uint64(1); i <= advance; i++ {
			w.clear(w.highest + i)
		}
	}

	w.highest = seq
	w.set(seq)
	return nil
}

// ReconstructESN reconstructs a received 64-bit ESN from its transmitted
// low-order 32 bits. The receiver keeps the full 64-bit highest seen value; the
// candidate high-order word is selected from the signed 32-bit distance to
// that value. This handles an ordinary reordered packet, a packet just to the
// left of the window, and a packet on either side of a 2^32 boundary.
func ReconstructESN(highest uint64, seqLow uint32) (uint64, error) {
	high := highest >> 32
	highestLow := uint32(highest)
	distance := int64(seqLow) - int64(highestLow)

	// A positive distance greater than 2^31 means seqLow is actually the
	// low-order word of a packet in the preceding 2^32 epoch. A negative
	// distance less than -2^31 means it belongs to the following epoch. At
	// exactly 2^31 the tie is resolved to the current epoch.
	if distance > 1<<31 {
		if high > 0 {
			high--
		}
	} else if distance < -(1 << 31) {
		if high == math.MaxUint32 {
			return 0, ErrRekeyRequired
		}
		high++
	}

	seq := high<<32 | uint64(seqLow)
	if err := validateSequence(seq); err != nil {
		return 0, err
	}
	return seq, nil
}

func (w *ReplayWindow) reset(highest uint64) {
	for i := range w.words {
		w.words[i] = 0
	}
	w.highest = highest
	w.initialized = true
	w.set(highest)
}

func (w *ReplayWindow) leftEdge() uint64 {
	if w.highest < uint64(w.size-1) {
		return 0
	}
	return w.highest - uint64(w.size-1)
}

func (w *ReplayWindow) has(seq uint64) bool {
	bit := int(seq % uint64(w.size))
	return w.words[bit/64]&(uint64(1)<<uint(bit%64)) != 0
}

func (w *ReplayWindow) set(seq uint64) {
	bit := int(seq % uint64(w.size))
	w.words[bit/64] |= uint64(1) << uint(bit%64)
}

func (w *ReplayWindow) clear(seq uint64) {
	bit := int(seq % uint64(w.size))
	w.words[bit/64] &^= uint64(1) << uint(bit%64)
}

func validateSequence(seq uint64) error {
	switch seq {
	case 0:
		return ErrSequenceZero
	case math.MaxUint64:
		return ErrRekeyRequired
	default:
		return nil
	}
}
