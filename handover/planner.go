package handover

import (
	"fmt"
	"sort"
)

// BuildPlan 在给定句柄表上为全部角色计算最优交接计划。
//
// 优化目标（按优先级）：
//  1. 满足全部角色的对象身份、精确权限与共享约束；
//  2. 去重后的源对象数量最少；
//  3. 在角色顺序下槽号序列字典序最小。
//
// tempSlot 是宿主提供的唯一一个临时槽，规划阶段它必须为空。
func BuildPlan(tab *Table, roles []Role, tempSlot int) (*Plan, error) {
	if tab == nil {
		return nil, fmt.Errorf("%w: nil table", ErrInvalidInput)
	}
	if len(roles) == 0 {
		return nil, fmt.Errorf("%w: no roles declared", ErrInvalidInput)
	}

	bySlot := tab.BySlot()
	if _, clash := bySlot[tempSlot]; clash {
		return nil, fmt.Errorf("%w: temp slot %d must be empty", ErrInvalidInput, tempSlot)
	}

	targets := make(map[int]string, len(roles))
	for i := range roles {
		r := &roles[i]
		if r.Name == "" {
			return nil, fmt.Errorf("%w: role %d has empty name", ErrInvalidInput, i)
		}
		if r.Allowed == nil || r.Share == nil {
			return nil, fmt.Errorf("%w: role %s missing allowed/share sets", ErrInvalidInput, r.Name)
		}
		if _, ok := targets[r.Target]; ok {
			return nil, fmt.Errorf("%w: duplicate target slot %d", ErrInvalidInput, r.Target)
		}
		targets[r.Target] = r.Name
	}

	// 每个角色的候选句柄按槽号升序，DFS 因此天然按字典序探索。
	candidates := make([][]int, len(roles))
	for i := range roles {
		r := &roles[i]
		var cands []int
		for _, h := range tab.Handles {
			if !r.allowsObject(h.Object) {
				continue
			}
			// 精确权限：缺权限或带额外权限（如可写别名）都不可交接。
			if h.Perm != r.Need {
				continue
			}
			cands = append(cands, h.Slot)
		}
		sort.Ints(cands)
		if len(cands) == 0 {
			return nil, fmt.Errorf("%w: role %s has no eligible handle", ErrNoAssignment, r.Name)
		}
		candidates[i] = cands
	}

	s := &search{
		roles:      roles,
		candidates: candidates,
		bySlot:     bySlot,
		bestCount:  int(^uint(0) >> 1),
	}
	if !s.run(0, make([]int, len(roles)), map[int]struct{}{}, map[ObjectID]struct{}{}) {
		return nil, ErrNoAssignment
	}

	assignments := make([]Assignment, len(roles))
	for i, slot := range s.bestSlots {
		h := bySlot[slot]
		assignments[i] = Assignment{
			RoleName: roles[i].Name,
			Slot:     slot,
			Object:   h.Object,
			Perm:     h.Perm,
			Target:   roles[i].Target,
		}
	}

	ops, expect := buildRearrangement(bySlot, tempSlot, roles, s.bestSlots)

	usedObjs := make(map[ObjectID]struct{})
	var objOrder []ObjectID
	for _, slot := range s.bestSlots {
		id := bySlot[slot].Object
		if _, ok := usedObjs[id]; !ok {
			usedObjs[id] = struct{}{}
			objOrder = append(objOrder, id)
		}
	}

	return &Plan{
		TempSlot:    tempSlot,
		Assignments: assignments,
		Ops:         ops,
		Expect:      expect,
		UsedSlots:   append([]int(nil), s.bestSlots...),
		UsedObjects: objOrder,
	}, nil
}

type search struct {
	roles      []Role
	candidates [][]int
	bySlot     map[int]Handle

	bestSlots []int
	bestCount int
}

func (s *search) run(depth int, chosen []int, usedSlots map[int]struct{}, usedObjs map[ObjectID]struct{}) bool {
	if len(usedObjs) > s.bestCount {
		return false // 已不可能优于当前最优对象数
	}
	if depth == len(s.roles) {
		if len(usedObjs) < s.bestCount {
			s.bestCount = len(usedObjs)
			s.bestSlots = append([]int(nil), chosen...)
			return true
		}
		// 对象数相同：候选按槽号升序，首次命中的字典序最小。
		return false
	}

	role := &s.roles[depth]
	found := false
	for _, slot := range s.candidates[depth] {
		if _, taken := usedSlots[slot]; taken {
			continue
		}
		h := s.bySlot[slot]

		// 共享校验：同一对象只能出现在互相允许共享的角色之间。
		if _, shared := usedObjs[h.Object]; shared {
			ok := true
			for prev := 0; prev < depth; prev++ {
				if s.bySlot[chosen[prev]].Object != h.Object {
					continue
				}
				if !role.allowsShare(s.roles[prev].Name) || !s.roles[prev].allowsShare(role.Name) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
		}

		chosen[depth] = slot
		usedSlots[slot] = struct{}{}
		_, objWasUsed := usedObjs[h.Object]
		usedObjs[h.Object] = struct{}{}

		if s.run(depth+1, chosen, usedSlots, usedObjs) {
			found = true
		}

		delete(usedSlots, slot)
		if !objWasUsed {
			delete(usedObjs, h.Object)
		}
	}
	return found
}
