package netsec

import (
	"fmt"
	"strings"
)

// Witness is evidence that one feasible build can mix conflicting
// dependency instances: a concrete variable assignment plus the
// dependency paths leading from the roots to each conflicting instance.
type Witness struct {
	Assign  uint32 // bit i holds the value of configuration variable i
	NumVars int
	Paths   [2][]Instance
}

func (w *Witness) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "assign=%0*b\n", w.NumVars, w.Assign)
	for i, p := range w.Paths {
		fmt.Fprintf(&b, "path%d: ", i)
		for j, inst := range p {
			if j > 0 {
				b.WriteString(" -> ")
			}
			b.WriteString(inst.String())
		}
		if i == 0 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// FindConflictWitness enumerates every feasible configuration assignment
// and returns the shortest witness of a feasible build that would mix
// conflicting instances, or nil if no feasible build can. The cost of a
// witness is the sum of the two dependency path lengths; the returned
// witness minimizes that cost over all assignments and conflicting pairs.
//
// Reachability is computed per assignment with a visited set, so cyclic
// dependencies terminate.
func (g *Graph) FindConflictWitness() (*Witness, error) {
	if g.NumVars < 0 || g.NumVars > MaxVars {
		return nil, fmt.Errorf("netsec: NumVars %d out of range [0,%d]", g.NumVars, MaxVars)
	}
	var best *Witness
	bestCost := 0
	limit := uint32(1) << uint(g.NumVars)
	for assign := uint32(0); assign < limit; assign++ {
		if !g.feasible(assign) {
			continue
		}
		if w, cost := g.witnessFor(assign); w != nil && (best == nil || cost < bestCost) {
			best, bestCost = w, cost
		}
	}
	return best, nil
}

// witnessFor computes shortest paths from the roots under one assignment
// and returns the cheapest conflicting pair reachable together.
func (g *Graph) witnessFor(assign uint32) (*Witness, int) {
	type groupKey struct{ name, version string }

	depth := make(map[Instance]int)
	parent := make(map[Instance]Instance)
	hasParent := make(map[Instance]bool)
	byGroup := make(map[groupKey][]Instance)

	queue := make([]Instance, 0, len(g.Roots))
	push := func(inst Instance, d int, from Instance, ok bool) {
		depth[inst] = d
		if ok {
			parent[inst] = from
			hasParent[inst] = true
		}
		queue = append(queue, inst)
		k := groupKey{inst.Name, inst.Version}
		byGroup[k] = append(byGroup[k], inst)
	}
	for _, r := range g.Roots {
		if _, seen := depth[r]; !seen {
			push(r, 0, Instance{}, false)
		}
	}
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		for _, e := range g.Edges[cur] {
			if !e.enabled(assign) {
				continue
			}
			if _, seen := depth[e.To]; seen {
				continue
			}
			push(e.To, depth[cur]+1, cur, true)
		}
	}

	var best *Witness
	bestCost := 0
	for _, insts := range byGroup {
		for a := 0; a < len(insts); a++ {
			for b := a + 1; b < len(insts); b++ {
				if !insts[a].ConflictsWith(insts[b]) {
					continue
				}
				cost := depth[insts[a]] + depth[insts[b]]
				if best == nil || cost < bestCost {
					best = &Witness{
						Assign:  assign,
						NumVars: g.NumVars,
						Paths: [2][]Instance{
							buildPath(insts[a], parent, hasParent),
							buildPath(insts[b], parent, hasParent),
						},
					}
					bestCost = cost
				}
			}
		}
	}
	return best, bestCost
}

func buildPath(inst Instance, parent map[Instance]Instance, hasParent map[Instance]bool) []Instance {
	var rev []Instance
	for cur := inst; ; {
		rev = append(rev, cur)
		if !hasParent[cur] {
			break
		}
		cur = parent[cur]
	}
	path := make([]Instance, len(rev))
	for i, v := range rev {
		path[len(rev)-1-i] = v
	}
	return path
}
