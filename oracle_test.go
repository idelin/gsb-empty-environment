package destplan_test

import (
	"sort"
	"testing"

	dp "prescreen.local/ddd-9511-20261122-v2"
)

// sectorOracle is an independent reference implementation that materializes
// every logical address of a small volume into its backing block and data
// key, then enumerates edge subsets directly. It shares no planning code
// with the production solver, which makes the differential test meaningful.
type sectorOracle struct {
	sectors    map[string]map[uint64]sector
	rootKeys   map[string]bool
	dataKeys   map[string]bool
	groups     []oracleGroup
	adj        map[string][]oracleEdge
	destroyKey map[string]bool
	retainKey  map[string]bool
	destroyBlk map[uint64]bool
	retainBlk  map[uint64]bool
	infeasible string
}

type sector struct {
	block uint64
	key   string
}

type oracleGroup struct {
	name string
	cost uint64
}

type oracleEdge struct {
	group int
	to    string
}

func buildOracle(t *testing.T, p *dp.Problem) *sectorOracle {
	t.Helper()
	o := &sectorOracle{
		sectors:    map[string]map[uint64]sector{},
		rootKeys:   map[string]bool{},
		dataKeys:   map[string]bool{},
		adj:        map[string][]oracleEdge{},
		destroyKey: map[string]bool{},
		retainKey:  map[string]bool{},
		destroyBlk: map[uint64]bool{},
		retainBlk:  map[uint64]bool{},
	}
	for _, s := range p.Snapshots {
		o.sectors[s.ID] = map[uint64]sector{}
		materialize(t, o.sectors[s.ID], s.Root, 0, s.Shift)
	}
	for _, k := range p.Graph.Keys {
		switch k.Role {
		case dp.KeyRoot:
			o.rootKeys[k.ID] = true
		case dp.KeyData:
			o.dataKeys[k.ID] = true
		}
	}
	owner := map[string]int{}
	for _, e := range p.Graph.Edges {
		all := append(append([]string{}, e.ID), e.Aliases...)
		gi, known := owner[e.ID]
		if !known {
			for _, nm := range all {
				if g, ok := owner[nm]; ok {
					gi, known = g, true
					break
				}
			}
		}
		if !known {
			gi = len(o.groups)
			sort.Strings(all)
			o.groups = append(o.groups, oracleGroup{name: all[0], cost: e.Cost})
		}
		for _, nm := range all {
			owner[nm] = gi
		}
		o.adj[e.From] = append(o.adj[e.From], oracleEdge{group: gi, to: e.To})
	}
	for _, req := range p.Destroy {
		for addr := req.Interval.Lo; addr < req.Interval.Hi; addr++ {
			sec, ok := o.sectors[req.Snapshot][addr]
			if !ok {
				continue
			}
			o.destroyKey[sec.key] = true
			o.destroyBlk[sec.block] = true
		}
	}
	for _, req := range p.Retain {
		for addr := req.Interval.Lo; addr < req.Interval.Hi; addr++ {
			sec, ok := o.sectors[req.Snapshot][addr]
			if !ok {
				continue
			}
			o.retainKey[sec.key] = true
			o.retainBlk[sec.block] = true
		}
	}
	for k := range o.destroyKey {
		if o.retainKey[k] {
			o.infeasible = "key-conflict"
			return o
		}
	}
	for b := range o.destroyBlk {
		if o.retainBlk[b] {
			o.infeasible = "block-conflict"
			return o
		}
	}
	if len(o.retainKey) > 0 {
		reach := o.reachable(0)
		for k := range o.retainKey {
			if !reach[k] {
				o.infeasible = "retain-unreachable"
				return o
			}
		}
	}
	return o
}

func materialize(t *testing.T, out map[uint64]sector, n *dp.MapNode, base uint64, shift int64) {
	t.Helper()
	switch n.Kind {
	case dp.NodeHole:
		return
	case dp.NodeLeaf:
		for off := uint64(0); off < n.Span.Lo; off++ {
			addr := base + off
			if shift > 0 {
				addr += uint64(shift)
			} else if shift < 0 {
				addr -= uint64(-shift)
			}
			out[addr] = sector{block: n.BlockBase + off, key: n.DataKey}
		}
	case dp.NodeShift:
		materialize(t, out, n.Inner, base, shift+n.Delta)
	case dp.NodeBranch:
		for _, ch := range n.Children {
			materialize(t, out, ch.Node, base+ch.Offset, shift)
		}
	default:
		t.Fatalf("unknown node kind")
	}
}

func (o *sectorOracle) reachable(disabled uint32) map[string]bool {
	seen := map[string]bool{}
	var queue []string
	for k := range o.rootKeys {
		if !seen[k] {
			seen[k] = true
			queue = append(queue, k)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range o.adj[cur] {
			if disabled&(1<<e.group) != 0 {
				continue
			}
			if !seen[e.to] {
				seen[e.to] = true
				queue = append(queue, e.to)
			}
		}
	}
	return seen
}

func (o *sectorOracle) maskNames(mask uint32) []string {
	var out []string
	for i, g := range o.groups {
		if mask&(1<<i) != 0 {
			out = append(out, g.name)
		}
	}
	sort.Strings(out)
	return out
}

func (o *sectorOracle) optimum() (string, []string, uint64) {
	if o.infeasible != "" {
		return o.infeasible, nil, 0
	}
	total := uint32(1) << len(o.groups)
	found := false
	var bestCost uint64
	var best []string
	for mask := uint32(0); mask < total; mask++ {
		var cost uint64
		for i, g := range o.groups {
			if mask&(1<<i) != 0 {
				cost += g.cost
			}
		}
		reach := o.reachable(mask)
		feasible := true
		for k := range o.destroyKey {
			if reach[k] {
				feasible = false
				break
			}
		}
		if feasible {
			for k := range o.retainKey {
				if !reach[k] {
					feasible = false
					break
				}
			}
		}
		if !feasible {
			continue
		}
		names := o.maskNames(mask)
		if !found || cost < bestCost ||
			(cost == bestCost && len(names) < len(best)) ||
			(cost == bestCost && len(names) == len(best) && cmpGoStrings(names, best) < 0) {
			found, bestCost, best = true, cost, names
		}
	}
	if !found {
		return "cut-coupling", nil, 0
	}
	return "", best, bestCost
}

func cmpGoStrings(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
