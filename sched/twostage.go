package sched

import (
	"sort"
	"strconv"
	"strings"
)

// Scenario is one possible world. Profits gives the per-candidate profit in
// this world; Observe gives the value a candidate reveals when it runs in
// stage 1. The planner only learns the observation vector of its stage-1
// experiments, never the scenario id directly.
type Scenario struct {
	ID      string         `json:"id"`
	Profits map[string]int `json:"profits"`
	Observe map[string]int `json:"observe"`
}

// TwoStageInstance splits the timeline at Stage1End: candidates with
// Slot <= Stage1End are stage-1 eligible, the rest stage-2 eligible.
// Cooldown and mutex rules apply to the union of both stages.
type TwoStageInstance struct {
	Cooldown   int         `json:"cooldown"`
	Stage1End  int         `json:"stage1_end"`
	Candidates []Candidate `json:"candidates"`
	Scenarios  []Scenario  `json:"scenarios"`
}

// Strategy is a policy tree: a fixed stage-1 set, plus one stage-2 set per
// observation key reachable from it. Because stage-2 branches are keyed by
// observation, the strategy structurally cannot act on unobservable
// information.
type Strategy struct {
	Stage1 []string            `json:"stage1"`
	Stage2 map[string][]string `json:"stage2"`
	Worst  int                 `json:"worst"`
}

func (inst *TwoStageInstance) legalityInstance() *Instance {
	return &Instance{Cooldown: inst.Cooldown, Candidates: inst.Candidates}
}

func (inst *TwoStageInstance) eligible() (stage1, stage2 []int) {
	for i, c := range inst.Candidates {
		if c.Slot <= inst.Stage1End {
			stage1 = append(stage1, i)
		} else {
			stage2 = append(stage2, i)
		}
	}
	return stage1, stage2
}

// obsKey is what the planner observes after stage 1 under scenario sc: the
// revealed values of the stage-1 experiments, keyed by sorted id.
func obsKey(inst *TwoStageInstance, sc *Scenario, s1 []int) string {
	ids := make([]string, 0, len(s1))
	for _, ci := range s1 {
		ids = append(ids, inst.Candidates[ci].ID)
	}
	sort.Strings(ids)
	var sb strings.Builder
	for _, id := range ids {
		sb.WriteString(id)
		sb.WriteByte('=')
		sb.WriteString(strconv.Itoa(sc.Observe[id]))
		sb.WriteByte(';')
	}
	return sb.String()
}

// enumLegal lists every subset of pool whose union with base is legal under
// time replay. Returned slices contain indexes into inst.Candidates and
// exclude the base elements.
func enumLegal(inst *Instance, base, pool []int) [][]int {
	var out [][]int
	chosen := append([]int(nil), base...)
	var rec func(k int)
	rec = func(k int) {
		if k == len(pool) {
			if ReplayLegal(inst, chosen) {
				cp := append([]int(nil), chosen[len(base):]...)
				out = append(out, cp)
			}
			return
		}
		rec(k + 1)
		chosen = append(chosen, pool[k])
		rec(k + 1)
		chosen = chosen[:len(chosen)-1]
	}
	rec(0)
	return out
}

func betterStrategy(worstA int, s1A []string, worstB int, s1B []string) bool {
	if worstA != worstB {
		return worstA > worstB
	}
	if len(s1A) != len(s1B) {
		return len(s1A) < len(s1B)
	}
	for i := range s1A {
		if s1A[i] != s1B[i] {
			return s1A[i] < s1B[i]
		}
	}
	return false
}

const negInf = -1 << 30

// SolveTwoStage finds the strategy tree maximizing the worst-case total
// profit across scenarios. For each legal stage-1 set it partitions the
// scenarios by observation vector; each observation group is then an
// independent subproblem whose stage-2 set maximizes the worst profit
// inside the group. Ties favor fewer stage-1 experiments, then the
// lexicographically smaller stage-1 id list; stage-2 ties use the shared
// tie-break rules.
func SolveTwoStage(inst *TwoStageInstance) Strategy {
	leg := inst.legalityInstance()
	e1, e2 := inst.eligible()

	bestWorst := negInf
	best := Strategy{Stage1: []string{}, Stage2: map[string][]string{}, Worst: negInf}

	for _, s1 := range enumLegal(leg, nil, e1) {
		p1 := make([]int, len(inst.Scenarios))
		for si := range inst.Scenarios {
			for _, ci := range s1 {
				p1[si] += inst.Scenarios[si].Profits[inst.Candidates[ci].ID]
			}
		}
		groups := map[string][]int{}
		keys := []string{}
		for si := range inst.Scenarios {
			k := obsKey(inst, &inst.Scenarios[si], s1)
			if _, ok := groups[k]; !ok {
				keys = append(keys, k)
			}
			groups[k] = append(groups[k], si)
		}
		sort.Strings(keys)

		stage2 := map[string][]string{}
		worst := 0
		for gi, k := range keys {
			gs := groups[k]
			groupBest := negInf
			var groupBestIDs []string
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
				ids := idsOf(leg, s2)
				if groupBest == negInf || better(val, ids, groupBest, groupBestIDs) {
					groupBest = val
					groupBestIDs = ids
				}
			}
			stage2[k] = groupBestIDs
			if gi == 0 || groupBest < worst {
				worst = groupBest
			}
		}

		s1ids := idsOf(leg, s1)
		if betterStrategy(worst, s1ids, bestWorst, best.Stage1) {
			bestWorst = worst
			best = Strategy{Stage1: s1ids, Stage2: stage2, Worst: worst}
		}
	}
	return best
}
