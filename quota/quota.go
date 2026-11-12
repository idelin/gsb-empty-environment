// Package quota implements reference-counted, per-tenant byte accounting for
// an isolated storage host.
//
// Billing is driven by object references rather than visible directory
// entries. An object stays charged against its owning tenant while it is kept
// alive by any directory entry (a "link") or by any open handle. Only when
// the last reference of either kind goes away are the physical bytes returned
// to the tenant's quota. This closes the hole where a deleted-but-still-open
// sensitive file would otherwise stop being counted.
//
// Hard links are confined to the tenant that owns the object: linking into
// another tenant's namespace is rejected without touching any quota.
//
// Rename-overwrite and write growth are executed as transactions: every
// state change registers an undo action, and an injected failure (or any
// later step failing) rewinds the whole operation so that a failed attempt
// never issues extra quota and never leaves partial state behind.
package quota

import (
	"errors"
	"sync"
)

// ObjectID uniquely identifies a storage object inside a Manager.
type ObjectID uint64

// TenantID identifies a quota domain. Every object is owned by exactly one
// tenant for its whole lifetime.
type TenantID string

// Op names identify the storage operations that can be fault-injected.
const (
	OpCreate = "create"
	OpLink   = "link"
	OpUnlink = "unlink"
	OpGrow   = "grow"
	OpRename = "rename"
)

// Phase names identify the transaction phases at which a fault may fire.
const (
	PhaseReserve = "reserve"
	PhaseCommit  = "commit"
	PhaseLinkadd = "linkadd"
	PhaseDetach  = "detach"
	PhaseCharge  = "charge"
	PhaseVictim  = "victim"
)

var (
	// ErrInjected wraps every error produced by the configured Fault hook.
	ErrInjected = errors.New("quota: injected fault")
	// ErrUnknownTenant is returned when a tenant has not been registered.
	ErrUnknownTenant = errors.New("quota: unknown tenant")
	// ErrQuotaExceeded is returned when a reservation does not fit.
	ErrQuotaExceeded = errors.New("quota: tenant quota exceeded")
	// ErrInvalidSize is returned for zero or absurdly large sizes.
	ErrInvalidSize = errors.New("quota: invalid size")
	// ErrCrossTenant is returned when a link would cross tenant boundaries.
	ErrCrossTenant = errors.New("quota: link cannot cross tenants")
	// ErrNotFound is returned when no object is bound at the given name.
	ErrNotFound = errors.New("quota: name not found")
	// ErrNameExists is returned when a link target name is already bound.
	ErrNameExists = errors.New("quota: name already exists")
	// ErrClosed is returned when a handle is closed more than once.
	ErrClosed = errors.New("quota: handle already closed")
)

// Fault is a hook used to inject storage failures during tests. Returning a
// non-nil error aborts the current operation at the given phase. The hook
// runs while holding the Manager lock, so it must not call back into the
// Manager.
type Fault func(op string, phase string, tenant TenantID, name string) error

// object is the internal billing record for one physical object.
type object struct {
	id      ObjectID
	owner   TenantID
	size    int64
	links   int
	handles int
}

// alive reports whether any reference keeps the object charged.
func (o *object) alive() bool {
	return o.links > 0 || o.handles > 0
}

type tenant struct {
	limit int64
	used  int64
	// names maps a directory entry to the object it currently names.
	names map[string]*object
}

// Manager is the quota authority a storage host calls into. A Manager is safe
// for concurrent use.
type Manager struct {
	mu      sync.Mutex
	tenants map[TenantID]*tenant
	objects map[ObjectID]*object
	nextID  ObjectID
	fault   Fault
}

// NewManager creates an empty Manager.
func NewManager() *Manager {
	return &Manager{
		tenants: make(map[TenantID]*tenant),
		objects: make(map[ObjectID]*object),
		nextID:  1,
	}
}

// SetFault installs (or clears, with nil) the fault-injection hook.
func (m *Manager) SetFault(f Fault) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fault = f
}

