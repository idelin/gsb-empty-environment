package netsec

import (
	"fmt"
	"sync"
)

// Store persists account state. Save must be atomic: if it returns an error,
// the previously stored state must remain intact. The Engine always mutates a
// clone and calls Save before publishing the new state.
type Store interface {
	Load(accountID string) (*AccountState, error)
	Save(state *AccountState) error
}

// MemoryStore is an in-process Store useful for tests and ephemeral hosts.
type MemoryStore struct {
	mu       sync.Mutex
	accounts map[string]*AccountState
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{accounts: make(map[string]*AccountState)}
}

// Load implements Store.
func (s *MemoryStore) Load(accountID string) (*AccountState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.accounts[accountID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
	}
	return state.Clone(), nil
}

// Save implements Store.
func (s *MemoryStore) Save(state *AccountState) error {
	if state == nil {
		return fmt.Errorf("%w: nil account state", ErrStorage)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[state.ID] = state.Clone()
	return nil
}

// Delete removes an account. It is primarily useful for test cleanup.
func (s *MemoryStore) Delete(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.accounts, accountID)
}
