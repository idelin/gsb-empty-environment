package planner

import "testing"

func quota(days int, q int64) []int64 {
	out := make([]int64, days)
	for i := range out {
		out[i] = q
	}
	return out
}

func sumUnmet(r *Result) int64 { return r.TotalUnmet }

// 到期当日禁售：到期日当天的需求不能用该批次满足。
func TestExpiryDayForbidden(t *testing.T) {
	in := &Input{
		Days:   3,
		Demand: []int64{0, 0, 10},
		Quota:  quota(3, 100),
		Batches: []Batch{
			{ID: "A", Qty: 10, Expiry: 3, Cost: 1}, // 第 3 天到期，当日禁售
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalUnmet != 10 {
		t.Fatalf("到期当日库存不得充数, unmet=%d 期望 10", res.TotalUnmet)
	}
	if len(res.Flows) != 0 {
		t.Fatalf("不应产生任何流向: %+v", res.Flows)
	}
	if res.TotalWaste != 10 {
		t.Fatalf("全部报废, waste=%d 期望 10", res.TotalWaste)
	}
}

// 到期前一天可用。
func TestSellableBeforeExpiry(t *testing.T) {
	in := &Input{
		Days:   3,
		Demand: []int64{0, 10, 0},
		Quota:  quota(3, 100),
		Batches: []Batch{
			{ID: "A", Qty: 10, Expiry: 3, Cost: 1},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalUnmet != 0 || res.TotalWaste != 0 {
		t.Fatalf("unmet=%d waste=%d, 期望均为 0", res.TotalUnmet, res.TotalWaste)
	}
	if len(res.Flows) != 1 || res.Flows[0].Day != 2 || res.Flows[0].Qty != 10 {
		t.Fatalf("流向不符: %+v", res.Flows)
	}
}

// 每日调拨额度限制满足量。
func TestDailyQuota(t *testing.T) {
	in := &Input{
		Days:   2,
		Demand: []int64{10, 10},
		Quota:  []int64{6, 100},
		Batches: []Batch{
			{ID: "A", Qty: 20, Expiry: 9, Cost: 1},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Unmet[0] != 4 || res.Unmet[1] != 0 {
		t.Fatalf("unmet=%v, 期望 [4 0]", res.Unmet)
	}
}

// 报废最少优先于成本：贵但快过期的批次应优先于便宜的长保质期批次。
func TestWasteBeforeCost(t *testing.T) {
	in := &Input{
		Days:   2,
		Demand: []int64{5, 0},
		Quota:  quota(2, 100),
		Batches: []Batch{
			{ID: "EXP", Qty: 5, Expiry: 2, Cost: 100}, // 明天到期，贵
			{ID: "FAR", Qty: 5, Expiry: 9, Cost: 1},   // 不过期，便宜
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalWaste != 0 {
		t.Fatalf("应优先消耗临期批次避免报废, waste=%d", res.TotalWaste)
	}
	if res.TotalCost != 500 {
		t.Fatalf("cost=%d, 期望 500 (5*100)", res.TotalCost)
	}
	if len(res.Flows) != 1 || res.Flows[0].BatchID != "EXP" {
		t.Fatalf("流向不符: %+v", res.Flows)
	}
}

// 报废相同则成本最小。
func TestCostTieBreak(t *testing.T) {
	in := &Input{
		Days:   2,
		Demand: []int64{5, 0},
		Quota:  quota(2, 100),
		Batches: []Batch{
			{ID: "X", Qty: 5, Expiry: 9, Cost: 10},
			{ID: "Y", Qty: 5, Expiry: 9, Cost: 2},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCost != 10 {
		t.Fatalf("cost=%d, 期望 10 (5*2)", res.TotalCost)
	}
	if len(res.Flows) != 1 || res.Flows[0].BatchID != "Y" {
		t.Fatalf("流向不符: %+v", res.Flows)
	}
}

// 需求满足优先于报废与成本：额度紧张时先保满足量。
func TestDemandFirst(t *testing.T) {
	in := &Input{
		Days:   2,
		Demand: []int64{10, 10},
		Quota:  quota(2, 10),
		Batches: []Batch{
			{ID: "EXP", Qty: 10, Expiry: 2, Cost: 1}, // 只能第 1 天用
			{ID: "FAR", Qty: 10, Expiry: 9, Cost: 1},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if sumUnmet(res) != 0 {
		t.Fatalf("unmet=%d, 期望 0", res.TotalUnmet)
	}
	if res.TotalWaste != 0 {
		t.Fatalf("waste=%d, 期望 0", res.TotalWaste)
	}
}

// 库存不足时输出不可满足需求。
func TestUnmetReported(t *testing.T) {
	in := &Input{
		Days:   7,
		Demand: []int64{5, 5, 5, 5, 5, 5, 5},
		Quota:  quota(7, 100),
		Batches: []Batch{
			{ID: "A", Qty: 12, Expiry: 9, Cost: 1},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalUnmet != 35-12 {
		t.Fatalf("unmet=%d, 期望 23", res.TotalUnmet)
	}
}

// 已过期批次（Expiry=1，第一天即到期当日）不可用。
func TestAlreadyExpired(t *testing.T) {
	in := &Input{
		Days:   1,
		Demand: []int64{5},
		Quota:  quota(1, 100),
		Batches: []Batch{
			{ID: "A", Qty: 5, Expiry: 1, Cost: 1},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalUnmet != 5 || res.TotalWaste != 5 {
		t.Fatalf("unmet=%d waste=%d, 期望 5/5", res.TotalUnmet, res.TotalWaste)
	}
}

// 输入校验。
func TestValidation(t *testing.T) {
	bad := &Input{
		Days:   7,
		Demand: quota(7, 1),
		Quota:  quota(7, 1),
		Batches: func() []Batch {
			bs := make([]Batch, 11)
			for i := range bs {
				bs[i] = Batch{ID: string(rune('A' + i)), Qty: 1, Expiry: 9}
			}
			return bs
		}(),
	}
	if err := bad.Validate(); err == nil {
		t.Fatal("超过 10 个批次应报错")
	}
}
