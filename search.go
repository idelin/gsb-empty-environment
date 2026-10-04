package main

import (
	"container/heap"
	"fmt"
	"strings"
)

// Plan 是求解结果：按顺序执行的动作名与总成本。
type Plan struct {
	Steps []string `json:"steps"`
	Cost  int64    `json:"cost"`
}

// Result 保存搜索产出：成功时的计划，以及已探索（已确定最短距离）的状态。
type Result struct {
	Found    bool
	Plan     Plan
	Explored map[uint32]int64 // 状态 -> 从初始状态到达的最低成本
}

// pqItem 是优先队列元素。
type pqItem struct {
	state uint32
	cost  int64
}

type priorityQueue []pqItem

func (q priorityQueue) Len() int            { return len(q) }
func (q priorityQueue) Less(i, j int) bool  { return q[i].cost < q[j].cost }
func (q priorityQueue) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *priorityQueue) Push(x interface{}) { *q = append(*q, x.(pqItem)) }
func (q *priorityQueue) Pop() interface{} {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}

type parentInfo struct {
	prev   uint32
	action int
}

// Solve 使用 Dijkstra（一致成本搜索）求达到目标的最低成本计划。
//
// 所有动作成本非负，且仅当发现严格更低的成本时才更新距离表，
// 因此零成本环不会引起无限搜索：环上状态的距离不会改进，不会重复入队扩张。
func Solve(p *Problem) *Result {
	dist := map[uint32]int64{p.initState: 0}
	parent := make(map[uint32]parentInfo)
	explored := make(map[uint32]int64)
	pq := &priorityQueue{{state: p.initState, cost: 0}}

	for pq.Len() > 0 {
		cur := heap.Pop(pq).(pqItem)
		if best, ok := explored[cur.state]; ok && cur.cost >= best {
			continue // 已以不更高成本确定，跳过过期条目
		}
		if cur.cost > dist[cur.state] {
			continue
		}
		explored[cur.state] = cur.cost
		if p.isGoal(cur.state) {
			return &Result{
				Found:    true,
				Plan:     reconstruct(p, parent, cur.state, cur.cost),
				Explored: explored,
			}
		}
		for i := range p.Actions {
			a := &p.Actions[i]
			if !a.applicable(cur.state) {
				continue
			}
			next := a.apply(cur.state)
			nc := cur.cost + a.Cost
			if old, ok := dist[next]; ok && nc >= old {
				continue // 未改进（含零成本环的等成本回边）
			}
			dist[next] = nc
			parent[next] = parentInfo{prev: cur.state, action: i}
			heap.Push(pq, pqItem{state: next, cost: nc})
		}
	}
	return &Result{Found: false, Explored: explored}
}

func reconstruct(p *Problem, parent map[uint32]parentInfo, goal uint32, cost int64) Plan {
	var rev []string
	for s := goal; s != p.initState; {
		pi := parent[s]
		rev = append(rev, p.Actions[pi.action].Name)
		s = pi.prev
	}
	steps := make([]string, len(rev))
	for i, name := range rev {
		steps[len(rev)-1-i] = name
	}
	return Plan{Steps: steps, Cost: cost}
}

// UnreachableSummary 生成目标不可达时的已探索状态摘要。
func (p *Problem) UnreachableSummary(r *Result, maxList int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "目标不可达。已探索状态数: %d\n", len(r.Explored))
	// 汇总目标缺口：列出每个已探索状态仍缺少的目标命题。
	missing := make(map[string]int)
	for s := range r.Explored {
		for i, name := range p.Propositions {
			bit := uint32(1) << uint(i)
			if p.goalTrue&bit != 0 && s&bit == 0 {
				missing["+"+name]++
			}
			if p.goalFalse&bit != 0 && s&bit != 0 {
				missing["-"+name]++
			}
		}
	}
	if len(missing) > 0 {
		keys := make([]string, 0, len(missing))
		for k := range missing {
			keys = append(keys, k)
		}
		sortStrings(keys)
		b.WriteString("目标条件在已探索状态中的满足情况（+ 表示需为真，- 表示需为假）:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s: 在 %d/%d 个状态中未满足\n", k, missing[k], len(r.Explored))
		}
	}
	states := sortedStates(r.Explored)
	shown := len(states)
	if maxList > 0 && shown > maxList {
		shown = maxList
	}
	fmt.Fprintf(&b, "已探索状态（按到达成本升序，显示 %d/%d）:\n", shown, len(states))
	for _, s := range states[:shown] {
		fmt.Fprintf(&b, "  成本 %6d  %s\n", r.Explored[s], p.stateString(s))
	}
	return b.String()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
