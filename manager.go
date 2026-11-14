package netsec

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ManagerConfig configures a lifecycle manager.
type ManagerConfig struct {
	ServerInstance string
	KeyRing        *KeyRing
	Now            func() time.Time
}

type objectRecord struct {
	id         StableObjectID
	generation uint64
	objectType ObjectType
	policy     Policy
	exportID   ExportID
	data       []byte
	locations  map[Location]struct{}
}

type exportRecord struct {
	id          ExportID
	tenant      string
	fsid        uint64
	generation  uint64
	active      bool
	objects     map[StableObjectID]*objectRecord
	locations   map[Location]StableObjectID
	invalidated map[uint64]error
}

// Manager owns the generation graph that makes old handles stably stale.  All
// mutations and all authorization decisions are serialized through mu.
type Manager struct {
	mu                   sync.RWMutex
	codec                *Codec
	keys                 *KeyRing
	now                  func() time.Time
	exports              map[ExportID]*exportRecord
	owners               map[StableObjectID]ExportID
	lastObjectGeneration uint64
}

// NewManager creates a lifecycle manager.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	codec, err := NewCodec(CodecConfig{
		ServerInstance: cfg.ServerInstance,
		KeyRing:        cfg.KeyRing,
		Now:            cfg.Now,
	})
	if err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		codec:   codec,
		keys:    cfg.KeyRing,
		now:     now,
		exports: make(map[ExportID]*exportRecord),
		owners:  make(map[StableObjectID]ExportID),
	}, nil
}

// KeyRing returns the manager's key ring.  Use Manager.RotateKey for rotation
// so the retired key's object-generation cut-off is captured atomically with
// the manager's generation allocator.
func (m *Manager) KeyRing() *KeyRing {
	return m.keys
}

// ExportSpec is the input to CreateExport/ReplaceExport.
type ExportSpec struct {
	ID     ExportID
	Tenant string
	Fsid   uint64
}

// ObjectSpec is the input to CreateObject.
type ObjectSpec struct {
	ExportID ExportID
	StableID StableObjectID
	Fsid     uint64
	Inode    uint64
	Type     ObjectType
	Policy   Policy
	Data     []byte
}

// Snapshot is an opaque point-in-time copy used by RollbackSnapshot.
type Snapshot struct {
	owner            *Manager
	exportID         ExportID
	exportGeneration uint64
	tenant           string
	fsid             uint64
	objects          []snapshotObject
}

type snapshotObject struct {
	id         StableObjectID
	objectType ObjectType
	policy     Policy
	locations  []Location
	data       []byte
}

