package netsec

import (
	"bytes"
	"fmt"
	"sync"
)

// Store is the durable admission state. Commit must atomically check all
// preconditions and then record the Welcome consumption, KeyPackage
// consumption and new group state. If Commit returns an error, it must leave
// every piece of state unchanged.
type Store interface {
	// LoadGroup returns a snapshot of the group state. It must not expose a
	// mutable internal pointer. ok is false when the group is unknown.
	LoadGroup(groupID []byte) (state GroupState, ok bool)

	// WelcomeConsumed reports whether welcomeID has already been admitted.
	WelcomeConsumed(welcomeID []byte) bool

	// KeyPackageConsumed reports whether a KeyPackage reference has already
	// been consumed in any group.
	KeyPackageConsumed(keyPackageRef []byte) bool

	// Commit atomically checks that welcomeID and KeyPackageRef are unconsumed
	// and that the current group epoch still equals ExpectedEpoch (when
	// HasGroup is true), then stores NewState and both consumptions.
	Commit(commit Commit) error
}

// Commit describes one atomic admission transaction.
type Commit struct {
	WelcomeID     []byte
	KeyPackageRef []byte
	GroupID       []byte
	HasGroup      bool
	ExpectedEpoch uint64
	NewState      GroupState
}

// MemoryStore is the default in-process Store. Its methods are safe for
// concurrent use.
type MemoryStore struct {
	mu          sync.Mutex
	groups      map[string]GroupState
	welcomes    map[string]struct{}
	keyPackages map[string]struct{}
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		groups:      make(map[string]GroupState),
		welcomes:    make(map[string]struct{}),
		keyPackages: make(map[string]struct{}),
	}
}

func (s *MemoryStore) LoadGroup(groupID []byte) (GroupState, bool) {
	if s == nil {
		return GroupState{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.groups[string(groupID)]
	if !ok {
		return GroupState{}, false
	}
	return cloneGroupState(state), true
}

func (s *MemoryStore) WelcomeConsumed(welcomeID []byte) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.welcomes[string(welcomeID)]
	return ok
}

func (s *MemoryStore) KeyPackageConsumed(keyPackageRef []byte) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.keyPackages[string(keyPackageRef)]
	return ok
}

func (s *MemoryStore) Commit(commit Commit) error {
	if s == nil {
		return ErrNotConfigured
	}
	if len(commit.WelcomeID) == 0 {
		return fmt.Errorf("%w: empty welcome id", ErrInvalidWelcome)
	}
	if len(commit.KeyPackageRef) == 0 {
		return fmt.Errorf("%w: empty key package reference", ErrInvalidKeyPackage)
	}
	if len(commit.GroupID) == 0 {
		return fmt.Errorf("%w: empty group id", ErrInvalidWelcome)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.welcomes[string(commit.WelcomeID)]; ok {
		return fmt.Errorf("%w: %x", ErrWelcomeAlreadyConsumed, commit.WelcomeID)
	}
	if _, ok := s.keyPackages[string(commit.KeyPackageRef)]; ok {
		return fmt.Errorf("%w: %x", ErrKeyPackageConsumed, commit.KeyPackageRef)
	}

	current, exists := s.groups[string(commit.GroupID)]
	if commit.HasGroup {
		if !exists {
			return fmt.Errorf("%w: group disappeared", ErrStateConflict)
		}
		if current.Epoch != commit.ExpectedEpoch {
			return fmt.Errorf("%w: epoch %d changed to %d", ErrStateConflict, commit.ExpectedEpoch, current.Epoch)
		}
		if commit.NewState.Epoch <= current.Epoch {
			return fmt.Errorf("%w: %d -> %d", ErrEpochRollback, current.Epoch, commit.NewState.Epoch)
		}
	} else if exists {
		return fmt.Errorf("%w: concurrent group creation at epoch %d", ErrStateConflict, current.Epoch)
	}

	if !bytes.Equal(commit.NewState.GroupID, commit.GroupID) {
		return fmt.Errorf("%w: new state group id differs", ErrGroupMismatch)
	}

	s.groups[string(commit.GroupID)] = cloneGroupState(commit.NewState)
	s.welcomes[string(commit.WelcomeID)] = struct{}{}
	s.keyPackages[string(commit.KeyPackageRef)] = struct{}{}
	return nil
}

// SeedGroup installs initial durable group state for tests or for a client
// restoring a previous session. It does not mark any Welcome or KeyPackage as
// consumed.
func (s *MemoryStore) SeedGroup(state GroupState) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups[string(state.GroupID)] = cloneGroupState(state)
}
