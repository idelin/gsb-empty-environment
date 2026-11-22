package cid

// ReplayWindow is a 64-bit DTLS replay window. The zero value is ready for
// use. It is not safe for concurrent use; Router serializes access to its
// windows.
type ReplayWindow struct {
	max     uint64
	bitmap  uint64
	started bool
}

// Accept checks a sequence number against the window and records it when the
// sequence number is new. It returns false for a duplicate sequence number and
// for sequence numbers older than the window.
func (w *ReplayWindow) Accept(sequence uint64) bool {
	if !w.started {
		w.started = true
		w.max = sequence
		w.bitmap = 1
		return true
	}

	if sequence > w.max {
		shift := sequence - w.max
		if shift >= ReplayWindowSize {
			w.bitmap = 1
		} else {
			w.bitmap = (w.bitmap << shift) | 1
		}
		w.max = sequence
		return true
	}

	diff := w.max - sequence
	if diff >= ReplayWindowSize {
		return false
	}
	mask := uint64(1) << diff
	if w.bitmap&mask != 0 {
		return false
	}
	w.bitmap |= mask
	return true
}

// CheckAndMark is an explicit alias for Accept.
func (w *ReplayWindow) CheckAndMark(sequence uint64) bool {
	return w.Accept(sequence)
}

// Seen reports whether a sequence number is currently marked in the window.
func (w *ReplayWindow) Seen(sequence uint64) bool {
	if !w.started || sequence > w.max {
		return false
	}
	diff := w.max - sequence
	if diff >= ReplayWindowSize {
		return false
	}
	return w.bitmap&(uint64(1)<<diff) != 0
}
