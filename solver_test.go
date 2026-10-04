package main

import "testing"

func solve(t *testing.T, in Input) Result {
	t.Helper()
	if err := in.validate(); err != nil {
		t.Fatalf("输入不合法: %v", err)
	}
	in.setDefaults()
	s := NewSolver(in)
	s.Solve()
	return s.Replay()
}

// 同一时段内禁止循环兑换：返还积分只能下一时段用，
// 单一时段下 100 分只能换 2 份（每份 50），返还无法复用。
func TestNoSamePeriodCycle(t *testing.T) {
	res := solve(t, Input{
		Periods: 1,
		Batches: []Batch{{ID: "b1", Source: "default", Amount: 100, ExpirePeriod: 1}},
		Offers:  []Offer{{ID: "o1", Period: 1, Cost: 50, Value: 60, Rebate: 50, Inventory: 10}},
	})
	if res.TotalValue != 120 {
		t.Fatalf("TotalValue = %d, 期望 120（若允许循环兑换会无限刷分）", res.TotalValue)
	}
	if res.TotalSpent != 100 {
		t.Fatalf("TotalSpent = %d, 期望 100", res.TotalSpent)
	}
	if res.ForfeitedRebate != 100 {
		t.Fatalf("ForfeitedRebate = %d, 期望 100", res.ForfeitedRebate)
	}
}

// 返还积分在下一时段到账并可继续兑换。
func TestRebateUsableNextPeriod(t *testing.T) {
	res := solve(t, Input{
		Periods: 2,
		Batches: []Batch{{ID: "b1", Source: "default", Amount: 100, ExpirePeriod: 2}},
		Offers: []Offer{
			{ID: "o1", Period: 1, Cost: 50, Value: 60, Rebate: 50, Inventory: 10},
			{ID: "o2", Period: 2, Cost: 50, Value: 60, Inventory: 10},
		},
	})
	if res.TotalValue != 240 {
		t.Fatalf("TotalValue = %d, 期望 240", res.TotalValue)
	}
	if res.TotalExpired != 0 {
		t.Fatalf("TotalExpired = %d, 期望 0", res.TotalExpired)
	}
	p2 := res.Periods[1]
	if len(p2.Arrivals) != 1 || p2.Arrivals[0].Amount != 100 {
		t.Fatalf("时段 2 返还到账 = %+v, 期望 1 笔 100 分", p2.Arrivals)
	}
	if len(p2.Deductions) != 1 || p2.Deductions[0].BatchID != "rebate#2:o1" {
		t.Fatalf("时段 2 扣减 = %+v, 期望来自 rebate#2:o1", p2.Deductions)
	}
}

// 到期未用的积分计入到期损失。
func TestExpirationLoss(t *testing.T) {
	res := solve(t, Input{
		Periods: 1,
		Batches: []Batch{{ID: "b1", Source: "default", Amount: 100, ExpirePeriod: 1}},
		Offers:  []Offer{{ID: "o1", Period: 1, Cost: 30, Value: 10, Inventory: 1}},
	})
	if res.TotalValue != 10 || res.TotalExpired != 70 {
		t.Fatalf("value=%d expired=%d, 期望 10 / 70", res.TotalValue, res.TotalExpired)
	}
	if len(res.Periods[0].Expired) != 1 || res.Periods[0].Expired[0].BatchID != "b1" {
		t.Fatalf("到期明细 = %+v", res.Periods[0].Expired)
	}
}

// 来源受限：mall 积分不能支付仅允许 creditcard 的兑换项。
func TestSourceRestriction(t *testing.T) {
	res := solve(t, Input{
		Periods: 1,
		Batches: []Batch{{ID: "b1", Source: "mall", Amount: 100, ExpirePeriod: 1}},
		Offers: []Offer{{ID: "o1", Period: 1, Cost: 50, Value: 60, Inventory: 5,
			AllowedSources: []string{"creditcard"}}},
	})
	if res.TotalValue != 0 || res.TotalExpired != 100 {
		t.Fatalf("value=%d expired=%d, 期望 0 / 100", res.TotalValue, res.TotalExpired)
	}
}

