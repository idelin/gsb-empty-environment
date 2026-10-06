package sched

import "fmt"

func indexByID(inst *TwoStageInstance) (map[string]int, error) {
	idx := make(map[string]int, len(inst.Candidates))
	for i, c := range inst.Candidates {
		if _, dup := idx[c.ID]; dup {
			return nil, fmt.Errorf("duplicate candidate id %q", c.ID)
		}
		idx[c.ID] = i
	}
	return idx, nil
}

// VerifyStrategy replays every scenario path through the strategy tree and
// confirms the strategy is legal, causal (no clairvoyance), and that the
// claimed worst-case profit is exact. Concretely it checks, per scenario:
// stage-1 and stage-2 sets are eligible and disjoint, the combined timeline
// passes time replay, and the scenario is routed solely by its stage-1
// observation vector — two scenarios producing the same observation are
// forced onto the same stage-2 branch because routing is a pure function of
// the observation key.
func VerifyStrategy(inst *TwoStageInstance, st Strategy) error {
	leg := inst.legalityInstance()
	e1, e2 := inst.eligible()
	inE1 := make(map[int]bool, len(e1))
	for _, i := range e1 {
		inE1[i] = true
	}
	inE2 := make(map[int]bool, len(e2))
	for _, i := range e2 {
		inE2[i] = true
	}
	idx, err := indexByID(inst)
	if err != nil {
		return err
	}
	resolve := func(ids []string) ([]int, error) {
		out := make([]int, 0, len(ids))
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				return nil, fmt.Errorf("duplicate id %q in strategy", id)
			}
			seen[id] = true
			i, ok := idx[id]
			if !ok {
				return nil, fmt.Errorf("unknown candidate id %q", id)
			}
			out = append(out, i)
		}
		return out, nil
	}

	s1idx, err := resolve(st.Stage1)
	if err != nil {
		return fmt.Errorf("stage1: %v", err)
	}
	for _, i := range s1idx {
		if !inE1[i] {
			return fmt.Errorf("stage1: candidate %q (slot %d) is not stage-1 eligible", inst.Candidates[i].ID, inst.Candidates[i].Slot)
		}
	}
	if !ReplayLegal(leg, s1idx) {
		return fmt.Errorf("stage1 set %v fails timeline replay", st.Stage1)
	}

	worst := 0
	for si := range inst.Scenarios {
		sc := &inst.Scenarios[si]
		key := obsKey(inst, sc, s1idx)
		s2ids, ok := st.Stage2[key]
		if !ok {
			return fmt.Errorf("scenario %s: observation %q has no stage-2 branch; serving it would require knowing the future", sc.ID, key)
		}
		s2idx, err := resolve(s2ids)
		if err != nil {
			return fmt.Errorf("scenario %s branch: %v", sc.ID, err)
		}
		combined := append(append([]int(nil), s1idx...), s2idx...)
		for _, i := range s2idx {
			if !inE2[i] {
				return fmt.Errorf("scenario %s: candidate %q (slot %d) is not stage-2 eligible", sc.ID, inst.Candidates[i].ID, inst.Candidates[i].Slot)
			}
		}
		if !ReplayLegal(leg, combined) {
			return fmt.Errorf("scenario %s: combined plan fails timeline replay (cooldown/mutex across stages)", sc.ID)
		}
		profit := 0
		for _, i := range combined {
			profit += sc.Profits[inst.Candidates[i].ID]
		}
		if si == 0 || profit < worst {
			worst = profit
		}
	}
	if worst != st.Worst {
		return fmt.Errorf("claimed worst %d, replayed worst %d", st.Worst, worst)
	}
	return nil
}

// BruteTwoStage recomputes the optimal worst-case profit with an
// independent naive enumeration: every legal stage-1 set, and per
// observation group every legal stage-2 set. Used to cross-check
// SolveTwoStage.
func BruteTwoStage(inst *TwoStageInstance) int {
	leg := inst.legalityInstance()
	e1, e2 := inst.eligible()
	best := negInf
	for _, s1 := range enumLegal(leg, nil, e1) {
		p1 := make([]int, len(inst.Scenarios))
		for si := range inst.Scenarios {
			for _, ci := range s1 {
				p1[si] += inst.Scenarios[si].Profits[inst.Candidates[ci].ID]
			}
		}
		groups := map[string][]int{}
		for si := range inst.Scenarios {
			k := obsKey(inst, &inst.Scenarios[si], s1)
			groups[k] = append(groups[k], si)
		}
		worst := 0
		gi := 0
		for _, gs := range groups {
			groupBest := negInf
			for _, s2 := range enumLegal(leg, s1, e2) {
				val := 0
				for i, si := range gs {
					p := p1[si]
					for _, ci := range s2 {
						p += inst.Scenarios[si].Profits[inst.Candidates[ci].ID]
					}
					if i == 0 || p < val {
						val = p
					}
				}
				if val > groupBest {
					groupBest = val
				}
			}
			if gi == 0 || groupBest < worst {
				worst = groupBest
			}
			gi++
		}
		if worst > best {
			best = worst
		}
	}
	return best
}

// ClairvoyantBound is an upper bound on any causal strategy: the minimum
// over scenarios of the profit an oracle that knows the scenario could get.
func ClairvoyantBound(inst *TwoStageInstance) int {
	leg := inst.legalityInstance()
	all := make([]int, len(inst.Candidates))
	for i := range all {
		all[i] = i
	}
	bound := 0
	for si := range inst.Scenarios {
		sc := &inst.Scenarios[si]
		best := negInf
		for _, sub := range enumLegal(leg, nil, all) {
			p := 0
			for _, ci := range sub {
				p += sc.Profits[inst.Candidates[ci].ID]
			}
			if p > best {
				best = p
			}
		}
		if si == 0 || best < bound {
			bound = best
		}
	}
	return bound
}