// CreateExport registers a new export.  If the UUID names a previously
// deleted export, its generation is advanced so handles from the old export
// can never become valid again.
func (m *Manager) CreateExport(spec ExportSpec) (ExportInfo, error) {
	if spec.Tenant == "" {
		return ExportInfo{}, deny(StatusInvalid, nil, "export tenant must not be empty")
	}
	if spec.ID.IsZero() {
		id, err := NewExportID()
		if err != nil {
			return ExportInfo{}, err
		}
		spec.ID = id
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.exports[spec.ID]; ok {
		if rec.active {
			return ExportInfo{}, ErrExportExists
		}
		oldGeneration := rec.generation
		m.clearExport(rec)
		rec.tenant = spec.Tenant
		rec.fsid = spec.Fsid
		rec.generation = oldGeneration + 1
		rec.active = true
		rec.invalidated[oldGeneration] = ErrStaleExport
		return exportInfo(rec), nil
	}
	rec := &exportRecord{
		id:          spec.ID,
		tenant:      spec.Tenant,
		fsid:        spec.Fsid,
		generation:  1,
		active:      true,
		objects:     make(map[StableObjectID]*objectRecord),
		locations:   make(map[Location]StableObjectID),
		invalidated: make(map[uint64]error),
	}
	m.exports[rec.id] = rec
	return exportInfo(rec), nil
}

// ReplaceExport atomically replaces an export's contents and generation.
// Every handle from the previous incarnation is stale.
func (m *Manager) ReplaceExport(id ExportID, spec ExportSpec) (ExportInfo, error) {
	if id.IsZero() {
		return ExportInfo{}, deny(StatusInvalid, nil, "export ID must not be zero")
	}
	if spec.Tenant == "" {
		return ExportInfo{}, deny(StatusInvalid, nil, "export tenant must not be empty")
	}
	if !spec.ID.IsZero() && spec.ID != id {
		return ExportInfo{}, deny(StatusInvalid, nil, "replacement export ID does not match")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.exports[id]
	if !ok {
		rec = &exportRecord{
			id:          id,
			objects:     make(map[StableObjectID]*objectRecord),
			locations:   make(map[Location]StableObjectID),
			invalidated: make(map[uint64]error),
		}
		m.exports[id] = rec
	} else {
		oldGeneration := rec.generation
		m.clearExport(rec)
		rec.generation = oldGeneration + 1
		rec.invalidated[oldGeneration] = ErrStaleExport
	}
	if rec.generation == 0 {
		rec.generation = 1
	}
	rec.tenant = spec.Tenant
	rec.fsid = spec.Fsid
	rec.active = true
	return exportInfo(rec), nil
}

// DeleteExport deactivates an export and tombstones its generation.  The
// record is retained so a later UUID reuse cannot restart at generation one.
func (m *Manager) DeleteExport(id ExportID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.exports[id]
	if !ok {
		return ErrExportNotFound
	}
	if !rec.active {
		m.clearExport(rec)
		return nil
	}
	oldGeneration := rec.generation
	m.clearExport(rec)
	rec.active = false
	rec.generation = oldGeneration + 1
	rec.invalidated[oldGeneration] = ErrStaleExport
	return nil
}

// ExportInfo returns a non-secret export description.
func (m *Manager) ExportInfo(id ExportID) (ExportInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.exports[id]
	if !ok {
		return ExportInfo{}, ErrExportNotFound
	}
	return exportInfo(rec), nil
}

func exportInfo(rec *exportRecord) ExportInfo {
	return ExportInfo{
		ID:         rec.id,
		Tenant:     rec.tenant,
		Fsid:       rec.fsid,
		Generation: rec.generation,
		Active:     rec.active,
	}
}

// CreateObject adds a backend object.  The stable ID is the authority; the
// (fsid, inode) pair is only a lookup alias and may be reused later.
func (m *Manager) CreateObject(spec ObjectSpec) (ObjectInfo, error) {
	if !spec.Type.valid() {
		return ObjectInfo{}, ErrUnsupportedObjectType
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.exports[spec.ExportID]
	if !ok {
		return ObjectInfo{}, ErrExportNotFound
	}
	if !rec.active {
		return ObjectInfo{}, ErrExportInactive
	}
	if spec.Policy.Tenant == "" {
		spec.Policy.Tenant = rec.tenant
	}
	if spec.Policy.Tenant != rec.tenant {
		return ObjectInfo{}, ErrTenantMismatch
	}
	if spec.StableID == "" {
		id, err := newStableObjectID()
		if err != nil {
			return ObjectInfo{}, err
		}
		spec.StableID = id
	}
	if owner, exists := m.owners[spec.StableID]; exists {
		if owner != rec.id {
			return ObjectInfo{}, deny(StatusInvalid, nil, "stable object ID %q belongs to another export", spec.StableID)
		}
		if _, stillPresent := rec.objects[spec.StableID]; stillPresent {
			return ObjectInfo{}, ErrConflict
		}
		delete(m.owners, spec.StableID)
	}
	if spec.Fsid == 0 {
		spec.Fsid = rec.fsid
	}
	loc := Location{Fsid: spec.Fsid, Inode: spec.Inode}
	if id, exists := rec.locations[loc]; exists && id != spec.StableID {
		return ObjectInfo{}, deny(StatusInvalid, nil, "location (%d,%d) already belongs to %q", loc.Fsid, loc.Inode, id)
	}
	if _, exists := rec.objects[spec.StableID]; exists {
		return ObjectInfo{}, ErrConflict
	}
	m.lastObjectGeneration++
	obj := &objectRecord{
		id:         spec.StableID,
		generation: m.lastObjectGeneration,
		objectType: spec.Type,
		policy:     spec.Policy,
		exportID:   rec.id,
		data:       append([]byte(nil), spec.Data...),
		locations:  map[Location]struct{}{loc: {}},
	}
	rec.objects[obj.id] = obj
	rec.locations[loc] = obj.id
	m.owners[obj.id] = rec.id
	return objectInfo(obj), nil
}

func newStableObjectID() (StableObjectID, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("netsec: generate stable object ID: %w", err)
	}
	return StableObjectID("obj-" + hex.EncodeToString(b[:])), nil
}

// DeleteObject removes an object and all of its links.  An object with the
// same stable ID cannot be recreated with the old generation; a subsequent
// creation receives a fresh generation.
func (m *Manager) DeleteObject(exportID ExportID, stableID StableObjectID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.exports[exportID]
	if !ok {
		return ErrExportNotFound
	}
	obj, ok := rec.objects[stableID]
	if !ok {
		return ErrObjectNotFound
	}
	for loc := range obj.locations {
		delete(rec.locations, loc)
	}
	delete(rec.objects, stableID)
	if m.owners[stableID] == exportID {
		delete(m.owners, stableID)
	}
	return nil
}

// DeleteObjectAt removes one NFS link.  If it was the last link, the object
// itself is tombstoned.
func (m *Manager) DeleteObjectAt(exportID ExportID, fsid, inode uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.exports[exportID]
	if !ok {
		return ErrExportNotFound
	}
	loc := Location{Fsid: fsid, Inode: inode}
	id, ok := rec.locations[loc]
	if !ok {
		return ErrObjectNotFound
	}
	obj := rec.objects[id]
	if obj == nil {
		delete(rec.locations, loc)
		return ErrObjectNotFound
	}
	delete(rec.locations, loc)
	delete(obj.locations, loc)
	if len(obj.locations) == 0 {
		delete(rec.objects, id)
		if m.owners[id] == exportID {
			delete(m.owners, id)
		}
	}
	return nil
}

// RemoveLink is an alias for DeleteObjectAt.
func (m *Manager) RemoveLink(exportID ExportID, fsid, inode uint64) error {
	return m.DeleteObjectAt(exportID, fsid, inode)
}

// AddHardLink adds another NFS location to the same backend object.  Hard
// links do not create a new object generation; handles issued through any link
// continue to name the same object.
func (m *Manager) AddHardLink(exportID ExportID, stableID StableObjectID, fsid, inode uint64) (ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.exports[exportID]
	if !ok {
		return ObjectInfo{}, ErrExportNotFound
	}
	if !rec.active {
		return ObjectInfo{}, ErrExportInactive
	}
	obj, ok := rec.objects[stableID]
	if !ok {
		return ObjectInfo{}, ErrObjectNotFound
	}
	if obj.objectType == ObjectTypeDirectory {
		return ObjectInfo{}, deny(StatusInvalid, nil, "directories cannot be hard-linked")
	}
	if fsid == 0 {
		fsid = rec.fsid
	}
	loc := Location{Fsid: fsid, Inode: inode}
	if id, exists := rec.locations[loc]; exists && id != stableID {
		return ObjectInfo{}, deny(StatusInvalid, nil, "location (%d,%d) already belongs to %q", loc.Fsid, loc.Inode, id)
	}
	rec.locations[loc] = stableID
	obj.locations[loc] = struct{}{}
	return objectInfo(obj), nil
}

// ChangeObjectPolicy replaces the tenant policy without changing object
// generation.  An old handle carries the old policy digest and is denied.
func (m *Manager) ChangeObjectPolicy(exportID ExportID, stableID StableObjectID, policy Policy) (ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.exports[exportID]
	if !ok {
		return ObjectInfo{}, ErrExportNotFound
	}
	if !rec.active {
		return ObjectInfo{}, ErrExportInactive
	}
	obj, ok := rec.objects[stableID]
	if !ok {
		return ObjectInfo{}, ErrObjectNotFound
	}
	if policy.Tenant == "" {
		policy.Tenant = rec.tenant
	}
	if policy.Tenant != rec.tenant {
		return ObjectInfo{}, ErrTenantMismatch
	}
	obj.policy = policy
	return objectInfo(obj), nil
}

// MoveObject moves an object and all of its links to another export/tenant.
// The stable ID stays the same, but an old handle names the old export and
// therefore fails instead of being rebound to the new tenant.
func (m *Manager) MoveObject(fromExport, toExport ExportID, stableID StableObjectID, policy Policy) (ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	from, ok := m.exports[fromExport]
	if !ok {
		return ObjectInfo{}, ErrExportNotFound
	}
	to, ok := m.exports[toExport]
	if !ok {
		return ObjectInfo{}, ErrExportNotFound
	}
	if !from.active || !to.active {
		return ObjectInfo{}, ErrExportInactive
	}
	obj, ok := from.objects[stableID]
	if !ok {
		return ObjectInfo{}, ErrObjectNotFound
	}
	if fromExport == toExport {
		return m.changePolicyLocked(from, obj, policy)
	}
	if policy.Tenant == "" {
		policy.Tenant = to.tenant
	}
	if policy.Tenant != to.tenant {
		return ObjectInfo{}, ErrTenantMismatch
	}
	if _, exists := to.objects[stableID]; exists {
		return ObjectInfo{}, ErrConflict
	}
	for loc := range obj.locations {
		if id, exists := to.locations[loc]; exists && id != stableID {
			return ObjectInfo{}, deny(StatusInvalid, nil, "target export location (%d,%d) already belongs to %q", loc.Fsid, loc.Inode, id)
		}
	}
	for loc := range obj.locations {
		delete(from.locations, loc)
	}
	delete(from.objects, stableID)
	// A move is a new tenant placement even if the object later moves back.
	// Advancing the generation prevents an old handle from being revived by a
	// return move that happens to restore the same policy digest.
	m.lastObjectGeneration++
	obj.generation = m.lastObjectGeneration
	obj.exportID = to.id
	obj.policy = policy
	to.objects[stableID] = obj
	for loc := range obj.locations {
		to.locations[loc] = stableID
	}
	m.owners[stableID] = to.id
	return objectInfo(obj), nil
}

func (m *Manager) changePolicyLocked(rec *exportRecord, obj *objectRecord, policy Policy) (ObjectInfo, error) {
	if policy.Tenant == "" {
		policy.Tenant = rec.tenant
	}
	if policy.Tenant != rec.tenant {
		return ObjectInfo{}, ErrTenantMismatch
	}
	obj.policy = policy
	return objectInfo(obj), nil
}

// Lookup resolves an NFS (fsid, inode) alias to the current backend object.
func (m *Manager) Lookup(exportID ExportID, fsid, inode uint64) (ObjectInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.exports[exportID]
	if !ok {
		return ObjectInfo{}, ErrExportNotFound
	}
	if !rec.active {
		return ObjectInfo{}, ErrExportInactive
	}
	id, ok := rec.locations[Location{Fsid: fsid, Inode: inode}]
	if !ok {
		return ObjectInfo{}, ErrObjectNotFound
	}
	obj, ok := rec.objects[id]
	if !ok {
		return ObjectInfo{}, ErrObjectNotFound
	}
	return objectInfo(obj), nil
}

// ResolveLocation is an alias for Lookup.
func (m *Manager) ResolveLocation(exportID ExportID, fsid, inode uint64) (ObjectInfo, error) {
	return m.Lookup(exportID, fsid, inode)
}

func objectInfo(obj *objectRecord) ObjectInfo {
	links := make([]Location, 0, len(obj.locations))
	for loc := range obj.locations {
		links = append(links, loc)
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].Fsid == links[j].Fsid {
			return links[i].Inode < links[j].Inode
		}
		return links[i].Fsid < links[j].Fsid
	})
	return ObjectInfo{
		ExportID:   obj.exportID,
		StableID:   obj.id,
		Generation: obj.generation,
		Type:       obj.objectType,
		Policy:     obj.policy,
		Links:      links,
	}
}

