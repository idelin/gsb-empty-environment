package planner

import (
	"fmt"
	"sort"
	"strings"
)

// CriterionPlan is the solver's per-criterion result.
type CriterionPlan struct {
	CriterionID string
	Satisfied   bool
	Term        *Term  // chosen satisfaction path, when satisfied
	Reason      string // why unsatisfied, when not
}

// Plan is the full solver output.
type Plan struct {
	Feasible    bool
	TotalCost   float64
	Evidence    []string // sorted ids of collected evidence
	Criteria    []CriterionPlan
	ExpiredSkip []string // evidence ignored because expired at as-of
}

// termOption is a feasible term annotated with cost.
type termOption struct {
	term Term
	cost float64 // sum of costs of required evidence
	size int
}

// Solve finds the minimum-cost evidence plan satisfying every criterion.
func Solve(inst *Instance) *Plan {
	plan := &Plan{}

	// Expired evidence is excluded from the universe entirely.
	usable := make(map[string]bool)
	for _, id := range inst.Order {
		if inst.Evidence[id].Valid(inst.AsOf) {
			usable[id] = true
		} else {
			plan.ExpiredSkip = append(plan.ExpiredSkip, id)
		}
	}
	sort.Strings(plan.ExpiredSkip)

	// Build feasible term options per criterion.
	options := make([][]termOption, len(inst.Criteria))
	individual := make([]error, len(inst.Criteria))
	for i, c := range inst.Criteria {
		opts, err := feasibleTerms(inst, c, usable)
		options[i] = opts
		individual[i] = err
	}

	s := &search{
		inst:    inst,
		options: options,
		assign:  make([]int, len(inst.Criteria)),
	}
	// Visit criteria with fewest options first for stronger pruning.
	s.order = make([]int, len(inst.Criteria))
	for i := range s.order {
		s.order[i] = i
	}
	sort.SliceStable(s.order, func(a, b int) bool {
		return len(options[s.order[a]]) < len(options[s.order[b]])
	})
	for i := range s.assign {
		s.assign[i] = -1
	}
	s.collected = make(map[string]bool)
	s.forbidden = make(map[string]bool)
	s.bestCost = -1

	s.dfs(0)

	if s.bestAssign == nil {
		plan.Feasible = false
		for i, c := range inst.Criteria {
			cp := CriterionPlan{CriterionID: c.ID}
			if individual[i] != nil {
				cp.Reason = individual[i].Error()
			} else {
				cp.Reason = "satisfiable on its own, but every satisfaction path conflicts with the requirements of other criteria (mutex exclusions, NOT constraints, or the evidence limit)"
			}
			plan.Criteria = append(plan.Criteria, cp)
		}
		return plan
	}

	plan.Feasible = true
	plan.TotalCost = s.bestCost
	for id := range s.bestCollected {
		plan.Evidence = append(plan.Evidence, id)
	}
	sort.Strings(plan.Evidence)
	for i, c := range inst.Criteria {
		t := options[i][s.bestAssign[i]].term
		plan.Criteria = append(plan.Criteria, CriterionPlan{
			CriterionID: c.ID,
			Satisfied:   true,
			Term:        &t,
		})
	}
	return plan
}

