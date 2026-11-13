package netsec

import "sync"

// Personality is a host-side token for a whitelisted personality routine.
// It deliberately contains no executable function pointer.
type Personality struct {
	ID     PersonalityID `json:"id"`
	Digest ModuleDigest  `json:"digest"`
}

// PersonalityRegistry is a host-owned whitelist keyed by module digest and
// personality ID. The zero value is usable after Register initializes it.
type PersonalityRegistry struct {
	mu      sync.RWMutex
	entries map[ModuleDigest]map[PersonalityID]struct{}
}

// Register whitelists id for exactly one module digest. The same ID may be
// registered under different digests; metadata from one module cannot use a
// personality bound to another module.
func (r *PersonalityRegistry) Register(digest ModuleDigest, id PersonalityID) error {
	if r == nil {
		return &ValidationError{Kind: ErrPersonality, Detail: "nil registry"}
	}
	if id == "" {
		return &ValidationError{Kind: ErrPersonality, Detail: "empty personality id"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[ModuleDigest]map[PersonalityID]struct{})
	}
	byID := r.entries[digest]
	if byID == nil {
		byID = make(map[PersonalityID]struct{})
		r.entries[digest] = byID
	}
	byID[id] = struct{}{}
	return nil
}

// Resolve performs the only allowed name-to-personality lookup.
func (r *PersonalityRegistry) Resolve(digest ModuleDigest, id PersonalityID) (Personality, bool) {
	if r == nil {
		return Personality{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	byID := r.entries[digest]
	if byID == nil {
		return Personality{}, false
	}
	if _, ok := byID[id]; !ok {
		return Personality{}, false
	}
	return Personality{ID: id, Digest: digest}, true
}

// NewPersonalityRegistry returns an empty host whitelist.
func NewPersonalityRegistry() *PersonalityRegistry { return &PersonalityRegistry{} }