// IssueHandle seals the current object incarnation into a handle.
func (m *Manager) IssueHandle(exportID ExportID, stableID StableObjectID) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.exports[exportID]
	if !ok {
		return nil, ErrExportNotFound
	}
	if !rec.active {
		return nil, ErrExportInactive
	}
	obj, ok := rec.objects[stableID]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return m.issueHandleLocked(rec, obj)
}

func (m *Manager) issueHandleLocked(rec *exportRecord, obj *objectRecord) ([]byte, error) {
	return m.codec.Seal(Claims{
		ExportID:         rec.id,
		ExportGeneration: rec.generation,
		ObjectID:         obj.id,
		ObjectGeneration: obj.generation,
		ObjectType:       obj.objectType,
		PolicyDigest:     obj.policy.Digest(),
	})
}

// IssueHandleAt issues a handle by resolving an NFS location first.
func (m *Manager) IssueHandleAt(exportID ExportID, fsid, inode uint64) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.exports[exportID]
	if !ok {
		return nil, ErrExportNotFound
	}
	if !rec.active {
		return nil, ErrExportInactive
	}
	id, ok := rec.locations[Location{Fsid: fsid, Inode: inode}]
	if !ok {
		return nil, ErrObjectNotFound
	}
	obj, ok := rec.objects[id]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return m.issueHandleLocked(rec, obj)
}