// 返还积分的来源同样受兑换项限制。
func TestRebateSourceRestriction(t *testing.T) {
	res := solve(t, Input{
		Periods: 2,
		Batches: []Batch{{ID: "b1", Source: "cc", Amount: 100, ExpirePeriod: 1}},
		Offers: []Offer{
			{ID: "o1", Period: 1, Cost: 50, Value: 60, Rebate: 50, Inventory: 2,
				AllowedSources: []string{"cc"}},
			{ID: "o2", Period: 2, Cost: 50, Value: 60, Inventory: 2,
				AllowedSources: []string{"cc"}},
		},
	})
	if res.TotalValue != 120 {
		t.Fatalf("TotalValue = %d, 期望 120（返还积分来源为 rebate，不能换 o2）", res.TotalValue)
	}
	if res.TotalExpired != 100 {
		t.Fatalf("TotalExpired = %d, 期望 100（返还积分到期作废）", res.TotalExpired)
	}
}

// 库存上限生效。
func TestInventoryCap(t *testing.T) {
	res := solve(t, Input{
		Periods: 1,
		Batches: []Batch{{ID: "b1", Source: "default", Amount: 10000, ExpirePeriod: 1}},
		Offers:  []Offer{{ID: "o1", Period: 1, Cost: 10, Value: 5, Inventory: 3}},
	})
	if res.TotalValue != 15 || res.TotalSpent != 30 {
		t.Fatalf("value=%d spent=%d, 期望 15 / 30", res.TotalValue, res.TotalSpent)
	}
}

// 同一时段优先选价值更高的兑换项。
func TestPrefersHigherValue(t *testing.T) {
	res := solve(t, Input{
		Periods: 1,
		Batches: []Batch{{ID: "b1", Source: "default", Amount: 100, ExpirePeriod: 1}},
		Offers: []Offer{
			{ID: "low", Period: 1, Cost: 50, Value: 60, Inventory: 5},
			{ID: "high", Period: 1, Cost: 50, Value: 70, Inventory: 5},
		},
	})
	if res.TotalValue != 140 {
		t.Fatalf("TotalValue = %d, 期望 140", res.TotalValue)
	}
	if got := res.Periods[0].Redemptions[0].OfferID; got != "high" {
		t.Fatalf("兑换项 = %s, 期望 high", got)
	}
}

// FEFO：同一时段扣减优先使用最早到期的批次，避免其到期损失。
func TestFEFODeduction(t *testing.T) {
	res := solve(t, Input{
		Periods: 2,
		Batches: []Batch{
			{ID: "b1", Source: "default", Amount: 50, ExpirePeriod: 1},
			{ID: "b2", Source: "default", Amount: 100, ExpirePeriod: 2},
		},
		Offers: []Offer{
			{ID: "o1", Period: 1, Cost: 50, Value: 60, Inventory: 1},
			{ID: "o2", Period: 2, Cost: 50, Value: 60, Inventory: 2},
		},
	})
	if res.TotalValue != 180 {
		t.Fatalf("TotalValue = %d, 期望 180", res.TotalValue)
	}
	if res.TotalExpired != 0 {
		t.Fatalf("TotalExpired = %d, 期望 0（b1 应被优先消耗）", res.TotalExpired)
	}
	d := res.Periods[0].Deductions[0]
	if d.BatchID != "b1" || d.Amount != 50 {
		t.Fatalf("时段 1 扣减 = %+v, 期望 b1 -50", d)
	}
}

// 跨时段权衡：与其在时段 1 低价值兑换，不如留给时段 2 高价值兑换。
func TestCrossPeriodTradeoff(t *testing.T) {
	res := solve(t, Input{
		Periods: 2,
		Batches: []Batch{{ID: "b1", Source: "default", Amount: 100, ExpirePeriod: 2}},
		Offers: []Offer{
			{ID: "cheap", Period: 1, Cost: 100, Value: 101, Inventory: 1},
			{ID: "good", Period: 2, Cost: 100, Value: 200, Inventory: 1},
		},
	})
	if res.TotalValue != 200 {
		t.Fatalf("TotalValue = %d, 期望 200", res.TotalValue)
	}
	if len(res.Periods[0].Redemptions) != 0 {
		t.Fatalf("时段 1 不应兑换: %+v", res.Periods[0].Redemptions)
	}
}
