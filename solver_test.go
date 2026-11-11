package netsec

import (
	"reflect"
	"testing"
)

// bruteForceOptimal is an independent, straightforward enumeration used only
// by the tests to confirm the solver returns a globally minimum cost.
func bruteForceOptimal(t *testing.T, p *Problem) int {
	t.Helper()
	c, err := compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	disabled := newBitset(len(c.gate))
	best := -1
	var mask uint32
	for ; mask < uint32(1)<<uint(len(c.patches)); mask++ {
		cost := 0
		for i, pc := range c.patches {
			if mask&(uint32(1)<<uint(i)) != 0 {
				disabled.set(pc.action)
				cost += pc.cost
			}
		}
		reach, _ := c.simulate(disabled, true)
		if !reach.has(c.target) && (best == -1 || cost < best) {
			best = cost
		}
		for i := range disabled {
			disabled[i] = 0
		}
	}
	return best
}

func validateSolution(t *testing.T, p *Problem, s *Solution) {
	t.Helper()

	if s.Cost < 0 {
		t.Fatalf("negative cost %d", s.Cost)
	}

	patchByID := map[string]Patch{}
	selectedCost := 0
	for _, pc := range p.Patches {
		patchByID[pc.ID] = pc
	}
	for _, id := range s.Selected {
		pc, ok := patchByID[id]
		if !ok {
			t.Fatalf("selected unknown patch %q", id)
		}
		selectedCost += pc.Cost
	}
	if selectedCost != s.Cost {
		t.Fatalf("reported cost %d, recomputed %d", s.Cost, selectedCost)
	}
	for i := 1; i < len(s.Selected); i++ {
		if s.Selected[i-1] >= s.Selected[i] {
			t.Fatalf("selected IDs not sorted: %v", s.Selected)
		}
	}

	// Blocking proof: disabled actions must leave the target unreachable.
	res, err := Reachability(p, s.Proof.Blocking.DisabledActions)
	if err != nil {
		t.Fatalf("blocking reachability: %v", err)
	}
	if res.Reachable {
		t.Fatalf("target still reachable under selected patches")
	}
	if !reflect.DeepEqual(res.Closure, s.Proof.Blocking.ReachableClosure) {
		t.Fatalf("closure mismatch: got %v want %v", s.Proof.Blocking.ReachableClosure, res.Closure)
	}

	// Minimality: every witness must be a valid activation sequence after the
	// named patch is omitted.
	if len(s.Proof.Minimality) != len(s.Selected) {
		t.Fatalf("got %d witnesses for %d patches", len(s.Proof.Minimality), len(s.Selected))
	}
	selectedSet := map[string]bool{}
	for _, id := range s.Selected {
		selectedSet[id] = true
	}
	for _, w := range s.Proof.Minimality {
		if !selectedSet[w.OmittedPatch] {
			t.Fatalf("witness for non-selected patch %q", w.OmittedPatch)
		}
		if len(w.ActivationOrder) == 0 || w.ActivationOrder[len(w.ActivationOrder)-1] != p.Target {
			t.Fatalf("witness %v does not end at target", w)
		}
		disabled := map[string]bool{}
		for _, id := range s.Selected {
			if id != w.OmittedPatch {
				disabled[patchByID[id].Action] = true
			}
		}
		var ids []string
		for id := range disabled {
			ids = append(ids, id)
		}
		omitted, err := Reachability(p, ids)
		if err != nil {
			t.Fatalf("omission reachability: %v", err)
		}
		if !omitted.Reachable {
			t.Fatalf("omitting %q did not re-open the target", w.OmittedPatch)
		}
		if !reflect.DeepEqual(omitted.ActivationOrder, w.ActivationOrder) {
			t.Fatalf("witness mismatch for %q: got %v want %v", w.OmittedPatch, w.ActivationOrder, omitted.ActivationOrder)
		}
		verifyActivation(t, p, w.ActivationOrder, disabled)
	}

	if want := bruteForceOptimal(t, p); want != s.Cost {
		t.Fatalf("cost %d is not optimal; brute force says %d", s.Cost, want)
	}
}

// verifyActivation checks each witness step against the gates of the graph,
// proving the sequence is a genuine activation order rather than an arbitrary
// reachable list.
func verifyActivation(t *testing.T, p *Problem, order []string, disabled map[string]bool) {
	t.Helper()
	nodes := map[string]Node{}
	for _, n := range p.Nodes {
		nodes[n.ID] = n
	}
	active := map[string]bool{}
	for _, id := range order {
		if disabled[id] {
			t.Fatalf("witness activates disabled node %q", id)
		}
		n := nodes[id]
		switch n.Gate {
		case GateAND:
			for _, pre := range n.Preconditions {
				if !active[pre] {
					t.Fatalf("witness node %q activates before AND precondition %q", id, pre)
				}
			}
		case GateOR:
			if len(n.Preconditions) == 0 {
				t.Fatalf("witness node %q has an unsatisfiable empty OR gate", id)
			}
			ok := false
			for _, pre := range n.Preconditions {
				if active[pre] {
					ok = true
					break
				}
			}
			if !ok {
				t.Fatalf("witness node %q activates without an OR precondition", id)
			}
		}
		active[id] = true
	}
}
