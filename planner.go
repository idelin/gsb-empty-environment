package main

import (
	"fmt"
	"sort"
	"time"
)

// ObjectInput describes one object in the deletion plan request.
//
// Semantics:
//   - RetentionUntil: the object may not be deleted before this time.
//   - LegalHold + HoldUntil: extra restrictions, interpreted only from these
//     input flags. LegalHold without HoldUntil blocks deletion permanently;
//     HoldUntil (with or without LegalHold) blocks deletion until that time.
//   - StrongRefs: ids this object strongly references. A strong reference
//     keeps the target alive: the target cannot be deleted before the
//     referrer. Cycles of strong references are deleted as a single unit.
//   - WeakRefs: parsed but never block deletion.
type ObjectInput struct {
	ID             string     `json:"id"`
	RetentionUntil *time.Time `json:"retention_until,omitempty"`
	LegalHold      bool       `json:"legal_hold,omitempty"`
	HoldUntil      *time.Time `json:"hold_until,omitempty"`
	StrongRefs     []string   `json:"strong_refs,omitempty"`
	WeakRefs       []string   `json:"weak_refs,omitempty"`
}

// Input is the full planning request.
type Input struct {
	Objects []ObjectInput `json:"objects"`
}

// Step is one hop in a permanent-blocking reason chain.
type Step struct {
	Object string `json:"object"`
	Reason string `json:"reason"`
}

// BlockedObject reports one object that can never be deleted and why.
type BlockedObject struct {
	Object string `json:"object"`
	Chain  []Step `json:"chain"`
}

// Batch is a group of objects sharing the same earliest deletion time.
// A zero Time means the objects are deletable immediately.
type Batch struct {
	Time    time.Time `json:"time"`
	Objects []string  `json:"objects"`
}

// Plan is the planner output: deletable batches plus permanently blocked
// objects with their reason chains.
type Plan struct {
	Batches            []Batch         `json:"batches"`
	PermanentlyBlocked []BlockedObject `json:"permanently_blocked"`
}

// sccCause records why an SCC is permanently blocked.
type sccCause struct {
	blocked  bool
	kind     string // "hold" or "referenced_by"
	object   string // member of this SCC where the cause is anchored
	referrer string // object in another SCC (only for "referenced_by")
}

// PlanDeletion computes the deletion plan for the given input.
func PlanDeletion(in Input) (Plan, error) {
	objects, strong, err := buildGraph(in)
	if err != nil {
		return Plan{}, err
	}

	sccs := tarjanSCC(objects, strong)
	sccOf := make(map[string]int, len(objects))
	for i, members := range sccs {
		for _, m := range members {
			sccOf[m] = i
		}
	}

	// ownTime[s]: earliest time this SCC could go considering only its
	// members' retention/hold flags. holdObj[s]: a member with a permanent
	// legal hold, if any.
	ownTime := make([]time.Time, len(sccs))
	holdObj := make([]string, len(sccs))
	byID := make(map[string]ObjectInput, len(objects))
	for _, o := range in.Objects {
		byID[o.ID] = o
	}
	for i, members := range sccs {
		for _, m := range members {
			o := byID[m]
			t := ownTime[i]
			if o.RetentionUntil != nil && o.RetentionUntil.After(t) {
				t = *o.RetentionUntil
			}
			if o.HoldUntil != nil && o.HoldUntil.After(t) {
				t = *o.HoldUntil
			}
			ownTime[i] = t
			if o.LegalHold && o.HoldUntil == nil && (holdObj[i] == "" || m < holdObj[i]) {
				holdObj[i] = m
			}
		}
	}

	// Condensation DAG: edge referrer SCC -> referent SCC. The referent
	// cannot be deleted before the referrer. Also keep witness object
	// edges for reason-chain construction.
	type witness struct{ from, to string }
	referrers := make([][]int, len(sccs))     // referent -> referrer SCCs
	referents := make([][]int, len(sccs))     // referrer -> referent SCCs
	witnesses := make([][]witness, len(sccs)) // referent -> witness edges
	seenEdge := make(map[[2]int]bool)
	for _, u := range objects {
		for _, v := range strong[u] {
			su, sv := sccOf[u], sccOf[v]
			if su == sv {
				continue
			}
			witnesses[sv] = append(witnesses[sv], witness{from: u, to: v})
			key := [2]int{su, sv}
			if !seenEdge[key] {
				seenEdge[key] = true
				referrers[sv] = append(referrers[sv], su)
				referents[su] = append(referents[su], sv)
			}
		}
	}
	for i := range witnesses {
		sort.Slice(witnesses[i], func(a, b int) bool {
			if witnesses[i][a].from != witnesses[i][b].from {
				return witnesses[i][a].from < witnesses[i][b].from
			}
			return witnesses[i][a].to < witnesses[i][b].to
		})
	}

	// Process SCCs in topological order (referrers first), propagating
	// earliest deletion times and permanent blocking.
	order := topoOrder(len(sccs), referents, referrers)
	earliest := make([]time.Time, len(sccs))
	causes := make([]sccCause, len(sccs))
	for _, s := range order {
		earliest[s] = ownTime[s]
		if holdObj[s] != "" {
			causes[s] = sccCause{blocked: true, kind: "hold", object: holdObj[s]}
		}
		for _, r := range referrers[s] {
			if earliest[r].After(earliest[s]) {
				earliest[s] = earliest[r]
			}
		}
		if !causes[s].blocked {
			for _, w := range witnesses[s] {
				if causes[sccOf[w.from]].blocked {
					causes[s] = sccCause{
						blocked:  true,
						kind:     "referenced_by",
						object:   w.to,
						referrer: w.from,
					}
					break
				}
			}
		}
	}

	return buildOutput(sccs, sccOf, causes, earliest), nil
}

