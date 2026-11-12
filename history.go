package taskpkg

import "sync"

// HistoryBuffer owns the last completed frame and publishes new frames
// atomically.
//
// Resolve takes a deep snapshot of the current history before doing any work.
// The snapshot remains immutable for the whole reprojection, so a concurrent
// or later Commit can never overwrite pixels while the new frame is being
// resolved. Commit succeeds only for the version that Resolve observed; a
// result computed from an older history returns ErrStaleHistory.
type HistoryBuffer struct {
	mu      sync.Mutex
	frame   Frame
	version uint64
}

// NewHistoryBuffer validates and stores the initial history frame.
func NewHistoryBuffer(initial Frame) (*HistoryBuffer, error) {
	if err := initial.Validate(); err != nil {
		return nil, err
	}
	return &HistoryBuffer{frame: initial.Clone()}, nil
}

// Snapshot returns a private copy of the current history and its version.
func (h *HistoryBuffer) Snapshot() (Frame, uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.frame.Clone(), h.version
}

// Version returns the number of committed frames.
func (h *HistoryBuffer) Version() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.version
}

// Resolve reprojects against a stable snapshot and returns a completed Result
// together with the history version on which it is based.
func (h *HistoryBuffer) Resolve(current Frame, motions map[ObjectID]Mat4, cfg Config) (Result, uint64, error) {
	previous, version := h.Snapshot()
	result, err := Reproject(previous, current, motions, cfg)
	if err != nil {
		return Result{}, version, err
	}
	return result, version, nil
}

// Commit atomically publishes a completed result. The result's frame geometry
// must still match the frame that Resolve used as the current frame.
func (h *HistoryBuffer) Commit(version uint64, result Result) error {
	if err := result.Frame.Validate(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.version != version {
		return ErrStaleHistory
	}
	h.frame = result.Frame.Clone()
	h.version++
	return nil
}