// AddTenant registers a tenant with the given nonnegative byte limit.
func (m *Manager) AddTenant(id TenantID, limit int64) error {
	if limit < 0 {
		return ErrInvalidSize
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tenants[id] = &tenant{limit: limit, names: make(map[string]*object)}
	return nil
}

// Usage reports the charged bytes and configured limit of a tenant.
func (m *Manager) Usage(id TenantID) (used int64, limit int64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tenants[id]
	if !ok {
		return 0, 0, ErrUnknownTenant
	}
	return t.used, t.limit, nil
}

// undo rewinds one committed transaction step.
type undo func()

// txn accumulates rollback actions in LIFO order.
type txn struct {
	steps []undo
}

func (t *txn) add(u undo) {
	t.steps = append(t.steps, u)
}

func (t *txn) rollback() {
	for i := len(t.steps) - 1; i >= 0; i-- {
		t.steps[i]()
	}
	t.steps = nil
}

func (m *Manager) fail(op, phase string, tenant TenantID, name string) error {
	if m.fault == nil {
		return nil
	}
	if err := m.fault(op, phase, tenant, name); err != nil {
		return errors.Join(ErrInjected, err)
	}
	return nil
}

func validSize(size int64) bool {
	return size > 0
}

// charge reserves delta bytes for tenant t, rejecting over-quota and
// arithmetic-overflow reservations.
func charge(t *tenant, delta int64) error {
	if delta <= 0 {
		return ErrInvalidSize
	}
	if t.used > t.limit-delta {
		return ErrQuotaExceeded
	}
	t.used += delta
	return nil
}

// Handle is one open reference to an object. Closing the last handle returns
// the object's bytes once no directory entry names it anymore.
type Handle struct {
	mgr  *Manager
	obj  *object
	once bool
	mu   sync.Mutex
}

// ObjectID returns the handle's object.
func (h *Handle) ObjectID() ObjectID { return h.obj.id }

// Size returns the object's current charged size.
func (h *Handle) Size() int64 {
	h.mgr.mu.Lock()
	defer h.mgr.mu.Unlock()
	return h.obj.size
}

// Close releases the open reference. It is safe to call concurrently from
// multiple goroutines sharing a Handle: exactly one of them performs the
// release, the rest get ErrClosed. Reclaiming the physical bytes happens at
// most once.
func (h *Handle) Close() error {
	h.mu.Lock()
	if h.once {
		h.mu.Unlock()
		return ErrClosed
	}
	h.once = true
	h.mu.Unlock()

	h.mgr.mu.Lock()
	defer h.mgr.mu.Unlock()
	if h.obj.handles <= 0 {
		panic("quota: internal invariant violated: negative handle count")
	}
	h.obj.handles--
	if !h.obj.alive() {
		t := h.mgr.tenants[h.obj.owner]
		if t == nil {
			panic("quota: internal invariant violated: owner tenant missing")
		}
		if t.used < h.obj.size {
			panic("quota: internal invariant violated: usage underflow")
		}
		t.used -= h.obj.size
		delete(h.mgr.objects, h.obj.id)
	}
	return nil
}

// maybeFree reclaims an object whose last reference just disappeared and
// returns its bytes to the owning tenant. It is a no-op while any link or
// open handle remains.
func (m *Manager) maybeFree(obj *object) {
	if obj.alive() {
		return
	}
	t := m.tenants[obj.owner]
	if t == nil {
		panic("quota: internal invariant violated: owner tenant missing")
	}
	if t.used < obj.size {
		panic("quota: internal invariant violated: usage underflow")
	}
	t.used -= obj.size
	delete(m.objects, obj.id)
}

// Create makes a new object owned by tenant and binds it to name. The
// object's size bytes are charged immediately. A failure at any phase leaves
// both the namespace and the quota untouched.
func (m *Manager) Create(tenant TenantID, name string, size int64) (ObjectID, error) {
	if !validSize(size) {
		return 0, ErrInvalidSize
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tenants[tenant]
	if !ok {
		return 0, ErrUnknownTenant
	}
	if _, exists := t.names[name]; exists {
		return 0, ErrNameExists
	}

	if err := m.fail(OpCreate, PhaseReserve, tenant, name); err != nil {
		return 0, err
	}

	var tr txn
	if err := charge(t, size); err != nil {
		return 0, err
	}
	tr.add(func() { t.used -= size })

	if err := m.fail(OpCreate, PhaseCommit, tenant, name); err != nil {
		tr.rollback()
		return 0, err
	}

	id := m.nextID
	m.nextID++
	obj := &object{id: id, owner: tenant, size: size, links: 1}
	m.objects[id] = obj
	tr.add(func() { delete(m.objects, id) })

	t.names[name] = obj
	return id, nil
}

// Link adds another directory entry dstName in tenant dstTenant naming src.
// Links never cross tenants: the object's owner must equal dstTenant. Linking
// issues no additional quota charge because the same physical bytes are
// already billed once.
func (m *Manager) Link(src ObjectID, dstTenant TenantID, dstName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	obj, ok := m.objects[src]
	if !ok {
		return ErrNotFound
	}
	if obj.owner != dstTenant {
		return ErrCrossTenant
	}
	t, ok := m.tenants[dstTenant]
	if !ok {
		return ErrUnknownTenant
	}
	if _, exists := t.names[dstName]; exists {
		return ErrNameExists
	}

	if err := m.fail(OpLink, PhaseLinkadd, dstTenant, dstName); err != nil {
		return err
	}

	obj.links++
	t.names[dstName] = obj
	return nil
}

// Unlink removes the directory entry name from tenant. The object's bytes
// stay charged while open handles remain; they are returned only after the
// last handle closes (or immediately, if nothing has the object open).
func (m *Manager) Unlink(tenant TenantID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tenants[tenant]
	if !ok {
		return ErrUnknownTenant
	}
	obj, ok := t.names[name]
	if !ok {
		return ErrNotFound
	}

	if err := m.fail(OpUnlink, PhaseDetach, tenant, name); err != nil {
		return err
	}

	var tr txn
	delete(t.names, name)
	tr.add(func() { t.names[name] = obj })

	if obj.links <= 0 {
		panic("quota: internal invariant violated: link count underflow")
	}
	obj.links--
	tr.add(func() { obj.links++ })

	if err := m.fail(OpUnlink, PhaseCommit, tenant, name); err != nil {
		tr.rollback()
		return err
	}

	m.maybeFree(obj)
	return nil
}

// Open takes an open reference on the object currently named name in tenant.
// The reference keeps the bytes charged after every directory entry is gone.
func (m *Manager) Open(tenant TenantID, name string) (*Handle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tenants[tenant]
	if !ok {
		return nil, ErrUnknownTenant
	}
	obj, ok := t.names[name]
	if !ok {
		return nil, ErrNotFound
	}
	obj.handles++
	return &Handle{mgr: m, obj: obj}, nil
}

// growLocked charges delta additional bytes for obj. It is shared by the
// public Grow path and by SetSize's growing branch.
func (m *Manager) growLocked(t *tenant, obj *object, delta int64, op string, tenant TenantID, name string) error {
	if delta <= 0 {
		return ErrInvalidSize
	}
	if err := m.fail(op, PhaseReserve, tenant, name); err != nil {
		return err
	}

	var tr txn
	if err := charge(t, delta); err != nil {
		return err
	}
	tr.add(func() { t.used -= delta })

	if err := m.fail(op, PhaseCharge, tenant, name); err != nil {
		tr.rollback()
		return err
	}

	old := obj.size
	obj.size += delta
	tr.add(func() { obj.size = old })
	return nil
}

// Grow extends an open object by delta bytes, charging the extension as a
// transaction. An injected reservation or charge failure changes neither the
// object size nor the tenant usage.
func (m *Manager) Grow(h *Handle, delta int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tenants[h.obj.owner]
	if !ok {
		return ErrUnknownTenant
	}
	return m.growLocked(t, h.obj, delta, OpGrow, h.obj.owner, "")
}

// SetSize resizes an open object. Growth goes through the same transactional
// reservation as Grow; shrinkage returns the freed bytes immediately.
func (m *Manager) SetSize(h *Handle, size int64) error {
	if !validSize(size) {
		return ErrInvalidSize
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tenants[h.obj.owner]
	if !ok {
		return ErrUnknownTenant
	}
	current := h.obj.size
	switch {
	case size > current:
		return m.growLocked(t, h.obj, size-current, OpGrow, h.obj.owner, "")
	case size < current:
		if t.used < current-size {
			panic("quota: internal invariant violated: usage underflow")
		}
		t.used -= current - size
		h.obj.size = size
	}
	return nil
}

// Rename moves directory entry srcName to dstName within one tenant,
// overwriting any object currently named dstName. The whole operation is a
// transaction: an injected failure rewinds every quota and namespace change,
// so a failed overwrite never frees the victim's bytes nor charges the source
// twice. A victim kept alive by open handles stays charged until those
// handles close, exactly as after Unlink.
func (m *Manager) Rename(tenant TenantID, srcName, dstName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tenants[tenant]
	if !ok {
		return ErrUnknownTenant
	}
	src, ok := t.names[srcName]
	if !ok {
		return ErrNotFound
	}
	if srcName == dstName {
		return nil
	}
	victim, overwrite := t.names[dstName]

	if err := m.fail(OpRename, PhaseReserve, tenant, dstName); err != nil {
		return err
	}

	var tr txn

	// Detach the source entry.
	delete(t.names, srcName)
	tr.add(func() { t.names[srcName] = src })

	src.links--
	tr.add(func() { src.links++ })

	if overwrite {
		if err := m.fail(OpRename, PhaseVictim, tenant, dstName); err != nil {
			tr.rollback()
			return err
		}

		// Detach the overwritten victim entry.
		delete(t.names, dstName)
		tr.add(func() { t.names[dstName] = victim })

		if victim.links <= 0 {
			panic("quota: internal invariant violated: link count underflow")
		}
		victim.links--
		tr.add(func() { victim.links++ })
	}

	if err := m.fail(OpRename, PhaseLinkadd, tenant, dstName); err != nil {
		tr.rollback()
		return err
	}

	// Attach the source at the destination.
	t.names[dstName] = src
	tr.add(func() { delete(t.names, dstName) })

	src.links++
	tr.add(func() { src.links-- })

	if err := m.fail(OpRename, PhaseCommit, tenant, dstName); err != nil {
		tr.rollback()
		return err
	}

	// Quota is unchanged by the source move (same object, same owner). The
	// victim's bytes are returned only if no open handle keeps it alive.
	if overwrite {
		m.maybeFree(victim)
	}
	return nil
}
