package blockmatch

// rollingHash is an rsync-style weak rolling checksum over a sliding window.
// It is deliberately cheap to update byte-by-byte; collisions are expected
// and only ever produce candidates that must be verified by stronger means.
type rollingHash struct {
	a, b uint32
	n    uint32 // current window length
}

func (h *rollingHash) reset() { *h = rollingHash{} }

// append adds x as the new last byte of a growing window.
func (h *rollingHash) append(x byte) {
	h.a += uint32(x)
	h.b += h.a
	h.n++
}

// roll slides a full window by one byte: out leaves, in enters.
func (h *rollingHash) roll(out, in byte) {
	h.a += uint32(in) - uint32(out)
	h.b += h.a - h.n*uint32(out)
}

// dropFirst removes the oldest byte x from the window (window shrinks).
func (h *rollingHash) dropFirst(x byte) {
	h.a -= uint32(x)
	h.b -= h.n * uint32(x)
	h.n--
}

// value returns the 32-bit weak fingerprint of the current window.
func (h *rollingHash) value() uint32 { return h.b<<16 | h.a&0xffff }
