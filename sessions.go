package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// MaxSessionIDLength bounds wire-supplied session identifiers to the same limit
// as any other length-prefixed wire field. Session IDs are opaque bytes, but
// they must be non-empty and are kept permanently for replay rejection.
const MaxSessionIDLength = MaxWireFieldLength

var (
	// ErrInvalidSessionID is returned for an empty or over-long session ID.
	ErrInvalidSessionID = errors.New("netsec: invalid session ID")
	// ErrSessionReuse is returned when a host sees a session ID twice. IDs are
	// consumed permanently, including after a completed handshake.
	ErrSessionReuse = errors.New("netsec: session ID reuse")
)

// SessionTable is a host-local replay/duplicate-session guard. Separate hosts
// normally have separate tables.
type SessionTable struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

// NewSessionTable returns an empty session table.
func NewSessionTable() *SessionTable {
	return &SessionTable{seen: make(map[string]struct{})}
}

// ValidateSessionID checks the structural session-ID rules.
func ValidateSessionID(id []byte) error {
	if len(id) == 0 {
		return fmt.Errorf("%w: empty", ErrInvalidSessionID)
	}
	if len(id) > MaxSessionIDLength {
		return fmt.Errorf("%w: length %d exceeds %d", ErrInvalidSessionID, len(id), MaxSessionIDLength)
	}
	return nil
}

// Reserve atomically consumes a session ID. A nil table performs no tracking.
func (t *SessionTable) Reserve(id []byte) error {
	if err := ValidateSessionID(id); err != nil {
		return err
	}
	if t == nil {
		return nil
	}
	key := string(id)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seen == nil {
		t.seen = make(map[string]struct{})
	}
	if _, ok := t.seen[key]; ok {
		return fmt.Errorf("%w: %x", ErrSessionReuse, id)
	}
	t.seen[key] = struct{}{}
	return nil
}

// Used reports whether a session ID was already consumed by this table.
func (t *SessionTable) Used(id []byte) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.seen[string(id)]
	return ok
}

// Reset clears the table. It is intended for tests or for hosts that have an
// explicit administrative epoch boundary, never for normal handshake cleanup.
func (t *SessionTable) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.seen = make(map[string]struct{})
	t.mu.Unlock()
}
