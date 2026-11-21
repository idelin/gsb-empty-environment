package handover

import "sort"

// buildRearrangement 构造把选中句柄安装到固定目标槽的操作序列。
//
// 只为选中句柄建立“源槽 -> 目标槽”的功能排列，未选中句柄留在原槽并在
// 最后统一置 cloexec。排列的每个连通分量要么是含初始空目标槽的链（直接
// Move），要么是无空槽的环（借宿主唯一临时槽旋转）。任何写入的目的地在
// 写入时均为空，因此不会覆盖后续仍需读取的源。
func buildRearrangement(bySlot map[int]Handle, tempSlot int, roles []Role, chosenSlots []int) ([]Op, map[int]SlotExpect) {
	selected := make(map[int]struct{}, len(chosenSlots))
	dst := make(map[int]int, len(chosenSlots)) // 选中源槽 -> 角色目标槽
	inv := make(map[int]int, len(chosenSlots)) // 角色目标槽 -> 选中源槽
	roleByTarget := make(map[int]Role, len(roles))

	for i, slot := range chosenSlots {
		selected[slot] = struct{}{}
		dst[slot] = roles[i].Target
		inv[roles[i].Target] = slot
		roleByTarget[roles[i].Target] = roles[i]
	}

	involved := make(map[int]struct{})
	for slot := range bySlot {
		involved[slot] = struct{}{}
	}
	for _, r := range roles {
		involved[r.Target] = struct{}{}
	}

	var ops []Op
	lastPerm := Perm(0)
	haveBound := false
	emitBarrier := func(p Perm) {
		if haveBound && p != lastPerm {
			ops = append(ops, Op{Kind: OpBarrier, Bound: p})
		}
		lastPerm = p
		haveBound = true
	}

	// 第一阶段：以初始空目标槽为根的链。空槽吸收其前驱内容，空位前移。
	var emptyTargets []int
	for target := range inv {
		if _, occupied := bySlot[target]; !occupied {
			emptyTargets = append(emptyTargets, target)
		}
	}
	sort.Ints(emptyTargets)
	consumed := make(map[int]struct{})
	for _, hole := range emptyTargets {
		cur := hole
		for {
			src, ok := inv[cur]
			if !ok {
				break
			}
			if r, ok := roleByTarget[cur]; ok {
				emitBarrier(r.Need)
			}
			ops = append(ops, Op{Kind: OpMove, A: src, B: cur})
			consumed[src] = struct{}{}
			cur = src
		}
	}

	// 第二阶段：剩余选中源构成无空槽的有向环。
	type cycle struct {
		order []int // 沿 dst 方向
	}
	var cycles []cycle
	for src := range dst {
		if _, done := consumed[src]; done {
			continue
		}
		var order []int
		cur := src
		for {
			order = append(order, cur)
			consumed[cur] = struct{}{}
			cur = dst[cur]
			if cur == src {
				break
			}
		}

		cycles = append(cycles, cycle{order: order})
	}
	sort.Slice(cycles, func(i, j int) bool {
		return cycles[i].order[0] < cycles[j].order[0]
	})

	// 互不相交的二元环合并为一次并发交换批次；权限变化或槽位相交则先冲刷。
	var pendingPairs [][2]int
	pendingPerm := Perm(0)
	flushPairs := func() {
		if len(pendingPairs) == 0 {
			return
		}
		if len(pendingPairs) == 1 {
			ops = append(ops, Op{Kind: OpSwap, A: pendingPairs[0][0], B: pendingPairs[0][1]})
		} else {
			ops = append(ops, Op{Kind: OpParSwap, Pairs: append([][2]int(nil), pendingPairs...)})
		}
		pendingPairs = nil
	}

	for _, c := range cycles {
		if len(c.order) == 1 {
			continue
		}
		if len(c.order) == 2 {
			nodes := []int{c.order[0], c.order[1]}
			perm := cyclePairPerm(dst, roleByTarget, c.order)
			if len(pendingPairs) > 0 && (pendingPerm != perm || overlapsPending(pendingPairs, nodes)) {
				flushPairs()
			}
			emitBarrier(perm)
			pendingPairs = append(pendingPairs, [2]int{nodes[0], nodes[1]})
			pendingPerm = perm
			continue
		}
		flushPairs()
		ops = append(ops, rotateCycle(c.order, dst, roleByTarget, tempSlot, emitBarrier)...)
		ops = append(ops, Op{Kind: OpDropTemp, A: tempSlot})
	}
	flushPairs()

	// 第三阶段：cloexec 收敛。选中句柄必须继承；未选中占用槽一律自动关闭。
	emitBarrier(Perm(0))
	finalClo := make(map[int]bool)
	finalSrc := make(map[int]int)
	for src, target := range dst {
		finalClo[target] = false
		finalSrc[target] = src
	}
	var cloSlots []int
	for slot := range involved {
		cloSlots = append(cloSlots, slot)
	}
	sort.Ints(cloSlots)
	for _, slot := range cloSlots {
		if target, isFinalTarget := finalClo[slot]; isFinalTarget {
			if bySlot[finalSrc[slot]].Cloexec != target {
				ops = append(ops, Op{Kind: OpSetCloexec, A: slot, On: target})
			}
			continue
		}
		h, initiallyOccupied := bySlot[slot]
		_, becameSelectedSource := selected[slot]
		if initiallyOccupied && !becameSelectedSource && !h.Cloexec {
			ops = append(ops, Op{Kind: OpSetCloexec, A: slot, On: true})
		}
	}

	expect := make(map[int]SlotExpect)
	for slot := range involved {
		if h, occupied := bySlot[slot]; occupied {
			expect[slot] = SlotExpect{Gen: h.Gen, Object: h.Object}
		} else {
			expect[slot] = SlotExpect{Empty: true}
		}
	}
	expect[tempSlot] = SlotExpect{Empty: true}
	return ops, expect
}

