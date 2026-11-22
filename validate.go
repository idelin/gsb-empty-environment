package destplan

import "sort"

type edgeGroup struct {
	canonical string
	names     []string
	edges     []int
	cost      uint64
}

type model struct {
	snapshots map[string]*Snapshot
	keys      map[string]int
	rootKeys  map[string]bool
	dataKeys  map[string]bool
	groups    []edgeGroup
	edgeOf    map[string]int
	adj       map[string][]groupEdge
}

type groupEdge struct {
	group int
	to    string
}

func buildModel(p *Problem) (*model, error) {
	if p == nil {
		return nil, errf("nil problem")
	}
	m := &model{
		snapshots: map[string]*Snapshot{},
		keys:      map[string]int{},
		rootKeys:  map[string]bool{},
		dataKeys:  map[string]bool{},
		edgeOf:    map[string]int{},
		adj:       map[string][]groupEdge{},
	}
	if len(p.Snapshots) == 0 {
		return nil, errf("at least one snapshot is required")
	}
	onStack := map[*MapNode]bool{}
	validated := map[*MapNode]bool{}
	for i := range p.Snapshots {
		snap := &p.Snapshots[i]
		if snap.ID == "" {
			return nil, errf("snapshot %d has empty ID", i)
		}
		if _, dup := m.snapshots[snap.ID]; dup {
			return nil, errf("duplicate snapshot ID %q", snap.ID)
		}
		m.snapshots[snap.ID] = snap
		if snap.Root == nil {
			return nil, errf("snapshot %q has no root", snap.ID)
		}
		if err := validateNode(snap.Root, onStack, validated, 0); err != nil {
			return nil, err
		}
		if err := checkShift(snap.Root.Span, snap.Shift); err != nil {
			return nil, errf("snapshot %q: %v", snap.ID, err)
		}
	}
	for _, kn := range p.Graph.Keys {
		if kn.ID == "" {
			return nil, errf("key with empty ID")
		}
		if _, dup := m.keys[kn.ID]; dup {
			return nil, errf("duplicate key %q", kn.ID)
		}
		switch kn.Role {
		case KeyRoot:
			m.rootKeys[kn.ID] = true
		case KeyData:
			m.dataKeys[kn.ID] = true
		case KeyIntermediate:
		default:
			return nil, errf("key %q has unknown role", kn.ID)
		}
		m.keys[kn.ID] = int(kn.Role)
	}
	if len(m.rootKeys) == 0 {
		return nil, errf("key graph has no root key")
	}
	if err := m.buildGroups(p.Graph.Edges); err != nil {
		return nil, err
	}
	for _, req := range append(append([]Requirement{}, p.Destroy...), p.Retain...) {
		if _, ok := m.snapshots[req.Snapshot]; !ok {
			return nil, errf("requirement references unknown snapshot %q", req.Snapshot)
		}
		if req.Interval.Empty() {
			return nil, errf("empty requirement interval on snapshot %q", req.Snapshot)
		}
	}
	for _, req := range append(append([]Requirement{}, p.Destroy...), p.Retain...) {
		snap := m.snapshots[req.Snapshot]
		res, err := Resolve(*snap, req.Interval)
		if err != nil {
			return nil, err
		}
		for _, pc := range res.Pieces {
			if !m.dataKeys[pc.Key] {
				return nil, errf("leaf references unknown or non-data key %q", pc.Key)
			}
		}
	}
	return m, nil
}

