package policymv

// snapshot is one immutable, fully materialized policy version.
// It is never mutated in place: apply returns a new snapshot, so readers that
// already hold a pointer keep observing a consistent version forever.
type snapshot struct {
	edges     map[Edge]struct{}
	parents   map[Role]map[Role]struct{} // role -> its direct parents
	children  map[Role]map[Role]struct{} // role -> its direct children
	grants    map[Role]map[Permission]struct{}
	effective map[Role]map[Permission]struct{}
	roles     map[Role]struct{}
}

func newSnapshot() *snapshot {
	return &snapshot{
		edges:     make(map[Edge]struct{}),
		parents:   make(map[Role]map[Role]struct{}),
		children:  make(map[Role]map[Role]struct{}),
		grants:    make(map[Role]map[Permission]struct{}),
		effective: make(map[Role]map[Permission]struct{}),
		roles:     make(map[Role]struct{}),
	}
}

func (s *snapshot) ensureRole(r Role) {
	if _, ok := s.roles[r]; ok {
		return
	}
	s.roles[r] = struct{}{}
	s.parents[r] = make(map[Role]struct{})
	s.children[r] = make(map[Role]struct{})
	s.grants[r] = make(map[Permission]struct{})
	s.effective[r] = make(map[Permission]struct{})
}

// apply validates the change set against this snapshot and returns a new
// snapshot with every mutation applied. The receiver is left untouched.
func (s *snapshot) apply(change ChangeSet) (*snapshot, error) {
	next := s.clone()

	pendingEdges := make(map[Edge]struct{}, len(change.AddEdges))
	for _, e := range change.AddEdges {
		if _, ok := next.edges[e]; ok {
			return nil, ErrDuplicateEdge
		}
		if _, ok := pendingEdges[e]; ok {
			return nil, ErrDuplicateEdge
		}
		pendingEdges[e] = struct{}{}
	}
	removedEdges := make(map[Edge]struct{}, len(change.RemoveEdges))
	for _, e := range change.RemoveEdges {
		if _, ok := s.edges[e]; !ok {
			return nil, ErrMissingEdge
		}
		if _, ok := removedEdges[e]; ok {
			return nil, ErrMissingEdge
		}
		removedEdges[e] = struct{}{}
	}

	pendingGrants := make(map[Role]map[Permission]struct{}, len(change.AddGrants))
	for _, rg := range change.AddGrants {
		bucket, ok := pendingGrants[rg.Role]
		if !ok {
			bucket = make(map[Permission]struct{})
			pendingGrants[rg.Role] = bucket
		}
		for _, p := range rg.Perms {
			if _, ok := s.grants[rg.Role][p]; ok {
				return nil, ErrDuplicateGrant
			}
			if _, ok := bucket[p]; ok {
				return nil, ErrDuplicateGrant
			}
			bucket[p] = struct{}{}
		}
	}
	revokedGrants := make(map[Role]map[Permission]struct{}, len(change.RemoveGrants))
	for _, rg := range change.RemoveGrants {
		bucket, ok := revokedGrants[rg.Role]
		if !ok {
			bucket = make(map[Permission]struct{})
			revokedGrants[rg.Role] = bucket
		}
		for _, p := range rg.Perms {
			if _, ok := s.grants[rg.Role][p]; !ok {
				return nil, ErrMissingGrant
			}
			if _, ok := bucket[p]; ok {
				return nil, ErrMissingGrant
			}
			bucket[p] = struct{}{}
		}
	}

	for e := range removedEdges {
		next.removeEdge(e)
	}
	for e := range pendingEdges {
		next.addEdge(e)
	}
	for role, perms := range pendingGrants {
		next.ensureRole(role)
		for p := range perms {
			next.grants[role][p] = struct{}{}
		}
	}
	for role, perms := range revokedGrants {
		for p := range perms {
			delete(next.grants[role], p)
		}
	}

	if next.hasCycle() {
		return nil, ErrCycle
	}
	next.recomputeEffective()
	return next, nil
}

func (s *snapshot) addEdge(e Edge) {
	s.ensureRole(e.Child)
	s.ensureRole(e.Parent)
	s.edges[e] = struct{}{}
	s.parents[e.Child][e.Parent] = struct{}{}
	s.children[e.Parent][e.Child] = struct{}{}
}

func (s *snapshot) removeEdge(e Edge) {
	delete(s.edges, e)
	delete(s.parents[e.Child], e.Parent)
	delete(s.children[e.Parent], e.Child)
}

// hasCycle reports whether the inheritance graph contains a directed cycle.
// It performs a three-color DFS following Child -> Parent edges.
func (s *snapshot) hasCycle() bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[Role]int, len(s.roles))
	for r := range s.roles {
		color[r] = white
	}
	var visit func(Role) bool
	visit = func(r Role) bool {
		color[r] = gray
		for parent := range s.parents[r] {
			switch color[parent] {
			case gray:
				return true
			case white:
				if visit(parent) {
					return true
				}
			}
		}
		color[r] = black
		return false
	}
	for r := range s.roles {
		if color[r] == white && visit(r) {
			return true
		}
	}
	return false
}

// recomputeEffective materializes the transitive permission set of every role
// so reader checks are O(1) and allocation free on the hot path.
func (s *snapshot) recomputeEffective() {
	s.effective = make(map[Role]map[Permission]struct{}, len(s.roles))
	for r := range s.roles {
		visited := make(map[Role]struct{})
		perms := make(map[Permission]struct{})
		var walk func(Role)
		walk = func(role Role) {
			if _, ok := visited[role]; ok {
				return
			}
			visited[role] = struct{}{}
			for p := range s.grants[role] {
				perms[p] = struct{}{}
			}
			for parent := range s.parents[role] {
				walk(parent)
			}
		}
		walk(r)
		s.effective[r] = perms
	}
}

func (s *snapshot) hasPermission(role Role, p Permission) bool {
	perms, ok := s.effective[role]
	if !ok {
		return false
	}
	_, ok = perms[p]
	return ok
}

// closure returns the dependency closure of a change set against this snapshot:
// every role the change touches plus every role that inherits (directly or
// transitively) from a touched role. These are exactly the roles whose
// effective permissions the publication can affect.
func (s *snapshot) closure(change ChangeSet) map[Role]struct{} {
	closed := make(map[Role]struct{})
	var stack []Role
	for _, r := range change.TouchedRoles() {
		if _, ok := closed[r]; !ok {
			closed[r] = struct{}{}
			stack = append(stack, r)
		}
	}
	for len(stack) > 0 {
		r := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for child := range s.children[r] {
			if _, ok := closed[child]; !ok {
				closed[child] = struct{}{}
				stack = append(stack, child)
			}
		}
	}
	return closed
}

func (s *snapshot) clone() *snapshot {
	next := newSnapshot()
	for r := range s.roles {
		next.ensureRole(r)
		for parent := range s.parents[r] {
			next.parents[r][parent] = struct{}{}
		}
		for child := range s.children[r] {
			next.children[r][child] = struct{}{}
		}
		for p := range s.grants[r] {
			next.grants[r][p] = struct{}{}
		}
	}
	for e := range s.edges {
		next.edges[e] = struct{}{}
	}
	return next
}
