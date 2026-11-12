package netsec

import (
	"context"
	"errors"
	"sync"
)

// StoreSnapshot is an immutable point-in-time view of a key host. All reads
// inside one transaction go through the same snapshot, so rebinding an alias
// mid-resolution cannot change the result of any reference in that
// transaction.
type StoreSnapshot interface {
	// LookupAlias returns the binding of ref as it existed at snapshot time.
	LookupAlias(ref AliasRef) (Binding, bool)
	// LookupKey returns key material owned by account at snapshot time.
	LookupKey(account AccountID, id KeyID) (PublicKey, bool)
}

// HostStore is the persistence boundary implemented by key hosts.
type HostStore interface {
	// Snapshot returns a consistent, immutable read view for a transaction.
	Snapshot(ctx context.Context) (StoreSnapshot, error)
	// LookupAlias reads the current binding outside of any transaction.
	LookupAlias(ref AliasRef) (Binding, bool)
	// LookupKey reads current key material outside of any transaction.
	LookupKey(account AccountID, id KeyID) (PublicKey, bool)
}

// ErrStaleVersion indicates a rebind used a binding version that is not the
// immediate successor of the current one.
var ErrStaleVersion = errors.New("netsec: stale binding version")

type storedKey struct {
	account AccountID
	id      KeyID
}

// MemoryStore is a goroutine-safe in-memory HostStore intended for build
// service integrations and tests. Bindings are versioned: every successful
// rebind produces a strictly increasing version number.
type MemoryStore struct {
	mu       sync.RWMutex
	bindings map[AliasRef]Binding
	keys     map[storedKey]PublicKey
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		bindings: make(map[AliasRef]Binding),
		keys:     make(map[storedKey]PublicKey),
	}
}

// Snapshot implements HostStore and returns a deep, immutable copy.
func (s *MemoryStore) Snapshot(_ context.Context) (StoreSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snap := &memorySnapshot{
		bindings: make(map[AliasRef]Binding, len(s.bindings)),
		keys:     make(map[storedKey]PublicKey, len(s.keys)),
	}
	for ref, binding := range s.bindings {
		snap.bindings[ref] = cloneBinding(binding)
	}
	for key, pub := range s.keys {
		snap.keys[key] = cloneKey(pub)
	}
	return snap, nil
}

// LookupAlias implements HostStore.
func (s *MemoryStore) LookupAlias(ref AliasRef) (Binding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.bindings[ref]
	return cloneBinding(binding), ok
}

// LookupKey implements HostStore.
func (s *MemoryStore) LookupKey(account AccountID, id KeyID) (PublicKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pub, ok := s.keys[storedKey{account: account, id: id}]
	return cloneKey(pub), ok
}

// PutBinding inserts or rebinds an alias. When binding.Version is zero the
// store assigns the next version; otherwise it must equal current+1 (or 1 for
// a new alias). The stored binding, with its final version, is returned.
func (s *MemoryStore) PutBinding(binding Binding) (Binding, error) {
	if err := validateBinding(binding); err != nil {
		return Binding{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.bindings[binding.Ref]
	next := uint64(1)
	if exists {
		next = current.Version + 1
	}
	if binding.Version != 0 && binding.Version != next {
		return Binding{}, ErrStaleVersion
	}
	binding.Version = next
	s.bindings[binding.Ref] = cloneBinding(binding)
	return cloneBinding(binding), nil
}

// Rebind is a convenience that points ref at target with the next version.
func (s *MemoryStore) Rebind(ref AliasRef, target Target) (Binding, error) {
	return s.PutBinding(Binding{Ref: ref, Target: target})
}

// PutKey stores key material. When pub.Fingerprint is empty it is derived
// from pub.DER; when both are present they must agree.
func (s *MemoryStore) PutKey(pub PublicKey) (PublicKey, error) {
	if pub.Account == "" || pub.ID == "" {
		return PublicKey{}, ErrInvalidKey
	}
	if len(pub.DER) > 0 {
		computed := SHA256Fingerprint(pub.DER)
		switch {
		case pub.Fingerprint == "":
			pub.Fingerprint = computed
		case pub.Fingerprint != computed:
			return PublicKey{}, ErrFingerprintMismatch
		}
	}
	if pub.Fingerprint == "" {
		return PublicKey{}, ErrInvalidKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := cloneKey(pub)
	s.keys[storedKey{account: pub.Account, id: pub.ID}] = stored
	return cloneKey(stored), nil
}

type memorySnapshot struct {
	bindings map[AliasRef]Binding
	keys     map[storedKey]PublicKey
}

func (s *memorySnapshot) LookupAlias(ref AliasRef) (Binding, bool) {
	binding, ok := s.bindings[ref]
	return cloneBinding(binding), ok
}

func (s *memorySnapshot) LookupKey(account AccountID, id KeyID) (PublicKey, bool) {
	pub, ok := s.keys[storedKey{account: account, id: id}]
	return cloneKey(pub), ok
}

func validateBinding(binding Binding) error {
	if !binding.Ref.Valid() {
		return ErrInvalidReference
	}
	switch binding.Target.Kind {
	case TargetAlias:
		if binding.Target.Alias == nil || !binding.Target.Alias.Valid() {
			return ErrInvalidBinding
		}
	case TargetKey:
		if binding.Target.Key == "" {
			return ErrInvalidBinding
		}
	default:
		return ErrInvalidBinding
	}
	return nil
}

func cloneBinding(binding Binding) Binding {
	if binding.Target.Alias != nil {
		alias := *binding.Target.Alias
		binding.Target.Alias = &alias
	}
	return binding
}

func cloneKey(pub PublicKey) PublicKey {
	if pub.DER != nil {
		pub.DER = append([]byte(nil), pub.DER...)
	}
	return pub
}
