package netsec

import "sync"

// EgoTable is the purely local mapping from root labels (the right-most
// component of a name) to zone public keys. It is the only source allowed to
// explain a root label: records fetched from remote zones can never create,
// shadow or redirect it. Deleting an ego makes every name rooted at that
// label unresolvable immediately, including names already in flight.
type EgoTable struct {
	mu sync.RWMutex
	// deleted labels stay in the map with a zero key so deletion is
	// distinguishable from a label that was never present.
	egos map[string]ZonePublicKey
}

// NewEgoTable creates an empty ego table.
func NewEgoTable() *EgoTable {
	return &EgoTable{egos: make(map[string]ZonePublicKey)}
}

// Put binds label to zoneKey. A zero key or empty label is rejected. Re-putting
// a label (for example after it was deleted) restores it.
func (t *EgoTable) Put(label string, zoneKey ZonePublicKey) error {
	if label == "" {
		return ErrInvalidName
	}
	if zoneKey.IsZero() {
		return ErrInvalidRecord
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.egos[label] = zoneKey
	return nil
}

// Lookup returns the zone key bound to label. ok is false when the label was
// never created or has been deleted; no remote data is consulted.
func (t *EgoTable) Lookup(label string) (zoneKey ZonePublicKey, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	zoneKey, ok = t.egos[label]
	if !ok || zoneKey.IsZero() {
		return ZonePublicKey{}, false
	}
	return zoneKey, true
}

// Delete removes the binding for label. Afterwards every name whose root label
// is label fails with ErrEgoNotFound until it is Put again. Deleting a missing
// label is a no-op.
func (t *EgoTable) Delete(label string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.egos, label)
}

// MemoryZoneStore is a concurrency-safe, map-backed ZoneStore used by tests
// and embedding scenarios that do not fetch zones over the network.
type MemoryZoneStore struct {
	mu    sync.RWMutex
	zones map[string]*Zone
}

// NewMemoryZoneStore creates an empty in-memory zone store.
func NewMemoryZoneStore() *MemoryZoneStore {
	return &MemoryZoneStore{zones: make(map[string]*Zone)}
}

// keyString renders a zone key as a stable map key.
func keyString(key ZonePublicKey) string {
	return string(key.Key)
}

// AddZone inserts or replaces the block for zone.Key.
func (s *MemoryZoneStore) AddZone(zone *Zone) error {
	if zone == nil || zone.Key.IsZero() {
		return ErrInvalidRecord
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.zones[keyString(zone.Key)] = zone
	return nil
}

// Zone implements ZoneStore.
func (s *MemoryZoneStore) Zone(key ZonePublicKey) (*Zone, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	zone, ok := s.zones[keyString(key)]
	return zone, ok
}
