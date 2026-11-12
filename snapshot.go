package netsec

// Snapshot is an explicit, immutable value-copy of the identity state visible
// on a thread at a point in time. It is the only object allowed to carry
// identity into an asynchronously spawned thread.
//
// A snapshot deliberately cannot be constructed by hand: callers must obtain
// one through Monitor.Snapshot so the provenance chain is recorded.
type Snapshot struct {
	seq       uint64
	threadID  string
	requestID string
	frames    []frame
}

// RequestID returns the request whose identity the snapshot captured.
func (s *Snapshot) RequestID() string {
	return s.requestID
}

// Depth returns the number of identity frames captured by the snapshot.
func (s *Snapshot) Depth() int {
	return len(s.frames)
}

// Provenance is the snapshot hop appended ahead of every inherited frame's
// chain when the snapshot is applied to a child thread.
func (s *Snapshot) provenance() Provenance {
	return Provenance{
		Seq:       s.seq,
		Kind:      eventSnapshot,
		ThreadID:  s.threadID,
		RequestID: s.requestID,
		Detail:    "explicit snapshot",
	}
}
