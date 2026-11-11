package netsec

import (
	"slices"
	"sync"
)

// entry is the internal, mutable representation. The exported Entry view is
// produced on demand so external callers can never mutate dictionary state.
type entry struct {
	id      ID
	word    []byte
	origins []Origin
	depth   int
	parents []ID
	dead    bool
}

func (e *entry) publicView() Entry {
	return Entry{
		ID:      e.id,
		Word:    cloneBytes(e.word),
		Origins: cloneOrigins(e.origins),
		Depth:   e.depth,
		Parents: cloneIDs(e.parents),
		Revoked: e.dead,
	}
}

// revokeKey identifies one extraction source: the input of one tenant.
type revokeKey struct {
	tenant Tenant
	input  Digest
}

// Dictionary is a concurrent, provenance-aware fuzzing dictionary. The zero
// value is not usable; create one with New.
type Dictionary struct {
	mu      sync.RWMutex
	entries map[ID]*entry
	order   []ID
	seq     ID

	// revoked records every revoked source. Membership blocks reuse of the
	// source and keeps revoked entries dead even if a public, same-bytes entry
	// is added later.
	revoked map[revokeKey]struct{}
}

// New returns an empty Dictionary.
func New() *Dictionary {
	return &Dictionary{
		entries: make(map[ID]*entry),
		revoked: make(map[revokeKey]struct{}),
	}
}

func validateOrigin(origin Origin) error {
	if origin.Tenant == "" {
		return ErrEmptyTenant
	}
	if origin.Input.IsZero() {
		return ErrZeroDigest
	}
	return nil
}

