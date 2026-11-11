package passkey

import (
	"sync"
	"time"
)

// PendingChallenge is a server-side WebAuthn assertion challenge created when
// the login flow starts. It is bound to the relying party ID, the expected
// origin and the cryptographic challenge bytes echoed back by the client.
type PendingChallenge struct {
	ID        string
	Challenge []byte
	RPID      string
	Origin    string
	// UserID is optional. When set, only credentials owned by that user may
	// answer the challenge.
	UserID    []byte
	ExpiresAt time.Time
}

func (c PendingChallenge) expired(now time.Time) bool {
	return !c.ExpiresAt.IsZero() && now.After(c.ExpiresAt)
}

// ChallengeStore is an in-memory, single-use challenge store. Consume is
// serialized under a mutex so that concurrent assertions carrying the same
// challenge ID can never both succeed.
type ChallengeStore struct {
	mu       sync.Mutex
	m        map[string]PendingChallenge
	consumed map[string]time.Time
	Now      func() time.Time
}

func NewChallengeStore() *ChallengeStore {
	return &ChallengeStore{
		m:        make(map[string]PendingChallenge),
		consumed: make(map[string]time.Time),
	}
}

func (s *ChallengeStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// consumedRetention bounds memory used by tombstones of spent challenges.
const consumedRetention = time.Hour

func (s *ChallengeStore) pruneConsumed(now time.Time) {
	for id, at := range s.consumed {
		if now.Sub(at) > consumedRetention {
			delete(s.consumed, id)
		}
	}
}

// Put stores a pending challenge, replacing any previous entry with the same ID.
func (s *ChallengeStore) Put(c PendingChallenge) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[c.ID] = c
}

// Peek returns the challenge without consuming it. Expired entries are removed.
func (s *ChallengeStore) Peek(id string) (PendingChallenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneConsumed(now)
	if _, used := s.consumed[id]; used {
		return PendingChallenge{}, ErrChallengeAlreadyUsed
	}
	c, ok := s.m[id]
	if !ok {
		return PendingChallenge{}, ErrChallengeNotFound
	}
	if c.expired(now) {
		delete(s.m, id)
		return PendingChallenge{}, ErrChallengeExpired
	}
	return c, nil
}

// Consume atomically reads and deletes the challenge. At most one caller can
// ever receive a given challenge; every subsequent caller gets
// ErrChallengeAlreadyUsed. Expired entries report ErrChallengeExpired.
func (s *ChallengeStore) Consume(id string) (PendingChallenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneConsumed(now)
	if _, used := s.consumed[id]; used {
		return PendingChallenge{}, ErrChallengeAlreadyUsed
	}
	c, ok := s.m[id]
	if !ok {
		return PendingChallenge{}, ErrChallengeNotFound
	}
	if c.expired(now) {
		delete(s.m, id)
		return PendingChallenge{}, ErrChallengeExpired
	}
	delete(s.m, id)
	s.consumed[id] = now
	return c, nil
}

// Credential is a registered passkey. PublicKey is the raw P-256 point in
// uncompressed form (0x04 || X || Y, 65 bytes), matching a COSE EC2 key.
type Credential struct {
	ID             []byte
	UserID         []byte
	RPID           string
	PublicKey      []byte
	SignCount      uint32
	BackupEligible bool
}

// CredentialStore is an in-memory credential registry.
type CredentialStore struct {
	mu sync.Mutex
	m  map[string]*Credential
}

func NewCredentialStore() *CredentialStore {
	return &CredentialStore{m: make(map[string]*Credential)}
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// Put registers or replaces a credential.
func (s *CredentialStore) Put(c Credential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := c
	cp.ID = cloneBytes(c.ID)
	cp.UserID = cloneBytes(c.UserID)
	cp.PublicKey = cloneBytes(c.PublicKey)
	s.m[string(c.ID)] = &cp
}

// Get returns a copy of the credential so callers cannot mutate stored state.
func (s *CredentialStore) Get(id []byte) (Credential, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.m[string(id)]
	if !ok {
		return Credential{}, false
	}
	return Credential{
		ID:             cloneBytes(c.ID),
		UserID:         cloneBytes(c.UserID),
		RPID:           c.RPID,
		PublicKey:      cloneBytes(c.PublicKey),
		SignCount:      c.SignCount,
		BackupEligible: c.BackupEligible,
	}, true
}

// CompareAndSwapCount updates the signature counter only when it still matches
// prev, reporting whether the swap happened.
func (s *CredentialStore) CompareAndSwapCount(id []byte, prev, next uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.m[string(id)]
	if !ok || c.SignCount != prev {
		return false
	}
	c.SignCount = next
	return true
}

// SetCountIfGreater raises the stored counter to v when v is larger. It is used
// for backup-eligible credentials whose counter is advisory only.
func (s *CredentialStore) SetCountIfGreater(id []byte, v uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.m[string(id)]; ok && v > c.SignCount {
		c.SignCount = v
	}
}

// Session is issued exactly once per successfully consumed challenge.
type Session struct {
	Token        string
	CredentialID []byte
	UserID       []byte
	CreatedAt    time.Time
}

// SessionStore is an in-memory session registry.
type SessionStore struct {
	mu sync.Mutex
	m  map[string]Session
}

func NewSessionStore() *SessionStore {
	return &SessionStore{m: make(map[string]Session)}
}

func (s *SessionStore) Add(sess Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[sess.Token] = sess
}

func (s *SessionStore) Get(token string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[token]
	return sess, ok
}

// Len reports how many sessions have been issued.
func (s *SessionStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}
