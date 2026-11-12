package taskpkg

import (
	"math"
	"sort"
	"sync"
)

type identityKey struct {
	tenant     string
	generation uint64
}

func keyOf(id Identity) identityKey {
	return identityKey{tenant: id.Tenant, generation: id.Generation}
}

type blockRecord struct {
	id       BlockID
	identity Identity
	offset   uint64
	data     []byte
}

func (b *blockRecord) end() uint64 {
	return b.offset + uint64(len(b.data))
}

// Store owns immutable evidence blocks. All accepted bytes are copied at the
// Add boundary, so pooled probe buffers can be reused immediately.
type Store struct {
	mu sync.RWMutex

	nextID   BlockID
	blocks   map[BlockID]*blockRecord
	revoked  map[identityKey]uint64
	tenants  map[string]uint64
	revEpoch uint64
	gens     map[string]uint64
}

// NewStore returns an empty evidence store.
func NewStore() *Store {
	return &Store{
		blocks:  make(map[BlockID]*blockRecord),
		revoked: make(map[identityKey]uint64),
		tenants: make(map[string]uint64),
		gens:    make(map[string]uint64),
	}
}

func (s *Store) initLocked() {
	if s.blocks == nil {
		s.blocks = make(map[BlockID]*blockRecord)
	}
	if s.revoked == nil {
		s.revoked = make(map[identityKey]uint64)
	}
	if s.tenants == nil {
		s.tenants = make(map[string]uint64)
	}
	if s.gens == nil {
		s.gens = make(map[string]uint64)
	}
}

// OpenConnection mints a monotonically increasing connection generation for a
// tenant. The probe should pass this identity to Add instead of accepting a
// generation from packet bytes. A revoked tenant cannot open a new connection.
func (s *Store) OpenConnection(tenant string) (Identity, error) {
	if s == nil {
		return Identity{}, ErrEvidenceInvalid
	}
	id := Identity{Tenant: tenant}
	if err := id.validate(); err != nil {
		return Identity{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if _, ok := s.tenants[tenant]; ok {
		return Identity{}, ErrRevoked
	}
	s.gens[tenant]++
	id.Generation = s.gens[tenant]
	return id, nil
}

// Add copies a fragment and returns its immutable block ID. Add rejects
// fragments for revoked tenants, and exact identity is preserved unchanged.
func (s *Store) Add(f Fragment) (BlockID, error) {
	if s == nil {
		return 0, ErrEvidenceInvalid
	}
	if err := f.Identity.validate(); err != nil {
		return 0, err
	}
	n := uint64(len(f.Data))
	if n > math.MaxUint64-f.Offset {
		return 0, ErrOffsetOverflow
	}

	// Copy before taking the lock. A concurrent revocation is checked again
	// below, so either the block is accepted before revocation or rejected.
	copied := cloneBytes(f.Data)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.revokedIdentity(f.Identity) {
		return 0, ErrRevoked
	}
	s.nextID++
	rec := &blockRecord{
		id:       s.nextID,
		identity: f.Identity,
		offset:   f.Offset,
		data:     copied,
	}
	s.blocks[rec.id] = rec
	return rec.id, nil
}

// Append is an alias for Add.
func (s *Store) Append(f Fragment) (BlockID, error) {
	return s.Add(f)
}

// AddBlock is an alias for Add.
func (s *Store) AddBlock(f Fragment) (BlockID, error) {
	return s.Add(f)
}

// Block is a copied snapshot of an immutable block.
type Block struct {
	ID       BlockID
	Identity Identity
	Offset   uint64
	Data     []byte
}

// End returns the exclusive end of the block.
func (b Block) End() uint64 {
	return b.Offset + uint64(len(b.Data))
}

// GetBlock returns a copy of an accepted block.
func (s *Store) GetBlock(id BlockID) (Block, bool) {
	if s == nil {
		return Block{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.blocks[id]
	if !ok {
		return Block{}, false
	}
	return Block{
		ID:       rec.id,
		Identity: rec.identity,
		Offset:   rec.offset,
		Data:     cloneBytes(rec.data),
	}, true
}

// BlockCount returns the number of currently retained blocks.
func (s *Store) BlockCount() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.blocks)
}

// Revoke invalidates every generation for a tenant and all pending evidence
// derived from it. It returns the number of blocks discarded.
func (s *Store) Revoke(tenant string) int {
	if s == nil || tenant == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	return s.revokeTenantLocked(tenant)
}

// RevokeTenant is an alias for Revoke.
func (s *Store) RevokeTenant(tenant string) int {
	return s.Revoke(tenant)
}

// RevokeIdentity invalidates exactly one (tenant, generation) connection.
func (s *Store) RevokeIdentity(id Identity) int {
	if s == nil || !id.Valid() {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	s.revEpoch++
	key := keyOf(id)
	s.revoked[key] = s.revEpoch
	removed := 0
	for blockID, rec := range s.blocks {
		if rec.identity.Equal(id) {
			delete(s.blocks, blockID)
			removed++
		}
	}
	return removed
}

func (s *Store) revokeTenantLocked(tenant string) int {
	s.revEpoch++
	s.tenants[tenant] = s.revEpoch
	removed := 0
	for blockID, rec := range s.blocks {
		if rec.identity.Tenant == tenant {
			delete(s.blocks, blockID)
			removed++
		}
	}
	for key := range s.revoked {
		if key.tenant == tenant {
			delete(s.revoked, key)
		}
	}
	// The tenant-level tombstone above subsumes all generation tombstones.
	delete(s.gens, tenant)
	return removed
}

func (s *Store) revokedIdentity(id Identity) bool {
	if _, ok := s.tenants[id.Tenant]; ok {
		return true
	}
	_, ok := s.revoked[keyOf(id)]
	return ok
}

func (s *Store) allows(id Identity) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.revokedIdentity(id)
}

// Revoked reports whether the exact identity or its tenant is revoked.
func (s *Store) Revoked(id Identity) bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revokedIdentity(id)
}

// TenantRevoked reports whether the tenant has been revoked.
func (s *Store) TenantRevoked(tenant string) bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.tenants[tenant]
	return ok
}

// RevocationEpoch returns a monotonically increasing value changed by any
// revocation. It is useful for callers that cache evidence status.
func (s *Store) RevocationEpoch() uint64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revEpoch
}

// snapshotIdentity returns pointers to immutable records matching id and
// overlapping [start,end). The caller must hold at least the read lock.
func (s *Store) snapshotIdentity(id Identity, start, end uint64) []*blockRecord {
	records := make([]*blockRecord, 0)
	for _, rec := range s.blocks {
		if !rec.identity.Equal(id) || len(rec.data) == 0 {
			continue
		}
		if rec.offset >= end || rec.end() <= start {
			continue
		}
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].offset != records[j].offset {
			return records[i].offset < records[j].offset
		}
		return records[i].id < records[j].id
	})
	return records
}

type blockSnapshot struct {
	identity Identity
	offset   uint64
	data     []byte
}