// rotateCycle 用唯一临时槽旋转长度 >= 3 的环，且不覆盖任何待读源。
//
// 标准旋转：Load 起点到临时槽，随后反复把前驱内容移入当前空槽，最后
// Store 临时槽内容到终点。若环上相邻目标槽分属不同权限角色，则在跨越点
// 先 Store 临时槽到当前空槽、发射屏障（临时槽为空），再用一次 Load 从该
// 槽取回，屏障两侧的能力因此不会在临时槽中混留。
func rotateCycle(order []int, dst map[int]int, roleByTarget map[int]Role, tempSlot int, emitBarrier func(Perm)) []Op {
	first := order[0]
	inverse := make(map[int]int, len(order))
	for _, src := range order {
		inverse[dst[src]] = src
	}
	permAt := func(target int) (Perm, bool) {
		r, ok := roleByTarget[target]
		return r.Need, ok
	}

	var ops []Op
	hole := first
	ops = append(ops, Op{Kind: OpLoad, A: tempSlot, B: first})

	for {
		src := inverse[hole]
		if src == first {
			break
		}
		curPerm, curOK := permAt(hole)
		nextPerm, nextOK := permAt(src)
		if curOK && nextOK && curPerm != nextPerm {
			// 临时槽内容回到当前空槽，空槽穿越屏障后再取回，内容归属不变。
			ops = append(ops, Op{Kind: OpStore, A: tempSlot, B: hole})
			ops = append(ops, Op{Kind: OpBarrier, Bound: nextPerm})
			ops = append(ops, Op{Kind: OpLoad, A: tempSlot, B: hole})
		} else if curOK {
			emitBarrier(curPerm)
		}
		ops = append(ops, Op{Kind: OpMove, A: src, B: hole})
		hole = src
	}
	if p, ok := permAt(hole); ok {
		emitBarrier(p)
	}
	ops = append(ops, Op{Kind: OpStore, A: tempSlot, B: hole})
	return ops
}

// cyclePairPerm 返回二元环交换涉及的角色权限；两个目标权限必须相同才能
// 归入同一并发批次（不同权限的交换分别冲刷）。
func cyclePairPerm(dst map[int]int, roleByTarget map[int]Role, order []int) Perm {
	for _, src := range order {
		if r, ok := roleByTarget[dst[src]]; ok {
			return r.Need
		}
	}
	return 0
}

func overlapsPending(pairs [][2]int, nodes []int) bool {
	for _, p := range pairs {
		for _, n := range nodes {
			if n == p[0] || n == p[1] {
				return true
			}
		}
	}
	return false
}