func validateNode(n *MapNode, onStack, validated map[*MapNode]bool, shift int64) error {
	if n == nil {
		return errf("nil mapping node")
	}
	if validated[n] {
		return nil
	}
	if onStack[n] {
		return errf("cycle detected in mapping tree")
	}
	onStack[n] = true
	defer delete(onStack, n)
	if addrEqual(n.Span, Addr{}) {
		return errf("mapping node has zero span")
	}
	switch n.Kind {
	case NodeHole:
		validated[n] = true
		return nil
	case NodeLeaf:
		if n.DataKey == "" {
			return errf("leaf node has no data key")
		}
		base := n.CoordBase
		if base == 0 {
			base = n.BlockBase
		}
		if err := checkLeafRange(n.BlockBase, n.Span, "block"); err != nil {
			return err
		}
		if err := checkLeafRange(base, n.Span, "coordinate"); err != nil {
			return err
		}
		validated[n] = true
		return nil
	case NodeShift:
		if n.Inner == nil {
			return errf("shift node has no inner node")
		}
		if !addrEqual(n.Inner.Span, n.Span) {
			return errf("shift node span does not match inner span")
		}
		if err := checkShift(n.Span, n.Delta); err != nil {
			return err
		}
		if err := validateNode(n.Inner, onStack, validated, shift+n.Delta); err != nil {
			return err
		}
		validated[n] = true
		return nil
	case NodeBranch:
		if len(n.Children) == 0 {
			return errf("branch node has no children")
		}
		offs := make([]Child, len(n.Children))
		copy(offs, n.Children)
		sort.Slice(offs, func(i, j int) bool { return offs[i].Offset < offs[j].Offset })
		var prevEnd Addr
		for idx, ch := range offs {
			if ch.Node == nil {
				return errf("branch child has nil node")
			}
			start := Addr64(ch.Offset)
			if idx > 0 && addrCmp(start, prevEnd) < 0 {
				return errf("branch children overlap")
			}
			end, ok := addrAdd(start, ch.Node.Span)
			if !ok || addrCmp(end, n.Span) > 0 {
				return errf("branch child exceeds node span")
			}
			if err := validateNode(ch.Node, onStack, validated, shift); err != nil {
				return err
			}
			prevEnd = end
		}
		validated[n] = true
		return nil
	default:
		return errf("unknown mapping node kind %d", n.Kind)
	}
}

// checkLeafRange validates [base, base+span) where block identities are
// 64-bit: the last identity base+span-1 must fit in uint64, while the
// exclusive end may legitimately equal 2^64.
func checkLeafRange(base uint64, span Addr, label string) error {
	if span.Hi != 0 {
		if base != 0 {
			return errf("leaf %s range overflows the 64-bit space", label)
		}
		return nil
	}
	last, ok := addrAdd64(Addr64(base), span.Lo-1)
	if !ok || !last.InSpace() {
		return errf("leaf %s range overflows the 64-bit space", label)
	}
	return nil
}

func checkShift(span Addr, delta int64) error {
	hi, ok := addrShift(span, delta)
	if _, okLo := addrShift(Addr{}, delta); !okLo || !ok || addrCmp(hi, AddrEnd()) > 0 {
		return errf("shift %d moves span out of the 64-bit space", delta)
	}
	return nil
}

func (m *model) buildGroups(edges []WrapEdge) error {
	names := make([][]string, len(edges))
	owner := map[string]int{}
	for i, e := range edges {
		if e.ID == "" {
			return errf("edge %d has empty ID", i)
		}
		set := append([]string{e.ID}, e.Aliases...)
		sort.Strings(set)
		names[i] = set
		group := -1
		for _, nm := range set {
			if g, ok := owner[nm]; ok {
				if group == -1 {
					group = g
				} else if group != g {
					return errf("edge %q joins incompatible alias groups", e.ID)
				}
			}
		}
		if group == -1 {
			group = len(m.groups)
			m.groups = append(m.groups, edgeGroup{cost: e.Cost})
		} else if m.groups[group].cost != e.Cost {
			return errf("aliases of edge %q declare inconsistent costs", e.ID)
		}
		g := &m.groups[group]
		g.edges = append(g.edges, i)
		for _, nm := range set {
			if _, ok := owner[nm]; ok {
				continue
			}
			owner[nm] = group
			g.names = append(g.names, nm)
		}
	}
	if len(m.groups) > 20 {
		return errf("at most 20 independently deactivatable edges are allowed, got %d", len(m.groups))
	}
	for i := range m.groups {
		g := &m.groups[i]
		sort.Strings(g.names)
		g.canonical = g.names[0]
		for _, nm := range g.names {
			m.edgeOf[nm] = i
		}
	}
	for _, e := range edges {
		if _, ok := m.keys[e.From]; !ok {
			return errf("edge %q starts at unknown key %q", e.ID, e.From)
		}
		if _, ok := m.keys[e.To]; !ok {
			return errf("edge %q ends at unknown key %q", e.ID, e.To)
		}
		gi := owner[e.ID]
		m.adj[e.From] = append(m.adj[e.From], groupEdge{group: gi, to: e.To})
	}
	return nil
}
