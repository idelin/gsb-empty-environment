package lsadb

import "sync"

// AuthTracker records the last accepted cryptographic authentication sequence
// number (the 64-bit keyed-MD5/HMAC sequence number carried in the OSPF
// authentication trailer, RFC 2328 appendix D.1 and RFC 5709) for each
// neighboring router.
//
// Sequence numbers must never move backwards: a packet carrying a sequence
// number lower than the last accepted one is a replay and is rejected before
// any of its LSAs touch the database. A packet carrying the same sequence
// number may be an acceptable duplicate (for example a retransmission) and
// does not advance the record.
type AuthTracker struct {
	mu      sync.RWMutex
	lastSeq map[RouterID]uint64
}

// NewAuthTracker creates an empty authentication sequence tracker.
func NewAuthTracker() *AuthTracker {
	return &AuthTracker{lastSeq: make(map[RouterID]uint64)}
}

// Check reports whether seq is an acceptable cryptographic authentication
// sequence number for neighbor. Acceptable means equal to or greater than the
// previously accepted value; a strictly greater value advances the record.
func (t *AuthTracker) Check(neighbor RouterID, seq uint64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	last, known := t.lastSeq[neighbor]
	if known && seq < last {
		return false
	}
	if !known || seq > last {
		t.lastSeq[neighbor] = seq
	}
	return true
}

// Last returns the last accepted authentication sequence number for neighbor
// and whether the neighbor is known.
func (t *AuthTracker) Last(neighbor RouterID) (uint64, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	last, known := t.lastSeq[neighbor]
	return last, known
}

// Reset forgets every neighbor, restoring the tracker to its initial state.
func (t *AuthTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastSeq = make(map[RouterID]uint64)
}