// feasibleTerms lists the individually usable satisfaction paths of one
// criterion, sorted cheapest first. The error explains why none is usable.
func feasibleTerms(inst *Instance, c *CompiledCriterion, usable map[string]bool) ([]termOption, error) {
	raw := DedupeTerms(Terms(c.Root))
	if len(raw) == 0 {
		return nil, fmt.Errorf("expression is self-contradictory (no assignment can satisfy it)")
	}
	var opts []termOption
	var expiredSets, mutexPairs []string
	minSize := -1
	for _, t := range raw {
		var expired []string
		for id := range t.Require {
			if !usable[id] {
				expired = append(expired, id)
			}
		}
		if len(expired) > 0 {
			sort.Strings(expired)
			expiredSets = append(expiredSets, strings.Join(expired, ", "))
			continue
		}
		if x, y, ok := inst.Conflicts(t.Require, t.Require); ok {
			mutexPairs = append(mutexPairs, x+" ↔ "+y)
			continue
		}
		if minSize == -1 || len(t.Require) < minSize {
			minSize = len(t.Require)
		}
		var cost float64
		for id := range t.Require {
			cost += inst.Evidence[id].Ev.Cost
		}
		opts = append(opts, termOption{term: t, cost: cost, size: len(t.Require)})
	}
	if len(opts) > 0 {
		sort.SliceStable(opts, func(a, b int) bool {
			if opts[a].cost != opts[b].cost {
				return opts[a].cost < opts[b].cost
			}
			return opts[a].size < opts[b].size
		})
		return opts, nil
	}
	switch {
	case len(expiredSets) > 0 && len(mutexPairs) == 0:
		return nil, fmt.Errorf("no usable satisfaction path: every path requires evidence expired at %s (%s)",
			inst.AsOf.Format(dateLayout), strings.Join(expiredSets, "; "))
	case len(mutexPairs) > 0 && len(expiredSets) == 0:
		return nil, fmt.Errorf("no usable satisfaction path: every path needs mutually exclusive evidence together (%s)",
			strings.Join(mutexPairs, "; "))
	default:
		return nil, fmt.Errorf("no usable satisfaction path: expired evidence (%s) or internal mutex conflicts (%s)",
			strings.Join(expiredSets, "; "), strings.Join(mutexPairs, "; "))
	}
}

type search struct {
	inst      *Instance
	options   [][]termOption
	order     []int
	assign    []int
	collected map[string]bool
	forbidden map[string]bool
	cost      float64

	bestCost      float64
	bestAssign    []int
	bestCollected map[string]bool
}

const eps = 1e-9

func (s *search) dfs(depth int) {
	if s.bestCost >= 0 && s.cost >= s.bestCost-eps {
		return
	}
	if depth == len(s.order) {
		s.bestCost = s.cost
		s.bestAssign = append([]int(nil), s.assign...)
		s.bestCollected = make(map[string]bool, len(s.collected))
		for id := range s.collected {
			s.bestCollected[id] = true
		}
		return
	}
	ci := s.order[depth]
	for ti, opt := range s.options[ci] {
		t := opt.term
		if !s.compatible(t) {
			continue
		}
		added := s.apply(t)
		s.assign[ci] = ti
		s.dfs(depth + 1)
		s.assign[ci] = -1
		s.undo(t, added)
	}
}

// compatible checks a term against the current partial plan.
func (s *search) compatible(t Term) bool {
	for id := range t.Require {
		if s.forbidden[id] {
			return false
		}
	}
	for id := range t.Forbid {
		if s.collected[id] {
			return false
		}
	}
	if _, _, ok := s.inst.Conflicts(t.Require, s.collected); ok {
		return false
	}
	merged := len(s.collected)
	for id := range t.Require {
		if !s.collected[id] {
			merged++
		}
	}
	if merged > s.inst.MaxEvidence {
		return false
	}
	return true
}

// apply merges a term into the partial plan, returning what was added.
func (s *search) apply(t Term) (added []string) {
	for id := range t.Require {
		if !s.collected[id] {
			s.collected[id] = true
			s.cost += s.inst.Evidence[id].Ev.Cost
			added = append(added, id)
		}
	}
	for id := range t.Forbid {
		if !s.forbidden[id] {
			s.forbidden[id] = true
			added = append(added, "!"+id)
		}
	}
	return added
}

func (s *search) undo(t Term, added []string) {
	for _, id := range added {
		if strings.HasPrefix(id, "!") {
			delete(s.forbidden, id[1:])
		} else {
			delete(s.collected, id)
			s.cost -= s.inst.Evidence[id].Ev.Cost
		}
	}
}
