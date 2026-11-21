package commit

// 本文件是与主规划器完全独立的“小图枚举”核对器：
// 同一套物理规则用另一种状态模型重新实现，显式建模读、写、释放三种边，
// 不做任何支配剪枝，只跑精确 Dijkstra。仅供测试交叉核对双指标使用。

type enumState struct {
	written  uint16
	resident uint16
	head     Cylinder
}

type enumLabel struct {
	cost  int64
	reads int64
}

func lexLE(a, b enumLabel) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	return a.reads <= b.reads
}

// Enumerate 用独立小图枚举返回 (可行性, 最小总读费用, 最少读次数)。
// 它显式枚举每个状态下所有合法的读/写/释放动作，包含主规划器隐式处理的释放边。
func Enumerate(inst *Instance) (feasible bool, readCost int64, reads int64, err error) {
	e, err := newEngine(inst)
	if err != nil {
		if _, ok := err.(*InfeasibleError); ok {
			return false, 0, 0, nil
		}
		return false, 0, 0, err
	}

	start := enumState{head: inst.Initial}
	best := map[enumState]enumLabel{start: {}}
	type queueItem struct {
		st    enumState
		label enumLabel
	}
	queue := []queueItem{{st: start}}

	pop := func() queueItem {
		bestIdx := 0
		for i := 1; i < len(queue); i++ {
			if lexLE(queue[i].label, queue[bestIdx].label) {
				bestIdx = i
			}
		}
		it := queue[bestIdx]
		queue[bestIdx] = queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		return it
	}

	relax := func(st enumState, label enumLabel) {
		if old, ok := best[st]; ok && !(lexLE(label, old) && label != old) {
			return
		}
		best[st] = label
		queue = append(queue, queueItem{st: st, label: label})
	}

	for len(queue) > 0 {
		it := pop()
		if cur, ok := best[it.st]; !ok || cur != it.label {
			continue
		}
		st := it.st
		if st.written == e.allW && st.resident == 0 {
			return true, it.label.cost, it.label.reads, nil
		}

		// 写边：全部引用源同时驻留。
		for t := 0; t < e.nTargets; t++ {
			bit := uint16(1 << uint(t))
			if st.written&bit != 0 || st.resident&e.targetReq[t] != e.targetReq[t] {
				continue
			}
			relax(enumState{
				written:  st.written | bit,
				resident: st.resident, // 显式模型：缓存保留，由释放边清理
				head:     e.targetCyl[t],
			}, it.label)
		}

		// 读边：未覆盖、未驻留、仍被需要，且有空槽。
		for s := 0; s < e.nSources; s++ {
			bit := uint16(1 << uint(s))
			if st.resident&bit != 0 || e.live[st.written]&bit == 0 ||
				e.overwritten[st.written]&bit != 0 || popcount16(st.resident) >= inst.K {
				continue
			}
			cost := inst.ReadCost(st.head, e.sourceCyl[s])
			relax(enumState{
				written:  st.written,
				resident: st.resident | bit,
				head:     e.sourceCyl[s],
			}, enumLabel{cost: it.label.cost + cost, reads: it.label.reads + 1})
		}

		// 释放边：免费、不动磁头。主规划器把它隐式规范化，这里显式枚举以独立核对。
		for s := 0; s < e.nSources; s++ {
			bit := uint16(1 << uint(s))
			if st.resident&bit == 0 {
				continue
			}
			relax(enumState{
				written:  st.written,
				resident: st.resident ^ bit,
				head:     st.head,
			}, it.label)
		}
	}
	return false, 0, 0, nil
}
