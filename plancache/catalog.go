package plancache

import "sync"

// Snapshot is the immutable permission/catalog view pinned when a
// transaction begins. Ordinary revocation never mutates a live snapshot;
// immediate invalidations are enforced separately through the bus.
type Snapshot struct {
	Epoch       int64
	Principal   PrincipalID
	RoleClosure Digest
	Active      bool
	Views       map[ViewID]viewState
	Tables      map[ObjectID]tableState
	Privileges  map[ObjectID]map[PrincipalID]map[Operation]bool
}

type viewState struct {
	id         ViewID
	name       string
	defVersion int64
	parent     ViewID
	underlying ObjectID
}

type tableState struct {
	id            ObjectID
	name          string
	schemaVersion int64
	rls           Digest
}

// Catalog is the host surface the plan cache needs for compilation and
// revalidation.
type Catalog interface {
	// Begin pins a permission snapshot for the principal.
	Begin(p PrincipalID) (*Snapshot, error)
	// AuthorizeCompile checks a compile input against current live state.
	AuthorizeCompile(in CompileInput) error
	// LiveActive reports whether the principal is currently active.
	LiveActive(p PrincipalID) bool
	// Bus returns the invalidation log.
	Bus() *InvalidationBus
}

// MemCatalog is a thread-safe in-memory Catalog suitable for embedding in a
// real host adapter or for tests.
type MemCatalog struct {
	mu          sync.RWMutex
	bus         *InvalidationBus
	epoch       int64
	views       map[ViewID]viewState
	tables      map[ObjectID]tableState
	viewByName  map[string]ViewID
	tableByName map[string]ObjectID
	roles       map[PrincipalID]Digest
	active      map[PrincipalID]bool
	privs       map[ObjectID]map[PrincipalID]map[Operation]bool
}

// NewMemCatalog creates a catalog backed by the given invalidation bus.
func NewMemCatalog(bus *InvalidationBus) *MemCatalog {
	return &MemCatalog{
		bus:         bus,
		views:       map[ViewID]viewState{},
		tables:      map[ObjectID]tableState{},
		viewByName:  map[string]ViewID{},
		tableByName: map[string]ObjectID{},
		roles:       map[PrincipalID]Digest{},
		active:      map[PrincipalID]bool{},
		privs:       map[ObjectID]map[PrincipalID]map[Operation]bool{},
	}
}

// Bus returns the invalidation log.
func (c *MemCatalog) Bus() *InvalidationBus { return c.bus }

// AddPrincipal registers an active principal with a role-closure digest.
func (c *MemCatalog) AddPrincipal(p PrincipalID, roleClosure Digest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.roles[p] = roleClosure
	c.active[p] = true
}

// SetActive enables or disables a principal.
func (c *MemCatalog) SetActive(p PrincipalID, active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active[p] = active
}

// SetRoleClosure changes the principal's transitive role digest.
func (c *MemCatalog) SetRoleClosure(p PrincipalID, roleClosure Digest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.roles[p] = roleClosure
}

// AddTable registers a base table.
func (c *MemCatalog) AddTable(id ObjectID, name string, schemaVersion int64, rls Digest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tables[id] = tableState{id: id, name: name, schemaVersion: schemaVersion, rls: rls}
	c.tableByName[name] = id
}

// AddView registers a view definition.
func (c *MemCatalog) AddView(id ViewID, name string, defVersion int64, parent ViewID, underlying ObjectID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.views[id] = viewState{id: id, name: name, defVersion: defVersion, parent: parent, underlying: underlying}
	c.viewByName[name] = id
}

// Grant gives a principal an operation on a base table.
func (c *MemCatalog) Grant(p PrincipalID, table ObjectID, op Operation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.privs[table] == nil {
		c.privs[table] = map[PrincipalID]map[Operation]bool{}
	}
	if c.privs[table][p] == nil {
		c.privs[table][p] = map[Operation]bool{}
	}
	c.privs[table][p][op] = true
}

// Revoke removes a privilege. When immediate is true the change pierces
// pinned transaction snapshots and waiting plans fail; otherwise it only
// affects transactions begun afterwards.
func (c *MemCatalog) Revoke(p PrincipalID, table ObjectID, op Operation, immediate bool) {
	c.mu.Lock()
	if c.privs[table] != nil && c.privs[table][p] != nil {
		delete(c.privs[table][p], op)
	}
	c.mu.Unlock()

	c.bus.Publish(InvalidationEvent{
		Type:      "revoke",
		Immediate: immediate,
		Principal: p,
		ObjectIDs: []ObjectID{table},
	})
}

// TightenRLS replaces a table's RLS policy digest and advances the global
// policy epoch. affectedViews lists nested views whose plans depend on the
// table, so immediate events can reach waiting plans through the view
// expansion chain.
func (c *MemCatalog) TightenRLS(table ObjectID, newPolicy Digest, affectedViews []ViewID, immediate bool) {
	c.mu.Lock()
	t := c.tables[table]
	t.rls = newPolicy
	c.tables[table] = t
	c.epoch++
	c.mu.Unlock()

	c.bus.Publish(InvalidationEvent{
		Type:      "rls",
		Immediate: immediate,
		ObjectIDs: []ObjectID{table},
		ViewIDs:   append([]ViewID(nil), affectedViews...),
	})
}

// BumpSchema advances a table schema version (for example after ALTER).
func (c *MemCatalog) BumpSchema(table ObjectID) {
	c.mu.Lock()
	t := c.tables[table]
	t.schemaVersion++
	c.tables[table] = t
	c.mu.Unlock()
	c.bus.Publish(InvalidationEvent{
		Type:      "ddl",
		Immediate: true,
		ObjectIDs: []ObjectID{table},
	})
}

// DropAndRecreateView drops a view and creates a same-name replacement with
// a brand new ViewID. The old identity is gone forever, so any plan that
// expanded the old view cannot match. The change is always immediate.
func (c *MemCatalog) DropAndRecreateView(name string, newID ViewID, underlying ObjectID) {
	c.mu.Lock()
	old := c.views[c.viewByName[name]]
	delete(c.views, old.id)
	// Preserve the expansion chain: the replacement keeps the same parent
	// link and starts at the next definition version rather than rewinding
	// to 1 (a version downgrade would itself be a revalidation failure).
	c.views[newID] = viewState{id: newID, name: name, defVersion: old.defVersion + 1,
		parent: old.parent, underlying: underlying}
	c.viewByName[name] = newID
	c.mu.Unlock()

	c.bus.Publish(InvalidationEvent{
		Type:      "ddl",
		Immediate: true,
		ViewIDs:   []ViewID{old.id},
	})
}

// DropAndRecreateTable replaces a same-name table with a new ObjectID.
func (c *MemCatalog) DropAndRecreateTable(name string, newID ObjectID, rls Digest) {
	c.mu.Lock()
	old := c.tables[c.tableByName[name]]
	delete(c.tables, old.id)
	delete(c.privs, old.id)
	c.tables[newID] = tableState{id: newID, name: name, schemaVersion: 1, rls: rls}
	c.tableByName[name] = newID
	c.mu.Unlock()

	c.bus.Publish(InvalidationEvent{
		Type:      "ddl",
		Immediate: true,
		ObjectIDs: []ObjectID{old.id},
	})
}
