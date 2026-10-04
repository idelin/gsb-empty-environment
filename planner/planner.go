package planner

import (
	"sort"
	"strings"
)

// Schedule validates the steps and schedules them into phases.
//
// Rules enforced:
//   - at most MaxSteps steps;
//   - Requires must reference known steps and be acyclic;
//   - per (table, column): add_column -> backfill -> add_constraint
//     (and add_column -> create_index) may not be inverted; missing
//     links of that chain are injected automatically;
//   - conflicting steps (read/write or write/write on the same object)
//     never share a phase;
//   - steps needing a write stop are shifted as late as their
//     dependents allow so they cluster into the fewest possible
//     write-stop windows.
func Schedule(steps []Step) (*Plan, error) {
	if len(steps) > MaxSteps {
		return nil, errorf("too many steps: %d exceeds the limit of %d", len(steps), MaxSteps)
	}
	p := &Plan{}
	if len(steps) == 0 {
		return p, nil
	}

	work := make([]Step, len(steps))
	copy(work, steps)
	byID := make(map[string]*Step, len(work))
	for i := range work {
		s := &work[i]
		if s.ID == "" {
			return nil, errorf("step %d: empty id", i)
		}
		if _, dup := byID[s.ID]; dup {
			return nil, errorf("duplicate step id %q", s.ID)
		}
		s.applyDefaults()
		byID[s.ID] = s
	}

	// Explicit requires, as a set per step.
	deps := make(map[string]map[string]bool, len(work))
	for i := range work {
		s := &work[i]
		deps[s.ID] = map[string]bool{}
		for _, r := range s.Requires {
			if r == s.ID {
				return nil, errorf("step %q requires itself", s.ID)
			}
			if _, ok := byID[r]; !ok {
				return nil, errorf("step %q requires unknown step %q", s.ID, r)
			}
			deps[s.ID][r] = true
		}
	}

	if err := verifyLifecycle(work, deps, p); err != nil {
		return nil, err
	}

	order, err := topoSort(work, deps)
	if err != nil {
		return nil, err
	}

	phase := schedulePhases(work, byID, deps, order)
	latest := latestPhases(work, deps, phase)
	moveWriteStopSteps(work, byID, phase, latest)

	return buildPlan(work, phase, p), nil
}

// verifyLifecycle checks the add_column -> backfill -> add_constraint
// chain per column against the explicit requires, then injects the
// missing edges of the chain so the scheduler cannot misorder it.
func verifyLifecycle(steps []Step, deps map[string]map[string]bool, p *Plan) error {
	type key struct{ table, column string }
	type group struct {
		adds, fills, cons, idxs []string
	}
	groups := map[key]*group{}
	var keys []key
	for i := range steps {
		s := &steps[i]
		k := key{s.Table, s.Column}
		g, ok := groups[k]
		if !ok {
			g = &group{}
			groups[k] = g
			keys = append(keys, k)
		}
		switch s.Kind {
		case KindAddColumn:
			g.adds = append(g.adds, s.ID)
		case KindBackfill:
			g.fills = append(g.fills, s.ID)
		case KindAddConstraint:
			g.cons = append(g.cons, s.ID)
		case KindCreateIndex:
			if s.Column != "" {
				g.idxs = append(g.idxs, s.ID)
			}
		}
	}

	// reachable reports whether `to` is already a (transitive)
	// prerequisite of `from` via the explicit requires.
	reachable := func(from, to string) bool {
		seen := map[string]bool{}
		stack := []string{from}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n == to {
				return true
			}
			if seen[n] {
				continue
			}
			seen[n] = true
			for d := range deps[n] {
				stack = append(stack, d)
			}
		}
		return false
	}

	for _, k := range keys {
		g := groups[k]
		obj := Object{Table: k.table, Column: k.column}
		if len(g.adds) > 1 {
			return errorf("duplicate add_column steps for %s: %s", obj, strings.Join(g.adds, ", "))
		}
		// Explicit requires must not invert the chain.
		for _, a := range g.adds {
			for _, later := range append(append([]string{}, g.fills...), g.cons...) {
				if reachable(a, later) {
					return errorf("lifecycle misordered on %s: add_column %q must not depend on %q", obj, a, later)
				}
			}
		}
		for _, f := range g.fills {
			for _, c := range g.cons {
				if reachable(f, c) {
					return errorf("lifecycle misordered on %s: backfill %q must not depend on add_constraint %q", obj, f, c)
				}
			}
		}
		if len(g.adds) == 0 {
			if len(g.fills) > 0 {
				p.Warnings = append(p.Warnings, "no add_column step for "+obj.String()+"; assuming the column already exists")
			}
			continue
		}
		add := g.adds[0]
		// Inject the missing edges of the chain.
		for _, f := range g.fills {
			deps[f][add] = true
		}
		for _, c := range g.cons {
			if len(g.fills) == 0 {
				deps[c][add] = true
			}
			for _, f := range g.fills {
				deps[c][f] = true
			}
		}
		for _, ix := range g.idxs {
			deps[ix][add] = true
		}
	}
	return nil
}

