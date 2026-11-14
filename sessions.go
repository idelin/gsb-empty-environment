package netsec

import (
	"context"
	"errors"
	"sync"
)

// ErrSessionSubjectMismatch indicates that the session exists but belongs to
// a different subject than the logout token.
var ErrSessionSubjectMismatch = errors.New("netsec: session subject does not match token")

// ErrInvalidSession indicates incomplete session data.
var ErrInvalidSession = errors.New("netsec: invalid session")

// Session is one application session. The same user may have several
// sessions; a logout token addresses exactly one of them by SessionID.
type Session struct {
	Issuer     string
	ID         string
	Subject    string
	Attributes map[string]string
}

// SessionStore is the minimal contract used by the logout service.
//
// TerminateSession must terminate only the session identified by issuer and
// sessionID. It must never terminate other sessions belonging to the same
// subject. If subject is non-empty it should match the stored session
// subject. The boolean result is true when an existing session was removed;
// an unknown session should return (false, nil) so logout remains idempotent.
type SessionStore interface {
	TerminateSession(ctx context.Context, issuer, sessionID, subject string) (bool, error)
}

// SessionLookup is an optional extension used by the front-channel handler to
// obtain the subject before terminating a session.
type SessionLookup interface {
	GetSession(issuer, sessionID string) (Session, bool)
}

// MemorySessionStore is a concurrency-safe in-memory SessionStore.
type MemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

// NewMemorySessionStore returns an empty session store.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{sessions: make(map[string]Session)}
}

// NewSessionStore is an alias for NewMemorySessionStore.
func NewSessionStore() *MemorySessionStore {
	return NewMemorySessionStore()
}

// Put stores a session. An existing session with the same issuer and ID is
// replaced.
func (s *MemorySessionStore) Put(session Session) error {
	if s == nil {
		return errors.New("netsec: nil session store")
	}
	if session.Issuer == "" || session.ID == "" || session.Subject == "" {
		return ErrInvalidSession
	}
	if session.Attributes != nil {
		session.Attributes = cloneAttributes(session.Attributes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]Session)
	}
	s.sessions[sessionKey(session.Issuer, session.ID)] = session
	return nil
}

// AddSession is a convenience wrapper around Put.
func (s *MemorySessionStore) AddSession(issuer, sessionID, subject string) error {
	return s.Put(Session{Issuer: issuer, ID: sessionID, Subject: subject})
}

// GetSession returns a copy of the requested session.
func (s *MemorySessionStore) GetSession(issuer, sessionID string) (Session, bool) {
	if s == nil {
		return Session{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionKey(issuer, sessionID)]
	if !ok {
		return Session{}, false
	}
	session.Attributes = cloneAttributes(session.Attributes)
	return session, true
}

// HasSession reports whether the session is active.
func (s *MemorySessionStore) HasSession(issuer, sessionID string) bool {
	_, ok := s.GetSession(issuer, sessionID)
	return ok
}

// TerminateSession removes only the addressed session.
func (s *MemorySessionStore) TerminateSession(ctx context.Context, issuer, sessionID, subject string) (bool, error) {
	if s == nil {
		return false, errors.New("netsec: nil session store")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(issuer, sessionID)
	session, ok := s.sessions[key]
	if !ok {
		return false, nil
	}
	if subject != "" && session.Subject != subject {
		return false, ErrSessionSubjectMismatch
	}
	delete(s.sessions, key)
	return true, nil
}

// Count returns the number of active sessions.
func (s *MemorySessionStore) Count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

func sessionKey(issuer, sessionID string) string {
	return issuer + "\x00" + sessionID
}

func cloneAttributes(attributes map[string]string) map[string]string {
	if attributes == nil {
		return nil
	}
	clone := make(map[string]string, len(attributes))
	for key, value := range attributes {
		clone[key] = value
	}
	return clone
}
