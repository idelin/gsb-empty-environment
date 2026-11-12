package netsec

import (
	"sync"
)

// DefaultReplayWindowSize is the default number of record sequence numbers
// tracked by a DTLS replay window.
const DefaultReplayWindowSize = 64

// ReplayWindow implements the sliding anti-replay bitmap used for DTLS
// records. It accepts a sequence number when it is newer than the window or
// when it is an older, previously unseen number that is still inside the
// window.
type ReplayWindow struct {
	mu          sync.Mutex
	size        uint
	max         uint64
	words       []uint64
	initialized bool
}

// NewReplayWindow creates a replay window of size bits. A size of zero selects
// DefaultReplayWindowSize. Sizes larger than 1<<20 are rejected by falling
// back to the default to avoid an accidental huge allocation.
func NewReplayWindow(size uint) *ReplayWindow {
	if size == 0 || size > 1<<20 {
		size = DefaultReplayWindowSize
	}
	return &ReplayWindow{
		size:  size,
		words: make([]uint64, (size+63)/64),
	}
}

// Size returns the configured window size in bits.
func (w *ReplayWindow) Size() uint {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

// Highest returns the highest accepted sequence number and whether any
// sequence number has been accepted.
func (w *ReplayWindow) Highest() (uint64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.max, w.initialized
}

// Check reports whether seq would be accepted without changing the window.
func (w *ReplayWindow) Check(seq uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.accept(seq, false)
}

// Accept accepts seq when it is not a replay. It returns false for an
// already-seen or too-old sequence number and leaves the window unchanged.
func (w *ReplayWindow) Accept(seq uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.accept(seq, true)
}

// Reset makes the window empty. The next accepted sequence number becomes the
// new highest.
func (w *ReplayWindow) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.words {
		w.words[i] = 0
	}
	w.max = 0
	w.initialized = false
}

func (w *ReplayWindow) accept(seq uint64, commit bool) bool {
	if !w.initialized {
		if commit {
			w.initialized = true
			w.max = seq
			w.setBit(0)
		}
		return true
	}

	if seq > w.max {
		if commit {
			w.advance(seq - w.max)
			w.max = seq
			w.setBit(0)
		}
		return true
	}

	index := w.max - seq
	if index >= uint64(w.size) {
		return false
	}
	if w.bit(index) {
		return false
	}
	if commit {
		w.setBit(index)
	}
	return true
}

func (w *ReplayWindow) advance(delta uint64) {
	if delta >= uint64(w.size) {
		for i := range w.words {
			w.words[i] = 0
		}
		return
	}
	wordShift := int(delta / 64)
	bitShift := uint(delta % 64)
	for i := len(w.words) - 1; i >= 0; i-- {
		src := i - wordShift
		var value uint64
		if src >= 0 {
			value = w.words[src] << bitShift
			if bitShift != 0 && src-1 >= 0 {
				value |= w.words[src-1] >> (64 - bitShift)
			}
		}
		w.words[i] = value
	}
}

func (w *ReplayWindow) bit(index uint64) bool {
	return w.words[index/64]&(uint64(1)<<(index%64)) != 0
}

func (w *ReplayWindow) setBit(index uint64) {
	w.words[index/64] |= uint64(1) << (index % 64)
}
