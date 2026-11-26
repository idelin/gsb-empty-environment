package plancache

// LiveActive reports current principal liveness.
func (c *MemCatalog) LiveActive(p PrincipalID) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.active[p]
}

// Begin pins the current catalog and permissions into an immutable snapshot.
func (c *MemCatalog) Begin(p PrincipalID) (*Snapshot, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.active[p] {
		return nil, ErrPrincipalInactive
	}
	snap := &Snapshot{
		Epoch:       c.epoch,
		Principal:   p,
		RoleClosure: c.roles[p],
		Active:      true,
		Views:       make(map[ViewID]viewState, len(c.views)),
		Tables:      make(map[ObjectID]tableState, len(c.tables)),
		Privileges:  make(map[ObjectID]map[PrincipalID]map[Operation]bool, len(c.privs)),
	}
	for id, v := range c.views {
		snap.Views[id] = v
	}
	for id, t := range c.tables {
		snap.Tables[id] = t
	}
	for oid, byPrincipal := range c.privs {
		snap.Privileges[oid] = make(map[PrincipalID]map[Operation]bool, len(byPrincipal))
		for pid, ops := range byPrincipal {
			cp := make(map[Operation]bool, len(ops))
			for op, ok := range ops {
				cp[op] = ok
			}
			snap.Privileges[oid][pid] = cp
		}
	}
	return snap, nil
}

// AuthorizeCompile validates every security dimension of a compile input
// against current live state.
func (c *MemCatalog) AuthorizeCompile(in CompileInput) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.active[in.Caller] {
		return ErrPrincipalInactive
	}
	if c.roles[in.Caller] != in.RoleClosure {
		return ErrUnauthorized
	}
	if in.PolicyEpoch != c.epoch {
		return ErrStalePlan
	}
	for _, node := range in.DepGraph {
		switch node.RefType {
		case "view":
			v, ok := c.views[node.View.ViewID]
			if !ok || v.defVersion != node.View.ViewDefVersion ||
				v.underlying != node.View.UnderlyingTable ||
				v.parent != node.View.ParentViewID {
				return ErrStalePlan
			}
		case "table":
			t, ok := c.tables[node.Table.ObjectID]
			if !ok {
				return ErrStalePlan
			}
			if t.schemaVersion != node.Table.SchemaVersion || t.rls != node.Table.RLSPolicy {
				return ErrStalePlan
			}
			for _, op := range in.RequiredOps {
				if !c.privs[node.Table.ObjectID][in.Caller][op] {
					return ErrUnauthorized
				}
			}
			if got := in.RLSPolicies[node.Table.ObjectID]; got != t.rls {
				return ErrStalePlan
			}
		default:
			return ErrUnauthorized
		}
	}
	return nil
}
