package sctpauth

// DefaultReceiveWindow is the default number of in-flight DATA chunks the
// receiver will accept ahead of the next cumulative TSN.
const DefaultReceiveWindow = 64

// maxReceiveWindow caps the window so a crafted value cannot force unbounded
// bookkeeping.
const maxReceiveWindow = 1 << 20

// ReceiveWindow is a strict sliding receive window over the 32-bit modular TSN
// space. A DATA chunk is accepted only when its TSN is the next expected TSN or
// lies strictly within the advertised window, and has not been seen before.
// Everything else (old TSNs, far-future TSNs) is rejected, blocking both
// off-path blind injection and replay.
type ReceiveWindow struct {
	cumulativeTSN uint32
	windowSize    uint32
	seen          map[uint32]struct{}
}

// NewReceiveWindow seeds the window with the peer's initial TSN. The first
// accepted DATA chunk must carry exactly initialTSN.
func NewReceiveWindow(initialTSN uint32, windowSize uint32) *ReceiveWindow {
	if windowSize == 0 {
		windowSize = DefaultReceiveWindow
	}
	if windowSize > maxReceiveWindow {
		windowSize = maxReceiveWindow
	}
	return &ReceiveWindow{
		cumulativeTSN: initialTSN - 1,
		windowSize:    windowSize,
		seen:          make(map[uint32]struct{}),
	}
}

// Check reports whether tsn may be accepted, returning ErrTSNOutsideWindow for
// chunks that lie more than WindowSize ahead of the cumulative TSN (off-path
// blind injection), and ErrDuplicateTSN for any TSN at or behind the
// cumulative TSN or already buffered (replay). Serial-number arithmetic over
// the 32-bit modular space makes the comparison wrap safe. It never mutates
// state.
func (w *ReceiveWindow) Check(tsn uint32) error {
	delta := int32(tsn - w.cumulativeTSN)
	if delta <= 0 {
		return ErrDuplicateTSN
	}
	if uint32(delta) > w.windowSize {
		return ErrTSNOutsideWindow
	}
	if _, ok := w.seen[tsn]; ok {
		return ErrDuplicateTSN
	}
	return nil
}

// Accept validates and records a TSN, then advances the cumulative TSN over
// any contiguous run of received chunks.
func (w *ReceiveWindow) Accept(tsn uint32) error {
	if err := w.Check(tsn); err != nil {
		return err
	}
	w.seen[tsn] = struct{}{}

	next := w.cumulativeTSN + 1
	for {
		if _, ok := w.seen[next]; !ok {
			break
		}
		delete(w.seen, next)
		w.cumulativeTSN = next
		next++
	}
	return nil
}

// CumulativeTSN returns the last contiguous TSN delivered.
func (w *ReceiveWindow) CumulativeTSN() uint32 {
	return w.cumulativeTSN
}

// WindowSize returns the configured window width.
func (w *ReceiveWindow) WindowSize() uint32 {
	return w.windowSize
}

// PendingCount returns how many accepted-but-not-contiguous TSNs are buffered.
func (w *ReceiveWindow) PendingCount() int {
	return len(w.seen)
}
