package pseq

// Store owns named snapshots (versions) of sequences from one Factory.
// Releasing a snapshot drops its root reference; nodes no longer reachable
// from any committed version become garbage and are reclaimed by the Go
// runtime. LiveNodes reports the exact set of still-reachable nodes, which
// is the observable evidence that reclamation is accurate.
type Store struct {
	f        *Factory
	versions map[string]*Seq
}

// NewStore returns an empty snapshot store for this factory.
func (f *Factory) NewStore() *Store {
	return &Store{f: f, versions: make(map[string]*Seq)}
}

// Commit records s under name, replacing any previous snapshot with the
// same name. The previous version's uniquely-held nodes become collectable.
func (st *Store) Commit(name string, s *Seq) {
	if s.f != st.f {
		panic("pseq: Commit of a sequence from a different factory")
	}
	st.versions[name] = s
}

// Get returns the snapshot stored under name.
func (st *Store) Get(name string) (*Seq, bool) {
	s, ok := st.versions[name]
	return s, ok
}

// Release drops the snapshot stored under name. Nodes reachable only from
// that snapshot become unreachable and are reclaimed by the runtime.
func (st *Store) Release(name string) bool {
	if _, ok := st.versions[name]; !ok {
		return false
	}
	delete(st.versions, name)
	return true
}

// LiveNodes counts the distinct nodes reachable from all committed
// snapshots. Nodes shared between versions are counted once.
func (st *Store) LiveNodes() int {
	return len(st.reachable())
}

// reachable returns the set of nodes reachable from committed snapshots.
func (st *Store) reachable() map[*node]struct{} {
	seen := make(map[*node]struct{})
	var walk func(n *node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		walk(n.left)
		walk(n.right)
	}
	for _, s := range st.versions {
		walk(s.root)
	}
	return seen
}
