// Package policymv implements a multi-versioned role policy store.
//
// A policy is a directed inheritance graph of roles together with per-role
// permissions. A publication (a ChangeSet of role edges and grants) is applied
// atomically: readers always observe one complete, immutable version and can
// never observe a publication that is only half applied.
//
// Two administrators may submit ChangeSets based on the same old version. A
// later submission rebases over the intervening publications and is merged
// automatically only when the dependency closures of every intervening change
// and the new change are pairwise disjoint (see ChangeSet.Closure).
package policymv

// Role identifies a principal role in the inheritance graph.
type Role string

// Permission identifies an authorization granted to a role.
type Permission string

// Edge is a directed inheritance edge Child -> Parent.
// A child role transitively inherits every permission of its parents.
type Edge struct {
	Child  Role
	Parent Role
}

// ChangeSet is one atomic publication: role-edge and grant mutations.
// All mutations become visible together in the new version.
type ChangeSet struct {
	AddEdges     []Edge
	RemoveEdges  []Edge
	AddGrants    []RoleGrants
	RemoveGrants []RoleGrants
}

// RoleGrants is a role together with a set of permissions.
type RoleGrants struct {
	Role  Role
	Perms []Permission
}

// TouchedRoles returns every role that the change set names directly.
func (c ChangeSet) TouchedRoles() []Role {
	seen := make(map[Role]struct{})
	add := func(r Role) {
		if r != "" {
			seen[r] = struct{}{}
		}
	}
	for _, e := range c.AddEdges {
		add(e.Child)
		add(e.Parent)
	}
	for _, e := range c.RemoveEdges {
		add(e.Child)
		add(e.Parent)
	}
	for _, g := range c.AddGrants {
		add(g.Role)
	}
	for _, g := range c.RemoveGrants {
		add(g.Role)
	}
	roles := make([]Role, 0, len(seen))
	for r := range seen {
		roles = append(roles, r)
	}
	return roles
}
