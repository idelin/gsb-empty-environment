package gdict

import (
	"fmt"
	"sort"
)

type edgeReason struct {
	kind      string // "collation-order" or "range-scan"
	version   string
	partition string
	cut       string
	cutCode   uint64
	bound     Bound
	leftKey   Key
	rightKey  Key
	leftOld   uint64
	rightOld  uint64
}

func (r edgeReason) observation() Observation {
	return Observation{
		Kind:         r.kind,
		Version:      r.version,
		Partition:    r.partition,
		Cut:          r.cut,
		OldCode:      r.cutCode,
		Bound:        r.bound,
		LeftKey:      r.leftKey,
		RightKey:     r.rightKey,
		LeftOldCode:  r.leftOld,
		RightOldCode: r.rightOld,
	}
}

type dag struct {
	n       int
	adj     [][]int
	indeg   []int
	reasons map[[2]int]edgeReason
}

func newDAG(n int) *dag {
	return &dag{n: n, adj: make([][]int, n), indeg: make([]int, n), reasons: make(map[[2]int]edgeReason)}
}

func (d *dag) add(a, b int, r edgeReason) {
	if a == b {
		return
	}
	key := [2]int{a, b}
	if _, ok := d.reasons[key]; ok {
		return
	}
	d.reasons[key] = r
	d.adj[a] = append(d.adj[a], b)
	d.indeg[b]++
}

// buildDAG constructs the constraints contributed by all collation versions
// and all still-live range scans. Nodes are active logical key groups.
func buildDAG(n *normalized) (*dag, error) {
	idx := make(map[int]int, len(n.activeIDs))
	for i, gid := range n.activeIDs {
		idx[gid] = i
	}
	d := newDAG(len(n.activeIDs))
	// Collation-order edges.
	for ai := 0; ai < len(n.activeIDs); ai++ {
		for bi := ai + 1; bi < len(n.activeIDs); bi++ {
			ga, gb := n.groups[n.activeIDs[ai]], n.groups[n.activeIDs[bi]]
			for vi, v := range n.versions {
				if ga.ranks[vi] < gb.ranks[vi] {
					d.add(ai, bi, edgeReason{kind: "collation-order", version: v, leftKey: ga.canonical, rightKey: gb.canonical})
				} else if gb.ranks[vi] < ga.ranks[vi] {
					d.add(bi, ai, edgeReason{kind: "collation-order", version: v, leftKey: gb.canonical, rightKey: ga.canonical})
				}
			}
		}
	}

	// Range-scan edges. A cut splits old rows into a left set L and a right
	// set R. Every global code in L must be smaller than every code in R;
	// otherwise no translated prefix cut can return the old row set.
	for _, p := range n.parts {
		for _, c := range p.cuts {
			var left, right []entryInfo
			for _, e := range p.entries {
				before := e.oldCode < c.OldCode
				if c.Bound == LE {
					before = e.oldCode <= c.OldCode
				}
				if before {
					left = append(left, e)
				} else {
					right = append(right, e)
				}
			}
			// A single logical key has one physical code. If a cut puts two
			// occurrences of that key on opposite sides, no page can split
			// them; report the two concrete range observations.
			rightGroup := make(map[int]entryInfo)
			for _, e := range right {
				if _, ok := rightGroup[e.group]; !ok {
					rightGroup[e.group] = e
				}
			}
			for _, le := range left {
				if re, ok := rightGroup[le.group]; ok {
					return nil, &IncompatibleError{Cert: Certificate{
						Kind:    "cut-splits-identical-key",
						Message: fmt.Sprintf("cut %s/%s places two occurrences of logical key %s at old codes %d and %d on opposite sides", p.name, c.Name, le.key, le.oldCode, re.oldCode),
						Observations: []Observation{
							{Kind: "range-scan", Partition: p.name, Cut: c.Name, OldCode: c.OldCode, Bound: c.Bound, LeftKey: le.key, RightKey: re.key, LeftOldCode: le.oldCode, RightOldCode: re.oldCode, Detail: "left occurrence"},
							{Kind: "range-scan", Partition: p.name, Cut: c.Name, OldCode: c.OldCode, Bound: c.Bound, LeftKey: re.key, RightKey: le.key, LeftOldCode: re.oldCode, RightOldCode: le.oldCode, Detail: "right occurrence of the same key"},
						},
						Requirement: "one logical key maps to one physical code, so a listed cut cannot split its occurrences",
					}}
				}
			}
			for _, le := range left {
				for _, re := range right {
					d.add(idx[le.group], idx[re.group], edgeReason{
						kind:      "range-scan",
						partition: p.name,
						cut:       c.Name,
						cutCode:   c.OldCode,
						bound:     c.Bound,
						leftKey:   le.key,
						rightKey:  re.key,
						leftOld:   le.oldCode,
						rightOld:  re.oldCode,
					})
				}
			}
		}
	}

	if err := d.checkFixedOrder(n); err != nil {
		return nil, err
	}
	if err := d.checkCycles(n); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *dag) checkFixedOrder(n *normalized) error {
	codeOf := func(i int) (uint64, bool) {
		f := n.groups[n.activeIDs[i]].fixed
		if f == nil {
			return 0, false
		}
		return *f, true
	}
	var conflicts []Fixed
	for a := 0; a < d.n; a++ {
		for _, b := range d.adj[a] {
			ca, oka := codeOf(a)
			cb, okb := codeOf(b)
			if oka && okb && ca >= cb {
				conflicts = append(conflicts, Fixed{Code: ca, Key: n.groups[n.activeIDs[a]].canonical})
				conflicts = append(conflicts, Fixed{Code: cb, Key: n.groups[n.activeIDs[b]].canonical})
				return &IncompatibleError{Cert: Certificate{
					Kind:         "fixed-code-order-conflict",
					Message:      fmt.Sprintf("fixed codes %d (%s) and %d (%s) contradict a strict ordering constraint", ca, n.groups[n.activeIDs[a]].canonical, cb, n.groups[n.activeIDs[b]].canonical),
					Fixed:        dedupFixed(conflicts),
					Observations: []Observation{d.reasons[[2]int{a, b}].observation()},
					Requirement:  fmt.Sprintf("the mapping requires %s before %s", n.groups[n.activeIDs[a]].canonical, n.groups[n.activeIDs[b]].canonical),
				}}
			}
		}
	}
	// Two fixed codes that are equal must denote one logical key group.
	byCode := make(map[uint64][]int)
	for i := 0; i < d.n; i++ {
		if f := n.groups[n.activeIDs[i]].fixed; f != nil {
			byCode[*f] = append(byCode[*f], i)
		}
	}
	for code, ids := range byCode {
		if len(ids) < 2 {
			continue
		}
		allowed := true
		for i := 0; i < len(ids) && allowed; i++ {
			for j := i + 1; j < len(ids); j++ {
				a, b := ids[i], ids[j]
				for vi := range n.versions {
					if n.groups[n.activeIDs[a]].ranks[vi] != n.groups[n.activeIDs[b]].ranks[vi] {
						allowed = false
						break
					}
				}
				if d.reachable(a, b) || d.reachable(b, a) {
					allowed = false
				}
			}
		}
		if allowed {
			continue
		}
		var fs []Fixed
		for _, id := range ids {
			fs = append(fs, Fixed{Code: code, Key: n.groups[n.activeIDs[id]].canonical})
		}
		return &IncompatibleError{Cert: Certificate{
			Kind:        "fixed-code-collision",
			Message:     fmt.Sprintf("fixed code %d is claimed by more than one logical key", code),
			Fixed:       dedupFixed(fs),
			Requirement: "one physical code denotes one global dictionary entry unless the keys are equivalent in every collation",
		}}
	}
	return nil
}

