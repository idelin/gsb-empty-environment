package digestauth

// replayWindow is a fixed-size sliding bitmap over nonce count (nc) values,
// kept per (nonce, cnonce) pair. It accepts out-of-order delivery within the
// window, rejects duplicates and values older than the window, and reports the
// service-defined wrap-around limit.
//
// high is the largest accepted nc. Bit i of bits marks (high - i). Values
// strictly above high slide the window forward, dropping the oldest marks.
type replayWindow struct {
	cap  int
	high uint32
	bits map[uint32]struct{}
	used bool
}

func newReplayWindow(capacity int) *replayWindow {
	if capacity <= 0 {
		capacity = defaultWindow
	}
	return &replayWindow{
		cap:  capacity,
		bits: make(map[uint32]struct{}),
	}
}

// observe records nc. It returns nil on first acceptance, ErrReplay for a
// duplicate, ErrStaleNonce for values that aged out of the window and
// ErrCounterWrapped when nc exceeds the configured maximum.
func (w *replayWindow) observe(nc, maxCount uint32) error {
	if nc > maxCount {
		return ErrCounterWrapped
	}

	if !w.used {
		w.used = true
		w.high = nc
		w.bits[0] = struct{}{}
		return nil
	}

	switch {
	case nc > w.high:
		// Slide the window forward, dropping marks that aged out.
		diff := nc - w.high
		next := make(map[uint32]struct{}, len(w.bits)+1)
		for offset := range w.bits {
			newOffset := offset + diff
			if newOffset >= uint32(w.cap) {
				continue // aged out of the window
			}
			next[newOffset] = struct{}{}
		}
		next[0] = struct{}{}
		w.bits = next
		w.high = nc
		return nil

	case nc == w.high:
		return ErrReplay

	default:
		offset := w.high - nc
		if offset >= uint32(w.cap) {
			return ErrStaleNonce
		}
		if _, seen := w.bits[offset]; seen {
			return ErrReplay
		}
		w.bits[offset] = struct{}{}
		return nil
	}
}
