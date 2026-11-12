package netsec

import (
	"sync"
	"time"
)

// Clock abstracts time so deadline behavior is deterministic in tests.
type Clock interface {
	Now() time.Time
}

// systemClock is the default wall-clock implementation.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Store persists per-user recovery state. Implementations must provide
// compare-and-swap semantics: CommitCAS applies mutate only when the stored
// version still equals base.Version, otherwise returns ErrConflict.
type Store interface {
	Load(userID string) (*State, error)
	CommitCAS(userID string, base *State, mutate func(*State) (*State, error)) (*State, error)
}

// MemoryStore is an in-memory Store for the identity host and for tests.
type MemoryStore struct {
	mu     sync.Mutex
	states map[string]*State

	// failCAS, when non-nil, is consulted before each CAS; returning an error
	// forces the manager's retry path (used by concurrency tests).
	failCAS func(userID string, base *State) error
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{states: make(map[string]*State)}
}

// Provision creates the initial recovery state for a user. It returns
// ErrInvalidChannel for malformed channels and fails if the user already
// exists, so provisioning cannot silently overwrite a recovery channel.
func (m *MemoryStore) Provision(userID string, active Channel) (*State, error) {
	if !active.Valid() {
		return nil, ErrInvalidChannel
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.states[userID]; ok {
		return nil, ErrStateVersion
	}
	state := &State{Version: 1, Active: active}
	m.states[userID] = state
	return state.Clone(), nil
}

// Load returns a deep copy of the user's state, or ErrNoState if unknown.
func (m *MemoryStore) Load(userID string) (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.states[userID]
	if !ok {
		return nil, ErrNoState
	}
	return state.Clone(), nil
}

// CommitCAS applies mutate under compare-and-swap protection.
func (m *MemoryStore) CommitCAS(userID string, base *State, mutate func(*State) (*State, error)) (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	current, ok := m.states[userID]
	if !ok {
		return nil, ErrNoState
	}
	if current.Version != base.Version {
		return nil, ErrConflict
	}
	if m.failCAS != nil {
		if err := m.failCAS(userID, base); err != nil {
			return nil, err
		}
	}

	next, err := mutate(current.Clone())
	if err != nil {
		return nil, err
	}
	if next == nil {
		return nil, ErrStateVersion
	}
	if next.Version <= base.Version {
		return nil, ErrStateVersion
	}
	m.states[userID] = next
	return next.Clone(), nil
}

// setConflictHook installs a test hook that can force CAS failures.
func (m *MemoryStore) setConflictHook(fn func(userID string, base *State) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCAS = fn
}
