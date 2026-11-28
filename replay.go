package wireguard

import "sync"

// ReplayWindowSize is the receive replay bitmap width in packets.
const ReplayWindowSize = 2048

const replayWords = ReplayWindowSize / 64

// ReplayWindow is a 2048-bit sliding receive counter window.
type ReplayWindow struct {
	mu     sync.Mutex
	max    uint64
	bitmap [replayWords]uint64
	init   bool
}

// Validate reports whether counter is fresh without changing the window.
func (w *ReplayWindow) Validate(counter uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.validate(counter)
}

func (w *ReplayWindow) validate(counter uint64) bool {
	if !w.init {
		return true
	}
	if counter > w.max {
		return true
	}
	diff := w.max - counter
	if diff >= ReplayWindowSize {
		return false
	}
	return w.bitmap[diff/64]&(uint64(1)<<(diff%64)) == 0
}

// Update marks counter as received. It returns false if the counter is a
// duplicate or is older than the window.
func (w *ReplayWindow) Update(counter uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.validate(counter) {
		return false
	}
	if !w.init {
		w.init = true
		w.max = counter
		w.bitmap[0] = 1
		return true
	}
	if counter > w.max {
		w.advance(counter - w.max)
		w.max = counter
		w.bitmap[0] |= 1
		return true
	}
	diff := w.max - counter
	w.bitmap[diff/64] |= uint64(1) << (diff % 64)
	return true
}

func (w *ReplayWindow) advance(shift uint64) {
	if shift >= ReplayWindowSize {
		w.bitmap = [replayWords]uint64{}
		return
	}
	wordShift := int(shift / 64)
	bitShift := uint(shift % 64)
	var next [replayWords]uint64
	for i := len(next) - 1; i >= 0; i-- {
		src := i - wordShift
		if src >= 0 {
			next[i] = w.bitmap[src] << bitShift
		}
		if bitShift != 0 && src-1 >= 0 {
			next[i] |= w.bitmap[src-1] >> (64 - bitShift)
		}
	}
	w.bitmap = next
}

// Max returns the highest accepted counter and whether any packet has been
// accepted.
func (w *ReplayWindow) Max() (uint64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.max, w.init
}

// Reset clears the window.
func (w *ReplayWindow) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.max = 0
	w.bitmap = [replayWords]uint64{}
	w.init = false
}
