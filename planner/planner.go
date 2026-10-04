// Package planner 实现保质期配货规划：
// 未来若干天需求、批次库存与到期日、每日调拨额度有限；
// 目标按字典序：1) 满足需求最多 2) 报废最少 3) 调拨成本最小。
// 约束：到期当日禁售（批次仅可在 Expiry 之前的天使用）。
//
// 建模为最小费用最大流：
//
//	源 -> 批次（容量=库存，费用 0）
//	批次 -> 天（仅 天 < Expiry，费用 = 单位调拨成本 - 报废避免奖励）
//	天 -> 汇（容量 = min(当日需求, 当日调拨额度)，费用 0）
//
// 最大流保证需求满足最多；在最大流中费用最小即报废最少、成本最小。
package planner

import (
	"fmt"
	"sort"
)

const (
	maxBatches  = 10
	maxBatchQty = int64(1) << 20 // 单批次库存上限，保证大权重数值安全
	maxCost     = int64(1) << 15 // 单位调拨成本上限
	// wWaste 为“避免一单位报废”的奖励，须大于任何方案间调拨成本差
	// （maxCost * 总库存），从而保证字典序：报废最少优先于成本最小。
	wWaste = int64(1) << 40
)

// Batch 一个库存批次。Expiry 为到期日（天序号，1 起）；
// 到期当日禁售，即可售天为 t < Expiry；Expiry > Days 表示规划期内不过期。
type Batch struct {
	ID     string `json:"id"`
	Qty    int64  `json:"qty"`
	Expiry int    `json:"expiry"`
	Cost   int64  `json:"cost"` // 单位调拨成本
}

// Input 规划输入。
type Input struct {
	Days    int     `json:"days"`    // 规划天数（通常 7）
	Demand  []int64 `json:"demand"`  // 每日需求，长度 = Days
	Quota   []int64 `json:"quota"`   // 每日调拨额度上限，长度 = Days
	Batches []Batch `json:"batches"` // 至多 10 个批次
}

// Flow 一条每日流向：某天从某批次调拨的数量。
type Flow struct {
	Day     int    `json:"day"`
	BatchID string `json:"batch_id"`
	Qty     int64  `json:"qty"`
}

// BatchWaste 批次报废量（规划期内到期且未售出的库存）。
type BatchWaste struct {
	BatchID string `json:"batch_id"`
	Qty     int64  `json:"qty"`
}

// Result 规划结果。
type Result struct {
	Flows      []Flow       `json:"flows"`       // 每日流向（按天、批次顺序）
	Unmet      []int64      `json:"unmet"`       // 每日不可满足需求
	TotalUnmet int64        `json:"total_unmet"` // 不可满足需求合计
	Waste      []BatchWaste `json:"waste"`       // 报废明细（仅含 >0）
	TotalWaste int64        `json:"total_waste"` // 报废合计
	TotalCost  int64        `json:"total_cost"`  // 总调拨成本
}

// Validate 校验输入合法性。
func (in *Input) Validate() error {
	if in.Days <= 0 {
		return fmt.Errorf("days 必须为正整数")
	}
	if len(in.Demand) != in.Days {
		return fmt.Errorf("demand 长度 %d 与 days %d 不一致", len(in.Demand), in.Days)
	}
	if len(in.Quota) != in.Days {
		return fmt.Errorf("quota 长度 %d 与 days %d 不一致", len(in.Quota), in.Days)
	}
	if len(in.Batches) == 0 {
		return fmt.Errorf("至少需要一个批次")
	}
	if len(in.Batches) > maxBatches {
		return fmt.Errorf("批次数 %d 超过上限 %d", len(in.Batches), maxBatches)
	}
	seen := map[string]bool{}
	for _, b := range in.Batches {
		if b.ID == "" {
			return fmt.Errorf("批次 id 不能为空")
		}
		if seen[b.ID] {
			return fmt.Errorf("批次 id 重复: %s", b.ID)
		}
		seen[b.ID] = true
		if b.Qty < 0 || b.Qty > maxBatchQty {
			return fmt.Errorf("批次 %s 库存须在 [0, %d] 内", b.ID, maxBatchQty)
		}
		if b.Expiry <= 0 {
			return fmt.Errorf("批次 %s 到期日必须 >= 1", b.ID)
		}
		if b.Cost < 0 || b.Cost >= maxCost {
			return fmt.Errorf("批次 %s 单位成本须在 [0, %d) 内", b.ID, maxCost)
		}
	}
	for i := range in.Demand {
		if in.Demand[i] < 0 || in.Quota[i] < 0 {
			return fmt.Errorf("第 %d 天需求/额度不能为负", i+1)
		}
	}
	return nil
}

// Solve 求解配货计划。
func Solve(in *Input) (*Result, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	days, nb := in.Days, len(in.Batches)
	// 节点编号：0=源, 1..nb=批次, nb+1..nb+days=天, nb+days+1=汇
	src, sink := 0, nb+days+1
	dayNode := func(t int) int { return nb + t } // t 为 1 起天序号
	g := newMCF(sink + 1)

	// 记录批次->天的边位置，便于事后取流量
	type edgeRef struct{ from, idx int }
	refs := make([][]edgeRef, nb)

	lastSellDay := func(expiry int) int {
		d := expiry - 1 // 到期当日禁售
		if d > days {
			d = days
		}
		return d
	}

	for bi, b := range in.Batches {
		batchNode := 1 + bi
		g.addEdge(src, batchNode, b.Qty, 0)
		// 规划期内会到期的批次给予报废避免奖励，鼓励优先使用
		reward := int64(0)
		if b.Expiry <= days {
			reward = wWaste
		}
		for t := 1; t <= lastSellDay(b.Expiry); t++ {
			idx := len(g.g[batchNode])
			g.addEdge(batchNode, dayNode(t), b.Qty, b.Cost-reward)
			refs[bi] = append(refs[bi], edgeRef{from: batchNode, idx: idx})
		}
	}
	for t := 1; t <= days; t++ {
		cap := in.Demand[t-1]
		if in.Quota[t-1] < cap {
			cap = in.Quota[t-1]
		}
		g.addEdge(dayNode(t), sink, cap, 0)
	}

	g.minCostMaxFlow(src, sink)

	res := &Result{Unmet: make([]int64, days)}
	sold := make([]int64, days)
	used := make([]int64, nb)
	for bi, b := range in.Batches {
		for t := 1; t <= lastSellDay(b.Expiry); t++ {
			r := refs[bi][t-1]
			q := g.flowOn(r.from, r.idx)
			if q > 0 {
				res.Flows = append(res.Flows, Flow{Day: t, BatchID: b.ID, Qty: q})
				sold[t-1] += q
				used[bi] += q
				res.TotalCost += q * b.Cost
			}
		}
	}
	sort.Slice(res.Flows, func(i, j int) bool {
		if res.Flows[i].Day != res.Flows[j].Day {
			return res.Flows[i].Day < res.Flows[j].Day
		}
		return res.Flows[i].BatchID < res.Flows[j].BatchID
	})
	for t := 0; t < days; t++ {
		res.Unmet[t] = in.Demand[t] - sold[t]
		res.TotalUnmet += res.Unmet[t]
	}
	for bi, b := range in.Batches {
		if b.Expiry <= days && used[bi] < b.Qty {
			w := b.Qty - used[bi]
			res.Waste = append(res.Waste, BatchWaste{BatchID: b.ID, Qty: w})
			res.TotalWaste += w
		}
	}
	return res, nil
}
