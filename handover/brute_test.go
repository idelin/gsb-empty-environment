package handover

import "sort"

// bruteSolution 是独立于规划器的穷举参考结果。
type bruteSolution struct {
	slots       []int
	objectCount int
	feasible    bool
}

// bruteForce 枚举所有角色-槽位组合，独立求出最少源对象数与字典序最小槽序列。
func bruteForce(tab *Table, roles []Role) bruteSolution {
	bySlot := tab.BySlot()
	cands := make([][]int, len(roles))
	for i := range roles {
		for _, h := range tab.Handles {
			if !roles[i].allowsObject(h.Object) || h.Perm != roles[i].Need {
				continue
			}
			cands[i] = append(cands[i], h.Slot)
		}
		sort.Ints(cands[i])
		if len(cands[i]) == 0 {
			return bruteSolution{feasible: false}
		}
	}

	best := bruteSolution{feasible: false, objectCount: int(^uint(0) >> 1)}
	chosen := make([]int, len(roles))

	var visit func(depth int)
	visit = func(depth int) {
		if depth == len(roles) {
			objs := map[ObjectID]struct{}{}
			for _, s := range chosen {
				objs[bySlot[s].Object] = struct{}{}
			}
			n := len(objs)
			if !best.feasible || n < best.objectCount ||
				(n == best.objectCount && lexLess(chosen, best.slots)) {
				best.feasible = true
				best.objectCount = n
				best.slots = append([]int(nil), chosen...)
			}
			return
		}
		for _, slot := range cands[depth] {
			used := false
			for _, s := range chosen[:depth] {
				if s == slot {
					used = true
					break
				}
			}
			if used {
				continue
			}
			h := bySlot[slot]
			shareOK := true
			for prev := 0; prev < depth; prev++ {
				if bySlot[chosen[prev]].Object != h.Object {
					continue
				}
				if !roles[depth].allowsShare(roles[prev].Name) ||
					!roles[prev].allowsShare(roles[depth].Name) {
					shareOK = false
					break
				}
			}
			if !shareOK {
				continue
			}
			chosen[depth] = slot
			visit(depth + 1)
		}
	}
	visit(0)
	return best
}

func lexLess(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