// Add registers a directly extracted token (derivation depth 0).
//
// Equal bytes added again from another origin produce a separate entry:
// content identity never merges provenance. Adding from an already revoked
// source fails with ErrRevoked so a revoked secret cannot be reintroduced.
func (d *Dictionary) Add(word []byte, origin Origin) (ID, error) {
	if len(word) == 0 {
		return 0, ErrEmptyWord
	}
	if err := validateOrigin(origin); err != nil {
		return 0, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.revoked[revokeKey{tenant: origin.Tenant, input: origin.Input}]; ok {
		return 0, ErrRevoked
	}

	id := d.nextIDLocked()
	d.entries[id] = &entry{
		id:      id,
		word:    cloneBytes(word),
		origins: sortOrigins([]Origin{origin}),
		depth:   0,
	}
	d.order = append(d.order, id)
	return id, nil
}

// Derive creates a new token from parents. The derived entry inherits the
// provenance closure of every parent (restrictions only accumulate) and gets
// depth one greater than the deepest parent. All parents must be live.
//
// A public parent can never cleanse a secret parent: the secret origin is
// retained in full. Deriving from a revoked entry or from any parent whose
// origins contain a revoked source fails with ErrRevoked.
func (d *Dictionary) Derive(word []byte, parents ...ID) (ID, error) {
	if len(word) == 0 {
		return 0, ErrEmptyWord
	}
	if len(parents) == 0 {
		return 0, ErrNoParents
	}

	parentIDs := append([]ID(nil), parents...)
	slices.Sort(parentIDs)
	parentIDs = slices.Compact(parentIDs)

	d.mu.Lock()
	defer d.mu.Unlock()

	var origins []Origin
	maxDepth := -1
	for _, pid := range parentIDs {
		parent, ok := d.entries[pid]
		if !ok {
			return 0, ErrUnknownEntry
		}
		if parent.dead || d.touchesRevokedLocked(parent.origins) {
			return 0, ErrRevoked
		}
		origins = append(origins, parent.origins...)
		if parent.depth > maxDepth {
			maxDepth = parent.depth
		}
	}

	id := d.nextIDLocked()
	d.entries[id] = &entry{
		id:      id,
		word:    cloneBytes(word),
		origins: sortOrigins(origins),
		depth:   maxDepth + 1,
		parents: parentIDs,
	}
	d.order = append(d.order, id)
	return id, nil
}

// Revoke invalidates every entry whose provenance contains input from tenant,
// across all derivation levels, and blocks future use of that source.
// Revocation is idempotent; revoking an unknown source is a no-op.
//
// Entries whose origins include the revoked source are invalidated even if
// they also carry public origins or origins of other tenants.
func (d *Dictionary) Revoke(tenant Tenant, input Digest) error {
	if tenant == "" {
		return ErrEmptyTenant
	}
	if input.IsZero() {
		return ErrZeroDigest
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	key := revokeKey{tenant: tenant, input: input}
	if _, ok := d.revoked[key]; ok {
		return nil
	}
	d.revoked[key] = struct{}{}

	for _, id := range d.order {
		e := d.entries[id]
		if e.dead {
			continue
		}
		for _, origin := range e.origins {
			if origin.Tenant == tenant && origin.Input == input {
				e.dead = true
				break
			}
		}
	}
	return nil
}

// RevokeTenant invalidates every entry carrying any non-public origin of
// tenant and marks all of the tenant's sources revoked. Public origins remain
// usable for other tenants because they carry no secret restriction.
func (d *Dictionary) RevokeTenant(tenant Tenant) error {
	if tenant == "" {
		return ErrEmptyTenant
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	for _, id := range d.order {
		e := d.entries[id]
		if e.dead {
			continue
		}
		for _, origin := range e.origins {
			if origin.Tenant == tenant && !origin.Public {
				e.dead = true
				d.revoked[revokeKey{tenant: tenant, input: origin.Input}] = struct{}{}
				break
			}
		}
	}
	return nil
}

// Entry returns a defensive copy of the entry identified by id regardless of
// authorization. It is intended for audit tooling; mutation hosts should use
// Snapshot to receive only entries they are authorized for.
func (d *Dictionary) Entry(id ID) (Entry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	e, ok := d.entries[id]
	if !ok {
		return Entry{}, ErrUnknownEntry
	}
	return e.publicView(), nil
}

// Eligible reports whether the entry is live and every secret tenant attached
// to it is contained in authorized. Entries with only public origins are
// eligible for any non-empty authorization set.
func (d *Dictionary) Eligible(id ID, authorized []Tenant) (bool, error) {
	if len(authorized) == 0 {
		return false, ErrEmptyTenant
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	e, ok := d.entries[id]
	if !ok {
		return false, ErrUnknownEntry
	}
	allowed := make(map[Tenant]struct{}, len(authorized))
	for _, tenant := range authorized {
		allowed[tenant] = struct{}{}
	}
	return e.eligibleMap(allowed), nil
}

// Snapshot returns an immutable view of the live entries visible inside the
// authorized tenant intersection. The returned snapshot keeps its own copies
// and is unaffected by later additions, derivations or revocations.
func (d *Dictionary) Snapshot(authorized []Tenant) (*Snapshot, error) {
	if len(authorized) == 0 {
		return nil, ErrEmptyTenant
	}

	allowed := make(map[Tenant]struct{}, len(authorized))
	for _, tenant := range authorized {
		if tenant == "" {
			return nil, ErrEmptyTenant
		}
		allowed[tenant] = struct{}{}
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	snap := &Snapshot{
		authorized: cloneTenants(authorized),
		entries:    make([]Entry, 0, len(d.order)),
		byWord:     make(map[string][]ID),
	}
	for _, id := range d.order {
		e := d.entries[id]
		if !e.eligibleMap(allowed) {
			continue
		}
		view := e.publicView()
		snap.entries = append(snap.entries, view)
		snap.byWord[string(view.Word)] = append(snap.byWord[string(view.Word)], view.ID)
	}
	return snap, nil
}

// Stats summarizes dictionary state for observability.
type Stats struct {
	Total   int
	Live    int
	Revoked int
	Sources int
}

// Stats returns current counters.
func (d *Dictionary) Stats() Stats {
	d.mu.RLock()
	defer d.mu.RUnlock()

	stats := Stats{Sources: len(d.revoked)}
	for _, id := range d.order {
		stats.Total++
		if d.entries[id].dead {
			stats.Revoked++
		} else {
			stats.Live++
		}
	}
	return stats
}

func (d *Dictionary) nextIDLocked() ID {
	d.seq++
	return d.seq
}

func (d *Dictionary) touchesRevokedLocked(origins []Origin) bool {
	for _, origin := range origins {
		if _, ok := d.revoked[revokeKey{tenant: origin.Tenant, input: origin.Input}]; ok {
			return true
		}
	}
	return false
}

func (e *entry) eligibleMap(allowed map[Tenant]struct{}) bool {
	if e.dead {
		return false
	}
	for _, origin := range e.origins {
		if origin.Public {
			continue
		}
		if _, ok := allowed[origin.Tenant]; !ok {
			return false
		}
	}
	return true
}
