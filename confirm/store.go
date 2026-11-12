package confirm

import (
	"errors"
	"sync"
)

// ErrUserNotFound is returned when no state exists for the requested user.
var ErrUserNotFound = errors.New("confirm: unknown user")

// State is the persisted per-user verifier state.
type State struct {
	// Secret is the shared OCRA key (RFC 6287 key material).
	Secret []byte
	// LastCounter is the greatest counter value that has been accepted.
	LastCounter uint64
	// CounterUsed is false until the first successful verification, which
	// allows a freshly provisioned token to start at counter 0.
	CounterUsed bool
	// UsedChallenges records every accepted QH64 challenge so that each
	// challenge can be consumed at most once.
	UsedChallenges map[string]struct{}
}

// Store persists per-user State. Implementations must make Get/Save atomic
// with respect to one another; a deployment with multiple verifier instances
// needs a transactional backend rather than MemoryStore.
type Store interface {
	Get(userID string) (State, error)
	Save(userID string, state State) error
}

// MemoryStore is an in-memory Store for single-process services and tests.
type MemoryStore struct {
	mu    sync.RWMutex
	users map[string]State
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{users: make(map[string]State)}
}

// Provision registers a user with a shared secret and initial state.
func (m *MemoryStore) Provision(userID string, secret []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[userID] = State{
		Secret:         append([]byte(nil), secret...),
		UsedChallenges: make(map[string]struct{}),
	}
}

// Get returns a deep copy of the user's state.
func (m *MemoryStore) Get(userID string) (State, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.users[userID]
	if !ok {
		return State{}, ErrUserNotFound
	}
	return cloneState(st), nil
}

// Save replaces the user's state with a deep copy of state.
func (m *MemoryStore) Save(userID string, state State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[userID]; !ok {
		return ErrUserNotFound
	}
	m.users[userID] = cloneState(state)
	return nil
}

func cloneState(st State) State {
	out := State{
		Secret:         append([]byte(nil), st.Secret...),
		LastCounter:    st.LastCounter,
		CounterUsed:    st.CounterUsed,
		UsedChallenges: make(map[string]struct{}, len(st.UsedChallenges)),
	}
	for q := range st.UsedChallenges {
		out.UsedChallenges[q] = struct{}{}
	}
	return out
}