// ParseHandle authenticates a handle and returns an opaque capability.
func (m *Manager) ParseHandle(handle []byte) (*Capability, error) {
	return m.codec.Open(handle)
}

// ParseHandleAt is ParseHandle with an explicit clock.
func (m *Manager) ParseHandleAt(handle []byte, now time.Time) (*Capability, error) {
	return m.codec.OpenAt(handle, now)
}

// DecodeHandle is an alias for ParseHandle.
func (m *Manager) DecodeHandle(handle []byte) (*Capability, error) {
	return m.ParseHandle(handle)
}

// Check performs the full authorization decision for one operation.
func (m *Manager) Check(cap *Capability, op Op) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.checkLocked(cap, op, m.now())
}

// Authorize is an alias for Check.
func (m *Manager) Authorize(cap *Capability, op Op) error {
	return m.Check(cap, op)
}

// LookupHandle performs the LOOKUP authorization for a handle and then
// resolves a child location within the same export.  The capability's export
// and generation are not trusted; Lookup re-reads the live export record.
func (m *Manager) LookupHandle(cap *Capability, fsid, inode uint64) (ObjectInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := m.checkLocked(cap, OpLookup, m.now()); err != nil {
		return ObjectInfo{}, err
	}
	rec := m.exports[cap.claims.ExportID]
	id, ok := rec.locations[Location{Fsid: fsid, Inode: inode}]
	if !ok {
		return ObjectInfo{}, ErrObjectNotFound
	}
	obj, ok := rec.objects[id]
	if !ok {
		return ObjectInfo{}, ErrObjectNotFound
	}
	return objectInfo(obj), nil
}

