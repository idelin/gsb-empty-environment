package netsec

import "sync"

// AuthOp is a single authorization change. Authorization versions form a
// monotonic sequence that is independent from content versions.
type AuthOp struct {
	Seq      uint64
	ObjectID string
	Revoked  bool
}

// AuthLog is the tenant authorization journal. Every revocation gets the next
// authorization sequence number; pools replay this journal to advance their
// local revocation watermark.
type AuthLog struct {
	mu      sync.Mutex
	head    uint64
	ops     []AuthOp
	revoked map[string]uint64
}

func NewAuthLog() *AuthLog {
	return &AuthLog{revoked: make(map[string]uint64)}
}

// Revoke records a revocation and returns its authorization sequence number.
func (l *AuthLog) Revoke(objectID string) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.head++
	l.ops = append(l.ops, AuthOp{Seq: l.head, ObjectID: objectID, Revoked: true})
	l.revoked[objectID] = l.head
	return l.head
}

// Head returns the latest authorization sequence number: the revocation
// barrier a migration target must reach before read routing may switch.
func (l *AuthLog) Head() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.head
}

// Delta returns the ops with Seq > since, plus the current head.
func (l *AuthLog) Delta(since uint64) ([]AuthOp, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []AuthOp
	for _, op := range l.ops {
		if op.Seq > since {
			out = append(out, op)
		}
	}
	return out, l.head
}

// RevokedAt reports whether objectID is revoked and at which authorization
// sequence number.
func (l *AuthLog) RevokedAt(objectID string) (uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	seq, ok := l.revoked[objectID]
	return seq, ok
}