// topoSort returns a deterministic topological order (Kahn's algorithm,
// lexicographically smallest ready id first) or a cycle error.
func topoSort(steps []Step, deps map[string]map[string]bool) ([]string, error) {
	indeg := map[string]int{}
	dependents := map[string][]string{}
	for _, s := range steps {
		indeg[s.ID] = len(deps[s.ID])
		for d := range deps[s.ID] {
			dependents[d] = append(dependents[d], s.ID)
		}
	}
	var ready []string
	for id, n := range indeg {
		if n == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	var order []string
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, m := range dependents[id] {
			indeg[m]--
			if indeg[m] == 0 {
				ready = append(ready, m)
			}
		}
		sort.Strings(ready)
	}
	if len(order) != len(steps) {
		var cyc []string
		for id, n := range indeg {
			if n > 0 {
				cyc = append(cyc, id)
			}
		}
		sort.Strings(cyc)
		return nil, errorf("dependency cycle involving: %s", strings.Join(cyc, ", "))
	}
	return order, nil
}

// schedulePhases places every step in the earliest phase after its
// dependencies that has no conflicting sibling.
func schedulePhases(steps []Step, byID map[string]*Step, deps map[string]map[string]bool, order []string) map[string]int {
	phase := map[string]int{}
	occupants := map[int][]string{}
	for _, id := range order {
		p := 1
		for d := range deps[id] {
			if phase[d]+1 > p {
				p = phase[d] + 1
			}
		}
		for hasConflict(byID[id], occupants[p], byID) {
			p++
		}
		phase[id] = p
		occupants[p] = append(occupants[p], id)
	}
	return phase
}

func hasConflict(s *Step, ids []string, byID map[string]*Step) bool {
	for _, id := range ids {
		if conflicts(*s, *byID[id]) {
			return true
		}
	}
	return false
}

// latestPhases computes, per step, the last phase it may occupy without
// delaying any dependent (dependents never move earlier than their
// originally scheduled phase).
func latestPhases(steps []Step, deps map[string]map[string]bool, phase map[string]int) map[string]int {
	max := 0
	for _, p := range phase {
		if p > max {
			max = p
		}
	}
	latest := map[string]int{}
	for _, s := range steps {
		latest[s.ID] = max
	}
	for _, s := range steps {
		for d := range deps[s.ID] {
			if phase[s.ID]-1 < latest[d] {
				latest[d] = phase[s.ID] - 1
			}
		}
	}
	return latest
}

