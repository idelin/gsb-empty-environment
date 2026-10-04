package main

import (
	"container/heap"
	"fmt"
)

// timeCost 是某 (节点, 电量) 状态下 Pareto 前沿中的一条标签。
type timeCost struct {
	time int
	cost int
}

// label 是搜索队列中的一条完整标签，带父指针用于路径回溯。
type label struct {
	node    string
	battery int
	time    int
	cost    int
	parent  int // labels 中的下标，-1 表示根
	action  string
}

type planner struct {
	in  *Input
	adj map[string][]Edge
	// frontier[node][battery] = 该 (节点,电量) 下互不支配的 (时间,费用) 集合。
	frontier map[string]map[int][]timeCost
	labels   []label
}

func newPlanner(in *Input) *planner {
	p := &planner{
		in:       in,
		adj:      make(map[string][]Edge),
		frontier: make(map[string]map[int][]timeCost),
	}
	for _, e := range in.Edges {
		p.adj[e.From] = append(p.adj[e.From], e)
	}
	return p
}

// dominated 判断标签是否被支配：同节点存在 电量>=battery 且 时间<=t 且 费用<=c 的已结算标签。
// 电量更高意味着其可行后继动作是battery的超集，因此可跨电量等级支配。
func (p *planner) dominated(node string, battery, t, c int) bool {
	for b, f := range p.frontier[node] {
		if b < battery {
			continue
		}
		for _, e := range f {
			if e.time <= t && e.cost <= c {
				return true
			}
		}
	}
	return false
}

// record 将标签写入对应 (节点,电量) 的 Pareto 前沿，并剔除被它支配的旧标签。
func (p *planner) record(node string, battery, t, c int) {
	levels := p.frontier[node]
	if levels == nil {
		levels = make(map[int][]timeCost)
		p.frontier[node] = levels
	}
	kept := levels[battery][:0]
	for _, e := range levels[battery] {
		if t <= e.time && c <= e.cost {
			continue // 旧标签被新标签支配
		}
		kept = append(kept, e)
	}
	levels[battery] = append(kept, timeCost{time: t, cost: c})
}

// nextSwapTime 返回 t 时刻起最早可换电的时间（窗口按天循环，允许等待到次日）。
func nextSwapTime(st Station, t int) (int, bool) {
	if len(st.Windows) == 0 {
		return 0, false
	}
	base := (t / dayLength) * dayLength
	best := -1
	for d := 0; d <= 1; d++ {
		for _, w := range st.Windows {
			start := base + d*dayLength + w.Open
			end := base + d*dayLength + w.Close
			if t >= end {
				continue
			}
			cand := t
			if start > cand {
				cand = start
			}
			if best == -1 || cand < best {
				best = cand
			}
		}
	}
	if best == -1 {
		return 0, false
	}
	return best, true
}

// ---- 按 (时间, 费用) 字典序的优先队列 ----

type pqItem struct {
	labelIdx int
	time     int
	cost     int
}

type priorityQueue []pqItem

func (q priorityQueue) Len() int { return len(q) }
func (q priorityQueue) Less(i, j int) bool {
	if q[i].time != q[j].time {
		return q[i].time < q[j].time
	}
	return q[i].cost < q[j].cost
}
func (q priorityQueue) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *priorityQueue) Push(x interface{}) { *q = append(*q, x.(pqItem)) }
func (q *priorityQueue) Pop() interface{} {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}

// solve 求 (最早到达, 其次最低费用) 的路径。
//
// 终止性：电量为 [0,Capacity] 整数，(节点,电量) 状态有限；每个状态的 Pareto
// 前沿中时间递增则费用严格递减且费用有下界 0，故前沿有限；被支配标签不会
// 再扩展。零耗电/零耗时环上的重访标签时间费用均不更优，必被支配剪枝，
// 因此搜索必然终止。
func solve(in *Input) Result {
	p := newPlanner(in)
	pq := &priorityQueue{}
	heap.Init(pq)

	push := func(node string, battery, t, c, parent int, action string) {
		if p.dominated(node, battery, t, c) {
			return
		}
		p.labels = append(p.labels, label{node: node, battery: battery, time: t, cost: c, parent: parent, action: action})
		heap.Push(pq, pqItem{labelIdx: len(p.labels) - 1, time: t, cost: c})
	}

	push(in.Start, in.Capacity, in.StartTime, 0, -1, "")

	for pq.Len() > 0 {
		it := heap.Pop(pq).(pqItem)
		cur := p.labels[it.labelIdx]
		if p.dominated(cur.node, cur.battery, cur.time, cur.cost) {
			continue // 懒惰删除：入队后已被更优标签支配
		}
		p.record(cur.node, cur.battery, cur.time, cur.cost)

		if cur.node == in.Goal {
			return buildResult(in, p.labels, it.labelIdx)
		}

		// 动作 1：沿边行驶（电量足够时）
		for _, e := range p.adj[cur.node] {
			if e.Energy > cur.battery {
				continue
			}
			action := fmt.Sprintf("行驶 %s -> %s：耗时 %d，耗电 %d，到达时间 %d，剩余电量 %d",
				e.From, e.To, e.Time, e.Energy, cur.time+e.Time, cur.battery-e.Energy)
			push(e.To, cur.battery-e.Energy, cur.time+e.Time, cur.cost, it.labelIdx, action)
		}

		// 动作 2：在换电站付费换满电（必要时等待至开门）
		if st, ok := in.Stations[cur.node]; ok && cur.battery < in.Capacity {
			if ts, ok := nextSwapTime(st, cur.time); ok {
				var action string
				if ts > cur.time {
					action = fmt.Sprintf("在 %s 等待至时间 %d（换电站营业），支付 %d 更换满电电池：电量 %d -> %d",
						cur.node, ts, st.Fee, cur.battery, in.Capacity)
				} else {
					action = fmt.Sprintf("在 %s 支付 %d 更换满电电池：电量 %d -> %d",
						cur.node, st.Fee, cur.battery, in.Capacity)
				}
				push(cur.node, in.Capacity, ts, cur.cost+st.Fee, it.labelIdx, action)
			}
		}
	}
	return Result{Reachable: false}
}

// buildResult 由父指针回溯完整路径。
func buildResult(in *Input, labels []label, goalIdx int) Result {
	var chain []label
	for i := goalIdx; i != -1; i = labels[i].parent {
		chain = append(chain, labels[i])
	}
	steps := []string{
		fmt.Sprintf("从 %s 出发：时间 %d，满电 %d", in.Start, in.StartTime, in.Capacity),
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].action != "" {
			steps = append(steps, chain[i].action)
		}
	}
	return Result{
		Reachable: true,
		Arrival:   labels[goalIdx].time,
		Cost:      labels[goalIdx].cost,
		Steps:     steps,
	}
}