func dedupFixed(in []Fixed) []Fixed {
	seen := make(map[Fixed]bool)
	out := make([]Fixed, 0, len(in))
	for _, f := range in {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Key.Less(out[j].Key)
	})
	return out
}

// checkCycles finds a directed cycle and turns its edges into a certificate
// made of concrete collation/range observations.
func (d *dag) checkCycles(n *normalized) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]uint8, d.n)
	parent := make([]int, d.n)
	for i := range parent {
		parent[i] = -1
	}
	type frame struct {
		node int
		next int
	}
	var stack []frame
	for start := 0; start < d.n; start++ {
		if color[start] != white {
			continue
		}
		stack = append(stack[:0], frame{node: start})
		color[start] = gray
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.next >= len(d.adj[f.node]) {
				color[f.node] = black
				stack = stack[:len(stack)-1]
				continue
			}
			to := d.adj[f.node][f.next]
			f.next++
			if color[to] == white {
				parent[to] = f.node
				color[to] = gray
				stack = append(stack, frame{node: to})
			} else if color[to] == gray {
				// Reconstruct to -> ... -> f.node -> to.
				cycle := []int{to}
				for x := f.node; x != to; x = parent[x] {
					cycle = append(cycle, x)
					if x < 0 {
						break
					}
				}
				cycle = append(cycle, to)
				// reverse to path order
				for i, j := 0, len(cycle)-1; i < j; i, j = i+1, j-1 {
					cycle[i], cycle[j] = cycle[j], cycle[i]
				}
				obs := make([]Observation, 0, len(cycle)-1)
				for i := 0; i+1 < len(cycle); i++ {
					a, b := cycle[i], cycle[i+1]
					r, ok := d.reasons[[2]int{a, b}]
					if ok {
						obs = append(obs, r.observation())
					}
				}
				if len(obs) == 0 {
					obs = append(obs,
						Observation{Kind: "collation-order", Version: n.versions[0], LeftKey: n.groups[n.activeIDs[to]].canonical, RightKey: n.groups[n.activeIDs[to]].canonical},
						Observation{Kind: "collation-order", Version: n.versions[0], LeftKey: n.groups[n.activeIDs[to]].canonical, RightKey: n.groups[n.activeIDs[to]].canonical},
					)
				}
				if len(obs) == 1 {
					obs = append(obs, obs[0])
				}
				return &IncompatibleError{Cert: Certificate{
					Kind:         "ordering-cycle",
					Message:      "the listed queries and collations imply a strict ordering cycle",
					Observations: obs,
					Requirement:  "a single global code order cannot satisfy every listed range scan and collation",
				}}
			}
		}
	}
	return nil
}

func (d *dag) reachable(a, b int) bool {
	if a == b {
		return true
	}
	seen := make([]bool, d.n)
	stack := []int{a}
	seen[a] = true
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, y := range d.adj[x] {
			if y == b {
				return true
			}
			if !seen[y] {
				seen[y] = true
				stack = append(stack, y)
			}
		}
	}
	return false
}
