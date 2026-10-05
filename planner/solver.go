package main

import (
	"errors"
	"fmt"
)

// Batch 一个库存批次。ExpiryDay 为到期日（1 起计），到期当日禁售，
// 即可售日期为 1 .. ExpiryDay-1；ExpiryDay > Days 表示规划期内不会过期。
type Batch struct {
	ID           string `json:"id"`
	Quantity     int64  `json:"quantity"`
	ExpiryDay    int    `json:"expiryDay"`
	TransferCost int64  `json:"transferCost"` // 每单位调拨成本
}

// Input 规划输入：未来 Days 天（默认 7 天）的需求、每日调拨额度与批次库存。
type Input struct {
	Days             int     `json:"days"`
	Demand           []int64 `json:"demand"`
	TransferCapacity []int64 `json:"transferCapacity"` // 每日调拨额度；缺省表示不限
	Batches          []Batch `json:"batches"`
}

// Flow 某日从某批次调拨的数量。
type Flow struct {
	Day      int    `json:"day"`
	BatchID  string `json:"batchId"`
	Quantity int64  `json:"quantity"`
}

// DayQuantity 按日汇总数量。
type DayQuantity struct {
	Day      int   `json:"day"`
	Quantity int64 `json:"quantity"`
}

// BatchQuantity 按批次汇总数量。
type BatchQuantity struct {
	BatchID  string `json:"batchId"`
	Quantity int64  `json:"quantity"`
}

// Output 规划结果。
type Output struct {
	Flows             []Flow          `json:"flows"`
	UnmetDemand       []DayQuantity   `json:"unmetDemand"`
	Waste             []BatchQuantity `json:"waste"`
	Remaining         []BatchQuantity `json:"remaining"`
	TotalSatisfied    int64           `json:"totalSatisfied"`
	TotalUnmet        int64           `json:"totalUnmet"`
	TotalWaste        int64           `json:"totalWaste"`
	TotalTransferCost int64           `json:"totalTransferCost"`
}

const maxBatches = 10

func validate(in *Input) error {
	if in.Days == 0 {
		in.Days = 7
	}
	if in.Days <= 0 {
		return errors.New("days 必须为正数")
	}
	if len(in.Demand) != in.Days {
		return fmt.Errorf("demand 长度(%d)必须等于 days(%d)", len(in.Demand), in.Days)
	}
	if in.TransferCapacity == nil {
		in.TransferCapacity = make([]int64, in.Days)
		for i := range in.TransferCapacity {
			in.TransferCapacity[i] = int64(1)<<62 - 1
		}
	}
	if len(in.TransferCapacity) != in.Days {
		return fmt.Errorf("transferCapacity 长度(%d)必须等于 days(%d)", len(in.TransferCapacity), in.Days)
	}
	if len(in.Batches) == 0 {
		return errors.New("至少需要一个批次")
	}
	if len(in.Batches) > maxBatches {
		return fmt.Errorf("批次数量(%d)超过上限 %d", len(in.Batches), maxBatches)
	}
	seen := map[string]bool{}
	for _, b := range in.Batches {
		if b.ID == "" {
			return errors.New("批次 id 不能为空")
		}
		if seen[b.ID] {
			return fmt.Errorf("批次 id 重复: %s", b.ID)
		}
		seen[b.ID] = true
		if b.Quantity < 0 {
			return fmt.Errorf("批次 %s 库存不能为负", b.ID)
		}
		if b.ExpiryDay < 1 {
			return fmt.Errorf("批次 %s 到期日必须 >= 1", b.ID)
		}
		if b.TransferCost < 0 {
			return fmt.Errorf("批次 %s 调拨成本不能为负", b.ID)
		}
	}
	for i, d := range in.Demand {
		if d < 0 {
			return fmt.Errorf("第 %d 天需求不能为负", i+1)
		}
	}
	for i, c := range in.TransferCapacity {
		if c < 0 {
			return fmt.Errorf("第 %d 天调拨额度不能为负", i+1)
		}
	}
	return nil
}

// ---- 最小费用最大流 ----

type mcfEdge struct {
	to, rev   int
	cap, cost int64
}

type mcfGraph struct {
	g [][]mcfEdge
}

func newMCFGraph(n int) *mcfGraph { return &mcfGraph{g: make([][]mcfEdge, n)} }

func (gr *mcfGraph) addEdge(from, to int, cap, cost int64) {
	gr.g[from] = append(gr.g[from], mcfEdge{to: to, rev: len(gr.g[to]), cap: cap, cost: cost})
	gr.g[to] = append(gr.g[to], mcfEdge{to: from, rev: len(gr.g[from]) - 1, cap: 0, cost: -cost})
}