// buildGraph validates the input and returns sorted object ids plus the
// strong-reference adjacency map.
func buildGraph(in Input) ([]string, map[string][]string, error) {
	seen := make(map[string]bool, len(in.Objects))
	ids := make([]string, 0, len(in.Objects))
	for _, o := range in.Objects {
		if o.ID == "" {
			return nil, nil, fmt.Errorf("object with empty id")
		}
		if seen[o.ID] {
			return nil, nil, fmt.Errorf("duplicate object id %q", o.ID)
		}
		seen[o.ID] = true
		ids = append(ids, o.ID)
	}
	strong := make(map[string][]string, len(in.Objects))
	for _, o := range in.Objects {
		for _, ref := range o.StrongRefs {
			if !seen[ref] {
				return nil, nil, fmt.Errorf("object %q strongly references unknown object %q", o.ID, ref)
			}
			strong[o.ID] = append(strong[o.ID], ref)
		}
		for _, ref := range o.WeakRefs {
			if !seen[ref] {
				return nil, nil, fmt.Errorf("object %q weakly references unknown object %q", o.ID, ref)
			}
		}
	}
	sort.Strings(ids)
	return ids, strong, nil
}

// tarjanSCC returns the strongly connected components of the graph.
// Each component's members are sorted; components are ordered by their
// smallest member for deterministic output.
func tarjanSCC(nodes []string, edges map[string][]string) [][]string {
	index := make(map[string]int, len(nodes))
	lowlink := make(map[string]int, len(nodes))
	onStack := make(map[string]bool, len(nodes))
	var stack []string
	var sccs [][]string
	counter := 0

	var visit func(v string)
	visit = func(v string) {
		index[v] = counter
		lowlink[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range edges[v] {
			if _, ok := index[w]; !ok {
				visit(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] {
				if index[w] < lowlink[v] {
					lowlink[v] = index[w]
				}
			}
		}
		if lowlink[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sort.Strings(comp)
			sccs = append(sccs, comp)
		}
	}
	for _, v := range nodes {
		if _, ok := index[v]; !ok {
			visit(v)
		}
	}
	sort.Slice(sccs, func(i, j int) bool { return sccs[i][0] < sccs[j][0] })
	return sccs
}

// topoOrder returns SCC indices with referrers before referents
// (Kahn's algorithm on the condensation DAG).
func topoOrder(n int, referents, referrers [][]int) []int {
	inDegree := make([]int, n)
	for s := 0; s < n; s++ {
		inDegree[s] = len(referrers[s])
	}
	var queue []int
	for s := 0; s < n; s++ {
		if inDegree[s] == 0 {
			queue = append(queue, s)
		}
	}
	sort.Ints(queue)
	var order []int
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		order = append(order, s)
		for _, t := range referents[s] {
			inDegree[t]--
			if inDegree[t] == 0 {
				queue = append(queue, t)
			}
		}
	}
	return order
}

// buildOutput assembles batches and permanent-blocking reason chains.
func buildOutput(sccs [][]string, sccOf map[string]int, causes []sccCause, earliest []time.Time) Plan {
	byTime := make(map[time.Time][]string)
	var blocked []BlockedObject
	for s, members := range sccs {
		if causes[s].blocked {
			for _, m := range members {
				blocked = append(blocked, BlockedObject{
					Object: m,
					Chain:  buildChain(m, sccOf, causes),
				})
			}
			continue
		}
		byTime[earliest[s]] = append(byTime[earliest[s]], members...)
	}

	var times []time.Time
	for t := range byTime {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	plan := Plan{Batches: []Batch{}, PermanentlyBlocked: []BlockedObject{}}
	for _, t := range times {
		objs := byTime[t]
		sort.Strings(objs)
		plan.Batches = append(plan.Batches, Batch{Time: t, Objects: objs})
	}
	sort.Slice(blocked, func(i, j int) bool { return blocked[i].Object < blocked[j].Object })
	plan.PermanentlyBlocked = blocked
	return plan
}

// buildChain reconstructs the reason chain for a permanently blocked
// object by walking from its SCC's cause to the root cause.
func buildChain(obj string, sccOf map[string]int, causes []sccCause) []Step {
	chain := []Step{}
	cur := obj
	for {
		c := causes[sccOf[cur]]
		if cur != c.object {
			chain = append(chain, Step{
				Object: cur,
				Reason: fmt.Sprintf("in strong-reference cycle with %s; cycle is deleted as a unit", c.object),
			})
			cur = c.object
		}
		if c.kind == "hold" {
			chain = append(chain, Step{
				Object: cur,
				Reason: "legal hold with no expiry (permanent)",
			})
			return chain
		}
		chain = append(chain, Step{
			Object: cur,
			Reason: fmt.Sprintf("strongly referenced by %s", c.referrer),
		})
		cur = c.referrer
	}
}