// ReadHandle checks READ policy and returns a generation-pinned snapshot of
// the object data.
func (m *Manager) ReadHandle(cap *Capability) ([]byte, error) {
	lease, err := m.Acquire(cap, OpRead)
	if err != nil {
		return nil, err
	}
	defer lease.Close()
	return lease.Read()
}

// WriteHandle checks WRITE policy and replaces object data only if the object
// incarnation is still the one authorized by the handle.
func (m *Manager) WriteHandle(cap *Capability, data []byte) error {
	lease, err := m.Acquire(cap, OpWrite)
	if err != nil {
		return err
	}
	defer lease.Close()
	return lease.Write(data)
}

func (m *Manager) checkLocked(cap *Capability, op Op, now time.Time) error {
	if cap == nil {
		return ErrBadHandle
	}
	if !op.valid() {
		return deny(StatusInvalid, nil, "invalid operation set %s", op)
	}
	if subtleCompare16(cap.serverTag, m.codec.serverTag) != 1 {
		return ErrWrongServer
	}
	if err := m.keys.authorize(cap.keyID, cap.claims.ObjectGeneration, now); err != nil {
		return err
	}
	claims := cap.claims
	rec, ok := m.exports[claims.ExportID]
	if !ok {
		return ErrExportNotFound
	}
	if !rec.active {
		// From a handle's point of view a deleted export is a stale export,
		// not a live-but-inactive administrative object.
		return ErrStaleExport
	}
	if claims.ExportGeneration != rec.generation {
		if reason, exists := rec.invalidated[claims.ExportGeneration]; exists {
			return reason
		}
		return ErrStaleExport
	}
	obj, ok := rec.objects[claims.ObjectID]
	if !ok {
		return ErrStaleObject
	}
	if obj.generation != claims.ObjectGeneration {
		return ErrStaleObject
	}
	if obj.objectType != claims.ObjectType {
		return ErrTypeMismatch
	}
	if obj.policy.Digest() != claims.PolicyDigest {
		return ErrStalePolicy
	}
	if rec.tenant != obj.policy.Tenant {
		return ErrTenantMismatch
	}
	if !obj.policy.Allows(op) {
		return ErrOperationDenied
	}
	return nil
}

// OpenHandle acquires a generation-snapshot lease for OPEN.
func (m *Manager) OpenHandle(cap *Capability) (*Lease, error) {
	return m.Acquire(cap, OpOpen)
}