// minCostMaxFlow 用 SPFA 找最短路增广，返回 (最大流, 最小费用)。
func (gr *mcfGraph) minCostMaxFlow(s, t int) (int64, int64) {
	n := len(gr.g)
	const inf = int64(1)<<62 - 1
	var flow, cost int64
	prevv := make([]int, n)
	preve := make([]int, n)
	dist := make([]int64, n)
	inq := make([]bool, n)
	for {
		for i := range dist {
			dist[i] = inf
		}
		dist[s] = 0
		queue := []int{s}
		inq[s] = true
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			inq[v] = false
			for ei := range gr.g[v] {
				e := &gr.g[v][ei]
				if e.cap > 0 && dist[e.to] > dist[v]+e.cost {
					dist[e.to] = dist[v] + e.cost
					prevv[e.to] = v
					preve[e.to] = ei
					if !inq[e.to] {
						queue = append(queue, e.to)
						inq[e.to] = true
					}
				}
			}
		}
		if dist[t] == inf {
			return flow, cost
		}
		d := inf
		for v := t; v != s; v = prevv[v] {
			if c := gr.g[prevv[v]][preve[v]].cap; c < d {
				d = c
			}
		}
		flow += d
		cost += d * dist[t]
		for v := t; v != s; v = prevv[v] {
			e := &gr.g[prevv[v]][preve[v]]
			e.cap -= d
			gr.g[v][e.rev].cap += d
		}
	}
}

// Solve 求字典序最优计划：① 满足需求最多 ② 报废最少 ③ 调拨成本最低。
//
// 建模：源 -> 批次(容量=库存) -> 可售日(费用=调拨成本-BIG) -> 汇(容量=min(需求,日额度))；
// 批次 -> 汇 为剩余边，规划期内会过期的批次费用为 MID（报废惩罚），否则为 0。
// BIG 远大于 MID 远大于最大调拨成本，从而保证三目标的字典序。
func Solve(in *Input) (*Output, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	days := in.Days
	nb := len(in.Batches)

	var maxCost int64
	for _, b := range in.Batches {
		if b.TransferCost > maxCost {
			maxCost = b.TransferCost
		}
	}
	mid := 2*maxCost + 1         // 报废 1 单位的惩罚，压过任何调拨成本差异
	big := 2*mid + 2*maxCost + 1 // 多满足 1 单位需求的收益，压过报废与成本

	// 节点编号: 0=源, 1..nb=批次, nb+1..nb+days=日期, nb+days+1=汇
	src, sink := 0, nb+days+1
	gr := newMCFGraph(sink + 1)
	batchNode := func(i int) int { return 1 + i }
	dayNode := func(t int) int { return nb + 1 + t }

	const infCap = int64(1)<<62 - 1
	for i, b := range in.Batches {
		gr.addEdge(src, batchNode(i), b.Quantity, 0)
		for t := 0; t < days; t++ {
			if t+1 < b.ExpiryDay { // 到期当日禁售：仅可流向早于到期日的日期
				gr.addEdge(batchNode(i), dayNode(t), infCap, b.TransferCost-big)
			}
		}
		wasteCost := int64(0)
		if b.ExpiryDay <= days { // 期内会过期，剩余即报废
			wasteCost = mid
		}
		gr.addEdge(batchNode(i), sink, infCap, wasteCost)
	}
	for t := 0; t < days; t++ {
		cap := in.Demand[t]
		if in.TransferCapacity[t] < cap {
			cap = in.TransferCapacity[t]
		}
		gr.addEdge(dayNode(t), sink, cap, 0)
	}

	gr.minCostMaxFlow(src, sink)

	// 从残量网络提取结果
	out := &Output{}
	sold := make([]int64, days)
	used := make([]int64, nb)
	for i, b := range in.Batches {
		for _, e := range gr.g[batchNode(i)] {
			if e.to >= dayNode(0) && e.to < dayNode(days) {
				q := gr.g[e.to][e.rev].cap // 反向边容量 = 正向流量
				if q > 0 {
					t := e.to - dayNode(0)
					out.Flows = append(out.Flows, Flow{Day: t + 1, BatchID: b.ID, Quantity: q})
					sold[t] += q
					used[i] += q
					out.TotalTransferCost += q * b.TransferCost
				}
			}
		}
		left := b.Quantity - used[i]
		if left > 0 {
			if b.ExpiryDay <= days {
				out.Waste = append(out.Waste, BatchQuantity{BatchID: b.ID, Quantity: left})
				out.TotalWaste += left
			} else {
				out.Remaining = append(out.Remaining, BatchQuantity{BatchID: b.ID, Quantity: left})
			}
		}
	}
	for t := 0; t < days; t++ {
		out.TotalSatisfied += sold[t]
		if unmet := in.Demand[t] - sold[t]; unmet > 0 {
			out.UnmetDemand = append(out.UnmetDemand, DayQuantity{Day: t + 1, Quantity: unmet})
			out.TotalUnmet += unmet
		}
	}
	if out.Flows == nil {
		out.Flows = []Flow{}
	}
	if out.UnmetDemand == nil {
		out.UnmetDemand = []DayQuantity{}
	}
	if out.Waste == nil {
		out.Waste = []BatchQuantity{}
	}
	if out.Remaining == nil {
		out.Remaining = []BatchQuantity{}
	}
	return out, nil
}
