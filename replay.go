package netsec

// DefaultReplayWindowSize is the replay window width required by this package.
const DefaultReplayWindowSize uint64 = 64

// ReplayWindow is a sliding replay window. Bit zero corresponds to the highest
// record number observed so far.
type ReplayWindow struct {
	size    uint64
	highest uint64
	bitmap  uint64
	started bool
}

// NewReplayWindow creates a replay window. A zero or omitted size means 64.
func NewReplayWindow(size ...uint64) *ReplayWindow {
	n := DefaultReplayWindowSize
	if len(size) > 0 && size[0] > 0 {
		n = size[0]
	}
	if n > 64 {
		n = 64
	}
	return &ReplayWindow{size: n}
}

// Size returns the replay window width.
func (w *ReplayWindow) Size() uint64 {
	if w == nil || w.size == 0 {
		return DefaultReplayWindowSize
	}
	return w.size
}

// Seen reports whether recordNumber is inside the window and has already been
// observed. A record older than the window is also reported as seen.
func (w *ReplayWindow) Seen(recordNumber uint64) bool {
	if w == nil || !w.started {
		return false
	}
	if recordNumber > w.highest {
		return false
	}
	delta := w.highest - recordNumber
	if delta >= w.Size() {
		return true
	}
	return w.bitmap&(uint64(1)<<delta) != 0
}

// Mark records recordNumber as observed.
func (w *ReplayWindow) Mark(recordNumber uint64) {
	if w == nil {
		return
	}
	if w.size == 0 {
		w.size = DefaultReplayWindowSize
	}
	if uint64(w.size) > 64 {
		w.size = 64
	}
	if !w.started {
		w.started = true
		w.highest = recordNumber
		w.bitmap = 1
		return
	}

	if recordNumber > w.highest {
		delta := recordNumber - w.highest
		if delta >= w.Size() {
			w.bitmap = 1
		} else {
			w.bitmap = (w.bitmap << delta) | 1
		}
		w.highest = recordNumber
		return
	}

	delta := w.highest - recordNumber
	if delta < w.Size() {
		w.bitmap |= uint64(1) << delta
	}
}

// IsNew reports whether recordNumber is newer than every record observed so
// far. RFC 9146 requires this before a CID record may change the peer address.
func (w *ReplayWindow) IsNew(recordNumber uint64) bool {
	if w == nil || !w.started {
		return true
	}
	return recordNumber > w.highest
}

// Highest returns the highest record number marked in the window.
func (w *ReplayWindow) Highest() uint64 {
	if w == nil {
		return 0
	}
	return w.highest
}