// moveWriteStopSteps clusters write-stop steps into as few windows as
// possible. Each step s may occupy any phase in [phase[s], latest[s]];
// finding the minimum number of windows is interval stabbing, solved
// greedily. Conflicts may force extra windows; the result stays valid.
func moveWriteStopSteps(steps []Step, byID map[string]*Step, phase, latest map[string]int) {
	var ws []string
	for _, s := range steps {
		if s.NeedsWriteStop {
			ws = append(ws, s.ID)
		}
	}
	if len(ws) < 2 {
		return
	}
	sort.Slice(ws, func(i, j int) bool {
		if latest[ws[i]] != latest[ws[j]] {
			return latest[ws[i]] < latest[ws[j]]
		}
		return ws[i] < ws[j]
	})

	occupants := map[int][]string{}
	for id, p := range phase {
		occupants[p] = append(occupants[p], id)
	}
	move := func(id string, to int) {
		from := phase[id]
		occ := occupants[from]
		for i, x := range occ {
			if x == id {
				occupants[from] = append(occ[:i], occ[i+1:]...)
				break
			}
		}
		occupants[to] = append(occupants[to], id)
		phase[id] = to
	}
	feasible := func(id string, p int) bool {
		return !hasConflict(byID[id], occupants[p], byID)
	}

	var windows []int
	placed := map[string]bool{}
	for _, id := range ws {
		if placed[id] {
			continue
		}
		chosen := -1
		for _, w := range windows {
			if w >= phase[id] && w <= latest[id] && feasible(id, w) {
				chosen = w
				break
			}
		}
		if chosen == -1 {
			for q := latest[id]; q >= phase[id]; q-- {
				if feasible(id, q) {
					chosen = q
					break
				}
			}
			if chosen == -1 {
				chosen = phase[id] // original placement is always feasible
			}
			windows = append(windows, chosen)
		}
		// Cover every other write-stop step whose interval admits this
		// window phase.
		for _, other := range ws {
			if placed[other] {
				continue
			}
			if phase[other] <= chosen && chosen <= latest[other] && feasible(other, chosen) {
				move(other, chosen)
				placed[other] = true
			}
		}
	}
}

// buildPlan assembles the final plan: compacts phase numbering, flags
// write-stop windows, and locates the irreversibility boundary.
func buildPlan(steps []Step, phase map[string]int, p *Plan) *Plan {
	set := map[int]bool{}
	max := 0
	for _, ph := range phase {
		set[ph] = true
		if ph > max {
			max = ph
		}
	}
	var nums []int
	for n := range set {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	renum := map[int]int{}
	for i, n := range nums {
		renum[n] = i + 1
	}

	byPhase := map[int][]Step{}
	for _, s := range steps {
		np := renum[phase[s.ID]]
		byPhase[np] = append(byPhase[np], s)
	}
	p.Phases = make([]Phase, 0, len(nums))
	boundaryPhase := 0
	var boundaryStep Step
	for i := range nums {
		n := i + 1
		ids := byPhase[n]
		sort.Slice(ids, func(a, b int) bool { return ids[a].ID < ids[b].ID })
		ph := Phase{Index: n}
		for _, s := range ids {
			ps := PlannedStep{
				Step:       s,
				Phase:      n,
				WriteStop:  s.NeedsWriteStop,
				Reversible: !s.Irreversible(),
			}
			if s.NeedsWriteStop {
				ph.WriteStop = true
			}
			if s.Irreversible() && boundaryPhase == 0 {
				boundaryPhase = n
				boundaryStep = s
			}
			ph.Steps = append(ph.Steps, ps)
		}
		if ph.WriteStop {
			p.WriteStopWindows++
		}
		p.Phases = append(p.Phases, ph)
	}
	if boundaryPhase != 0 {
		reason := "no rollback defined"
		if boundaryStep.destructive() {
			reason = "destructive operation"
		}
		p.Boundary = &Boundary{
			Phase:  boundaryPhase,
			StepID: boundaryStep.ID,
			Reason: reason + "; steps from this phase on cannot be fully rolled back",
		}
	}
	return p
}
