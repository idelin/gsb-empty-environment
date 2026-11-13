package netsec

import (
	"sync"
	"time"
)

// Session is the authoritative server-side state of a resumable TLS 1.2
// session. It is indexed by the opaque session ID carried inside a ticket.
//
// The extended master secret flag lives here and nowhere else: it is never
// serialized into the resumption ticket, so a client cannot present a forged
// or stripped flag during resumption.
type Session struct {
	ID                   []byte
	MasterSecret         []byte
	Hash                 HashFunc
	KeyBlockLength       int
	ExtendedMasterSecret bool
	CreatedAt            time.Time
	Lifetime             time.Duration
}

// Valid reports whether the session is still within its negotiated lifetime.
func (s *Session) Valid(now time.Time) bool {
	if s == nil || s.Lifetime <= 0 {
		return false
	}
	return now.Before(s.CreatedAt.Add(s.Lifetime))
}

// SessionStore is the server-side index from opaque session IDs to session
// state. Implementations must be safe for concurrent use.
type SessionStore interface {
	Put(*Session) error
	Get(id []byte) (*Session, error)
	Delete(id []byte) error
}

// MemorySessionStore is an in-memory SessionStore protected by a mutex.
type MemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	now      func() time.Time
}

// NewMemorySessionStore returns an empty in-memory session store.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{
		sessions: make(map[string]*Session),
		now:      time.Now,
	}
}

// Put stores a copy of the session keyed by its ID.
func (m *MemorySessionStore) Put(session *Session) error {
	if session == nil || len(session.ID) == 0 {
		return ErrSessionUnknown
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[string(session.ID)] = cloneSession(session)
	return nil
}

// Get returns a copy of the session, rejecting unknown IDs and expired
// sessions which are also evicted.
func (m *MemorySessionStore) Get(id []byte) (*Session, error) {
	m.mu.RLock()
	session, ok := m.sessions[string(id)]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrSessionUnknown
	}
	if !session.Valid(m.now()) {
		_ = m.Delete(id)
		return nil, ErrSessionExpired
	}
	return cloneSession(session), nil
}

// Delete removes a session. Deleting a missing ID is not an error.
func (m *MemorySessionStore) Delete(id []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, string(id))
	return nil
}

func cloneSession(session *Session) *Session {
	copy := *session
	copy.ID = append([]byte(nil), session.ID...)
	copy.MasterSecret = append([]byte(nil), session.MasterSecret...)
	return &copy
}

// ClientSession is the client-side record paired with an opaque resumption
// ticket. The extended master secret flag is retained only in the client's
// local cache; it is never read back from the wire ticket.
type ClientSession struct {
	Ticket               []byte
	MasterSecret         []byte
	Hash                 HashFunc
	KeyBlockLength       int
	ExtendedMasterSecret bool
}

// ClientSessionCache stores client session state keyed by an application
// defined name (for example a host:port endpoint). Implementations must be
// safe for concurrent use.
type ClientSessionCache interface {
	Put(name string, session *ClientSession)
	Get(name string) (*ClientSession, bool)
}

// MemoryClientSessionCache is a mutex-protected ClientSessionCache.
type MemoryClientSessionCache struct {
	mu       sync.RWMutex
	sessions map[string]*ClientSession
}

// NewMemoryClientSessionCache returns an empty in-memory client cache.
func NewMemoryClientSessionCache() *MemoryClientSessionCache {
	return &MemoryClientSessionCache{sessions: make(map[string]*ClientSession)}
}

// Put stores a copy of the client session under name.
func (c *MemoryClientSessionCache) Put(name string, session *ClientSession) {
	if session == nil {
		return
	}
	copy := *session
	copy.Ticket = append([]byte(nil), session.Ticket...)
	copy.MasterSecret = append([]byte(nil), session.MasterSecret...)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[name] = &copy
}

// Get returns a copy of the cached session and whether name was present.
func (c *MemoryClientSessionCache) Get(name string) (*ClientSession, bool) {
	c.mu.RLock()
	session, ok := c.sessions[name]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	copy := *session
	copy.Ticket = append([]byte(nil), session.Ticket...)
	copy.MasterSecret = append([]byte(nil), session.MasterSecret...)
	return &copy, true
}
