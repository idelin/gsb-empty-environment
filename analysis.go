package taskpkg

import "sort"

// PathMarks summarizes one concrete input assignment observed at a forward
// node during full enumeration.
type PathMarks struct {
	Assignment []bool
	Marks      []Mark
	Forwarded  bool
	Missing    Mark
	HasMissing bool
}

// ForwardGuarantee is the per-forward-node mark summary.
type ForwardGuarantee struct {
	NodeID    int
	Name      string
	Required  []Mark
	Reachable bool
	// ReachableMarks lists every distinct mark set observed on any assignment
	// that reaches this forward node.
	ReachableMarks [][]Mark
	// AlwaysPresent lists marks present on every reaching assignment.
	AlwaysPresent []Mark
	// ForwardedMarks lists mark sets on assignments that actually forwarded.
	ForwardedMarks [][]Mark
	// DeniedMarks lists observed (marks, missing) pairs on deny assignments.
	Denied []PathMarks
}

// Enumerate evaluates all 2^Inputs assignments and aggregates per-forward
// mark guarantees. Feasible because Inputs <= MaxInputs.
func (p *Program) Enumerate() ([]ForwardGuarantee, error) {
	n := p.rule.Inputs
	out := map[int]*ForwardGuarantee{}
	order := []int{}

	for mask := 0; mask < 1<<n; mask++ {
		assignment := decode(mask, n)
		snapshots := p.forwardSnapshots(assignment)
		for _, snap := range snapshots {
			g, ok := out[snap.nodeID]
			if !ok {
				g = &ForwardGuarantee{NodeID: snap.nodeID, Name: snap.name, Required: append([]Mark(nil), snap.required...)}
				out[snap.nodeID] = g
				order = append(order, snap.nodeID)
			}
			g.Reachable = true
			g.ReachableMarks = append(g.ReachableMarks, append([]Mark(nil), snap.marks...))
			pm := PathMarks{Assignment: append([]bool(nil), assignment...), Marks: append([]Mark(nil), snap.marks...)}
			if snap.forwarded {
				g.ForwardedMarks = append(g.ForwardedMarks, append([]Mark(nil), snap.marks...))
				pm.Forwarded = true
			} else {
				pm.Missing = snap.missing
				pm.HasMissing = true
				g.Denied = append(g.Denied, pm)
			}
		}
	}

	result := make([]ForwardGuarantee, 0, len(order))
	for _, id := range order {
		g := out[id]
		g.AlwaysPresent = intersectionAll(g.ReachableMarks)
		g.ReachableMarks = uniqueMarkSets(g.ReachableMarks)
		g.ForwardedMarks = uniqueMarkSets(g.ForwardedMarks)
		result = append(result, *g)
	}
	return result, nil
}

type forwardSnapshot struct {
	nodeID    int
	name      string
	required  []Mark
	marks     []Mark
	forwarded bool
	missing   Mark
}

// forwardSnapshots runs one assignment and records the mark state at every
// forward op encountered.
func (p *Program) forwardSnapshots(inputs []bool) []forwardSnapshot {
	k := newKernel(p, inputs, modeCompiled)
	var snaps []forwardSnapshot

	// Reuse the kernel loop with a hook: wrap run by stepping manually.
	k.runWithHook(func(op *Op) {
		if op.Kind != opForward {
			return
		}
		snap := forwardSnapshot{
			nodeID:   op.NodeID,
			name:     op.Fwd.Name,
			required: append([]Mark(nil), op.Fwd.Required...),
			marks:    append([]Mark(nil), k.marks...),
		}
		if missing, bad := MissingRequired(k.marks, op.Fwd.Required); bad {
			snap.missing = missing
		} else {
			snap.forwarded = true
		}
		snaps = append(snaps, snap)
	})
	return snaps
}

func decode(mask, n int) []bool {
	out := make([]bool, n)
	for i := 0; i < n; i++ {
		out[i] = mask&(1<<i) != 0
	}
	return out
}

func intersectionAll(sets [][]Mark) []Mark {
	if len(sets) == 0 {
		return nil
	}
	count := map[Mark]int{}
	for _, set := range sets {
		seen := map[Mark]bool{}
		for _, m := range set {
			if !seen[m] {
				seen[m] = true
				count[m]++
			}
		}
	}
	var out []Mark
	for m, c := range count {
		if c == len(sets) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return !out[i].Trusted && out[j].Trusted
	})
	return out
}

func uniqueMarkSets(sets [][]Mark) [][]Mark {
	seen := map[string]bool{}
	var out [][]Mark
	for _, set := range sets {
		key := markSetKey(set)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, append([]Mark(nil), set...))
	}
	return out
}

func markSetKey(marks []Mark) string {
	cp := append([]Mark(nil), marks...)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].Field != cp[j].Field {
			return cp[i].Field < cp[j].Field
		}
		return cp[i].Name < cp[j].Name
	})
	var b []byte
	for _, m := range cp {
		b = append(b, m.Field...)
		b = append(b, '/')
		b = append(b, m.Name...)
		if m.Trusted {
			b = append(b, 'T')
		}
		b = append(b, ';')
	}
	return string(b)
}
