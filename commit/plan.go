package commit

import "sort"

// BuildPlan 求解并展开为可直接驱动物理接口的读、释放、写序列。
// 规划期间只读取 Instance 描述，绝不访问物理设备。
func BuildPlan(inst *Instance) (*Plan, error) {
	e, err := newEngine(inst)
	if err != nil {
		return nil, err
	}
	goal, best, err := e.search()
	if err != nil {
		return nil, err
	}

	trace := e.trace(best, goal)
	ops := e.materialize(trace)

	// 重新汇总物理序列的指标，与 Dijkstra 标签互相印证。
	var cost int64
	reads := 0
	head := inst.Initial
	for i := range ops {
		switch ops[i].Kind {
		case "read":
			cost += inst.ReadCost(head, ops[i].Cyl)
			reads++
			head = ops[i].Cyl
		case "write":
			head = ops[i].Cyl
		}
	}
	if cost != goal.label[0] || int64(reads) != goal.label[1] {
		return nil, &InfeasibleError{Reason: "internal metric mismatch"}
	}
	return &Plan{Ops: ops, ReadCost: cost, Reads: reads}, nil
}

// materialize 把宏动作展开成物理动作：每次写后释放不再被需要的缓存副本，
// 全部写完后释放剩余槽位。释放免费且不移动磁头。
func (e *engine) materialize(trace []traceAction) []Op {
	inst := e.inst
	var ops []Op
	resident := map[int]bool{}
	var w uint16
	releaseDead := func() {
		live := e.live[w]
		dead := make([]int, 0)
		for s := range resident {
			if live&(1<<uint(s)) == 0 {
				dead = append(dead, s)
			}
		}
		sort.Ints(dead)
		for _, s := range dead {
			ops = append(ops, Op{Kind: "release", Sector: e.sourceID[s], Cyl: e.sourceCyl[s]})
			delete(resident, s)
		}
	}

	for _, a := range trace {
		switch a.kind {
		case actRead:
			ops = append(ops, Op{Kind: "read", Sector: e.sourceID[a.idx], Cyl: e.sourceCyl[a.idx]})
			resident[a.idx] = true
		case actWrite:
			tgt := &inst.Targets[a.idx]
			ops = append(ops, Op{Kind: "write", Sector: tgt.Address, Cyl: tgt.Sector})
			w |= 1 << uint(a.idx)
			releaseDead()
		case actFree:
			ops = append(ops, Op{Kind: "release", Sector: e.sourceID[a.idx], Cyl: e.sourceCyl[a.idx]})
			delete(resident, a.idx)
		}
	}
	tail := make([]int, 0, len(resident))
	for s := range resident {
		tail = append(tail, s)
	}
	sort.Ints(tail)
	for _, s := range tail {
		ops = append(ops, Op{Kind: "release", Sector: e.sourceID[s], Cyl: e.sourceCyl[s]})
	}
	return ops
}
