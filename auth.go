package netsec

import (
	"context"
	"sync"
)

// Authorizer answers per-object authorization questions. Implementations are
// expected to be safe for concurrent use.
type Authorizer interface {
	// Allowed reports whether the object may be restored. When it returns
	// false, reason explains why (for example "no grant recorded" or
	// "grant revoked"). A non-nil error indicates a transient failure and must
	// be treated as deny-by-default by callers.
	Allowed(ctx context.Context, id ObjectID) (allowed bool, reason string, err error)
}

// Grant is the recorded authorization state for one object.
type Grant struct {
	Allowed bool
	Reason  string
}

// GrantTable is a mutable, in-memory Authorizer keyed by object ID. Objects
// without an explicit entry are denied. It models a live permission backend
// whose entries can be revoked between planning and publishing.
type GrantTable struct {
	mu     sync.RWMutex
	grants map[ObjectID]Grant
}

// NewGrantTable creates a GrantTable in which only the listed objects are
// allowed. Every other object is denied until explicitly granted.
func NewGrantTable(allowed ...ObjectID) *GrantTable {
	t := &GrantTable{grants: make(map[ObjectID]Grant, len(allowed))}
	for _, id := range allowed {
		t.grants[id] = Grant{Allowed: true, Reason: "grant present"}
	}
	return t
}

// Allowed implements Authorizer. Unknown objects are denied rather than allowed.
func (t *GrantTable) Allowed(_ context.Context, id ObjectID) (bool, string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	grant, ok := t.grants[id]
	if !ok {
		return false, "no grant recorded", nil
	}
	return grant.Allowed, grant.Reason, nil
}

// Allow records an authorizing grant for the object.
func (t *GrantTable) Allow(id ObjectID) {
	t.Set(id, true, "grant present")
}

// Revoke removes the object's authorization. A revoked object is denied with a
// reason that distinguishes revocation from never having a grant.
func (t *GrantTable) Revoke(id ObjectID) {
	t.Set(id, false, "grant revoked")
}

// Set records an explicit authorization state and reason for the object.
func (t *GrantTable) Set(id ObjectID, allowed bool, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.grants[id] = Grant{Allowed: allowed, Reason: reason}
}

// Snapshot returns a point-in-time copy of every recorded grant.
func (t *GrantTable) Snapshot() map[ObjectID]Grant {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[ObjectID]Grant, len(t.grants))
	for id, grant := range t.grants {
		out[id] = grant
	}
	return out
}
