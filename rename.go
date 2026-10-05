// Package rename plans minimal-temporary-name file rename sequences.
//
// All source and target paths are interpreted against the initial state of
// the directory: a move a -> b means "the file currently named a ends up
// named b", even if b itself is also being renamed away.
package rename

import (
	"fmt"
	"sort"
)

// Move describes a single rename from From to To.
type Move struct {
	From string
	To   string
}

// Step is one atomic rename in an execution plan.
type Step struct {
	From string
	To   string
	Temp bool // To is a temporary name introduced by the planner
}

// Plan is an ordered sequence of renames. Temps lists every temporary
// name the plan introduces; there is exactly one per cycle, which is the
// minimum possible.
type Plan struct {
	Steps []Step
	Temps []string
}

// PlanMoves builds an execution plan for moves. exists reports whether a
// path is occupied in the initial directory.
//
// It returns an error if a source is missing, if two moves share a source
// or a target, or if a target is occupied by a file that does not
// participate in the rename set. Self-renames (a -> a) are no-ops.
func PlanMoves(moves []Move, exists func(string) bool) (*Plan, error) {
	fromTo := make(map[string]string, len(moves))
	for _, m := range moves {
		if m.From == "" || m.To == "" {
			return nil, fmt.Errorf("rename: empty path in move %q -> %q", m.From, m.To)
		}
		if m.From == m.To {
			continue // self-rename is a no-op
		}
		if prev, dup := fromTo[m.From]; dup {
			return nil, fmt.Errorf("rename: source %q used twice (-> %q and -> %q)", m.From, prev, m.To)
		}
		fromTo[m.From] = m.To
	}
	targetOf := make(map[string]string, len(fromTo)) // to -> from
	for from, to := range fromTo {
		if prev, dup := targetOf[to]; dup {
			return nil, fmt.Errorf("rename: target %q claimed by both %q and %q", to, prev, from)
		}
		targetOf[to] = from
	}
	for from, to := range fromTo {
		if !exists(from) {
			return nil, fmt.Errorf("rename: source %q does not exist", from)
		}
		if _, moved := fromTo[to]; !moved && exists(to) {
			return nil, fmt.Errorf("rename: target %q occupied by a file not part of the rename set", to)
		}
	}

	plan := &Plan{}
	visited := make(map[string]bool, len(fromTo))

	tempTaken := func(name string) bool {
		if exists(name) {
			return true
		}
		if _, ok := fromTo[name]; ok {
			return true
		}
		if _, ok := targetOf[name]; ok {
			return true
		}
		for _, t := range plan.Temps {
			if t == name {
				return true
			}
		}
		return false
	}
	newTemp := func() string {
		for i := 0; ; i++ {
			name := fmt.Sprintf(".rename-tmp-%d-%d", len(plan.Temps), i)
			if !tempTaken(name) {
				plan.Temps = append(plan.Temps, name)
				return name
			}
		}
	}

	// Chains: sources that nobody targets start a chain. Executing a chain
	// back to front needs no temporary name.
	var heads []string
	for from := range fromTo {
		if _, targeted := targetOf[from]; !targeted {
			heads = append(heads, from)
		}
	}
	sort.Strings(heads)
	for _, h := range heads {
		var chain []Step
		for cur := h; ; {
			to, ok := fromTo[cur]
			if !ok {
				break
			}
			chain = append(chain, Step{From: cur, To: to})
			visited[cur] = true
			cur = to
		}
		for i := len(chain) - 1; i >= 0; i-- {
			plan.Steps = append(plan.Steps, chain[i])
		}
	}

	// Everything left has in-degree 1, so it partitions into cycles. Each
	// cycle needs exactly one temporary name.
	var starts []string
	for from := range fromTo {
		if !visited[from] {
			starts = append(starts, from)
		}
	}
	sort.Strings(starts)
	for _, s := range starts {
		if visited[s] {
			continue
		}
		var cyc []string
		for cur := s; !visited[cur]; cur = fromTo[cur] {
			visited[cur] = true
			cyc = append(cyc, cur)
		}
		tmp := newTemp()
		plan.Steps = append(plan.Steps, Step{From: cyc[0], To: tmp, Temp: true})
		for i := len(cyc) - 1; i >= 1; i-- {
			plan.Steps = append(plan.Steps, Step{From: cyc[i], To: fromTo[cyc[i]]})
		}
		plan.Steps = append(plan.Steps, Step{From: tmp, To: fromTo[cyc[0]]})
	}
	return plan, nil
}