// Acquire validates a capability and returns a lease pinned to the object
// generation observed at authorization time.  Every later callback must
// revalidate that snapshot.
func (m *Manager) Acquire(cap *Capability, op Op) (*Lease, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := m.checkLocked(cap, op, m.now()); err != nil {
		return nil, err
	}
	claims := cap.claims
	return &Lease{
		manager:          m,
		cap:              cap,
		exportID:         claims.ExportID,
		exportGeneration: claims.ExportGeneration,
		objectID:         claims.ObjectID,
		objectGeneration: claims.ObjectGeneration,
		objectType:       claims.ObjectType,
		policyDigest:     claims.PolicyDigest,
		op:               op,
	}, nil
}

// RotateKey retires the current key and installs a new active key.  The
// retired key's cut-off is the greatest object generation allocated so far.
func (m *Manager) RotateKey(newID string, secret []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.keys.Rotate(newID, secret, m.now(), m.lastObjectGeneration)
}

// TakeSnapshot captures the current objects and policies of an export.
func (m *Manager) TakeSnapshot(exportID ExportID) (*Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.exports[exportID]
	if !ok {
		return nil, ErrExportNotFound
	}
	if !rec.active {
		return nil, ErrExportInactive
	}
	snap := &Snapshot{
		owner:            m,
		exportID:         rec.id,
		exportGeneration: rec.generation,
		tenant:           rec.tenant,
		fsid:             rec.fsid,
		objects:          make([]snapshotObject, 0, len(rec.objects)),
	}
	for _, obj := range rec.objects {
		links := make([]Location, 0, len(obj.locations))
		for loc := range obj.locations {
			links = append(links, loc)
		}
		sort.Slice(links, func(i, j int) bool {
			if links[i].Fsid == links[j].Fsid {
				return links[i].Inode < links[j].Inode
			}
			return links[i].Fsid < links[j].Fsid
		})
		snap.objects = append(snap.objects, snapshotObject{
			id:         obj.id,
			objectType: obj.objectType,
			policy:     obj.policy,
			locations:  links,
			data:       append([]byte(nil), obj.data...),
		})
	}
	sort.Slice(snap.objects, func(i, j int) bool { return snap.objects[i].id < snap.objects[j].id })
	return snap, nil
}

// RollbackSnapshot restores snapshot contents while advancing every generation
// involved.  Thus a handle issued before the rollback is stale rather than
// silently rebinding to the restored object.
func (m *Manager) RollbackSnapshot(snap *Snapshot) error {
	if snap == nil {
		return deny(StatusInvalid, nil, "nil snapshot")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if snap.owner != m {
		return ErrSnapshotMismatch
	}
	rec, ok := m.exports[snap.exportID]
	if !ok {
		return ErrExportNotFound
	}
	if !rec.active {
		return ErrExportInactive
	}
	if rec.tenant != snap.tenant || rec.fsid != snap.fsid || rec.generation != snap.exportGeneration {
		return ErrSnapshotMismatch
	}
	for _, o := range snap.objects {
		if owner, exists := m.owners[o.id]; exists && owner != rec.id {
			return deny(StatusInvalid, nil, "snapshot object %q belongs to another export", o.id)
		}
	}
	oldGeneration := rec.generation
	m.clearExport(rec)
	for _, o := range snap.objects {
		m.lastObjectGeneration++
		obj := &objectRecord{
			id:         o.id,
			generation: m.lastObjectGeneration,
			objectType: o.objectType,
			policy:     o.policy,
			exportID:   rec.id,
			data:       append([]byte(nil), o.data...),
			locations:  make(map[Location]struct{}, len(o.locations)),
		}
		for _, loc := range o.locations {
			obj.locations[loc] = struct{}{}
			rec.locations[loc] = o.id
		}
		rec.objects[o.id] = obj
		m.owners[o.id] = rec.id
	}
	rec.generation = oldGeneration + 1
	rec.invalidated[oldGeneration] = ErrStaleSnapshot
	return nil
}

func (m *Manager) clearExport(rec *exportRecord) {
	for id, owner := range m.owners {
		if owner == rec.id {
			delete(m.owners, id)
		}
	}
	rec.objects = make(map[StableObjectID]*objectRecord)
	rec.locations = make(map[Location]StableObjectID)
}

func subtleCompare16(a, b [16]byte) int {
	// Avoid importing crypto/subtle in this file only for one comparison; the
	// constant-time property is not security-critical here, but keep it clear.
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	if diff == 0 {
		return 1
	}
	return 0
}
