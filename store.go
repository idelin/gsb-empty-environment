package netsec

import (
	"sync"
)

// Store persists one AccountState per account. Commit must atomically replace
// the state only when its current version equals expectedVersion and must set
// the stored state's Version to expectedVersion+1.
type Store interface {
	Load(accountID AccountID) (AccountState, error)
	Commit(accountID AccountID, expectedVersion uint64, next AccountState) error
}

// MemoryStore is a concurrency-safe in-memory Store with deterministic failure
// injection for crash and retry tests.
type MemoryStore struct {
	mu             sync.Mutex
	accounts       map[AccountID]AccountState
	nextCommitErr  error
	persistOnError bool
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{accounts: make(map[AccountID]AccountState)}
}

func (s *MemoryStore) Load(accountID AccountID) (AccountState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.accounts[accountID]
	if !ok {
		return AccountState{}, ErrAccountNotFound
	}
	return state.Clone(), nil
}

func (s *MemoryStore) Commit(accountID AccountID, expectedVersion uint64, next AccountState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.accounts[accountID]
	if expectedVersion == 0 {
		if exists {
			return ErrAccountExists
		}
	} else if !exists || current.Version != expectedVersion {
		return ErrVersionConflict
	}

	next = next.Clone()
	next.Version = expectedVersion + 1

	if s.nextCommitErr != nil {
		err := s.nextCommitErr
		s.nextCommitErr = nil
		persist := s.persistOnError
		s.persistOnError = false
		if persist {
			s.accounts[accountID] = next
		}
		return err
	}
	s.accounts[accountID] = next
	return nil
}

// FailNextCommit makes the next Commit fail without changing stored state.
func (s *MemoryStore) FailNextCommit(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		err = ErrCommitFailure
	}
	s.nextCommitErr = err
	s.persistOnError = false
}

// FailNextCommitAfterPersist makes the next Commit persist its new state and
// then return an error, modelling a crash after the durable write but before
// the caller receives a response.
func (s *MemoryStore) FailNextCommitAfterPersist(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		err = ErrCommitFailure
	}
	s.nextCommitErr = err
	s.persistOnError = true
}

// Snapshot returns a copy of an account state for inspection.
func (s *MemoryStore) Snapshot(accountID AccountID) (AccountState, bool) {
	state, err := s.Load(accountID)
	if err != nil {
		return AccountState{}, false
	}
	return state, true
}
