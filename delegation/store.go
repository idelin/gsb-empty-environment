package delegation

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Store keeps every generation of every grant ever issued.
// Nothing is ever deleted: revocations are timestamped, so both
// "now" queries and historical point-in-time queries are exact.
type Store struct {
	mu     sync.RWMutex
	grants map[string]map[uint64]*Grant // id -> generation -> grant
}

func NewStore() *Store {
	return &Store{grants: make(map[string]map[uint64]*Grant)}
}

// Issue inserts a new grant. The generation is assigned automatically
// as one past the latest generation under the same ID, which makes
// same-name re-issuance explicit: the new grant is a fresh generation
// and does not resurrect children pinned to older generations.
func (s *Store) Issue(g Grant) (*Grant, error) {
	if g.ID == "" {
		return nil, errors.New("delegation: grant ID is required")
	}
	if g.NotAfter.Before(g.NotBefore) {
		return nil, fmt.Errorf("delegation: grant %q validity window is inverted", g.ID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	gens := s.grants[g.ID]
	if gens == nil {
		gens = make(map[uint64]*Grant)
		s.grants[g.ID] = gens
	}
	var max uint64
	for gen := range gens {
		if gen > max {
			max = gen
		}
	}
	stored := g
	stored.Generation = max + 1
	stored.Actions = append([]string(nil), g.Actions...)
	stored.Resources = append([]string(nil), g.Resources...)
	stored.Parents = append([]GrantRef(nil), g.Parents...)
	gens[stored.Generation] = &stored
	return &stored, nil
}

// Revoke marks one generation of a grant as revoked at time at.
// The revocation is effective immediately for any query at t >= at,
// and cascades to every descendant through normal validation.
func (s *Store) Revoke(ref GrantRef, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.get(ref)
	if !ok {
		return fmt.Errorf("delegation: cannot revoke unknown grant %s", ref)
	}
	if g.RevokedAt != nil {
		return fmt.Errorf("delegation: grant %s already revoked", ref)
	}
	g.RevokedAt = &at
	return nil
}

// Get returns the grant for an exact id+generation pair.
func (s *Store) Get(ref GrantRef) (*Grant, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.get(ref)
}

// Latest returns the newest generation issued under id.
func (s *Store) Latest(id string) (*Grant, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	gens := s.grants[id]
	if len(gens) == 0 {
		return nil, false
	}
	var max uint64
	for gen := range gens {
		if gen > max {
			max = gen
		}
	}
	return gens[max], true
}

func (s *Store) get(ref GrantRef) (*Grant, bool) {
	gens := s.grants[ref.ID]
	if gens == nil {
		return nil, false
	}
	g, ok := gens[ref.Generation]
	return g, ok
}
