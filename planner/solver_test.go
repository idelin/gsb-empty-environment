package main

import "testing"

func flowOf(out *Output, day int, batchID string) int64 {
	for _, f := range out.Flows {
		if f.Day == day && f.BatchID == batchID {
			return f.Quantity
		}
	}
	return 0
}

func unmetOf(out *Output, day int) int64 {
	for _, u := range out.UnmetDemand {
		if u.Day == day {
			return u.Quantity
		}
	}
	return 0
}

func wasteOf(out *Output, batchID string) int64 {
	for _, w := range out.Waste {
		if w.BatchID == batchID {
			return w.Quantity
		}
	}
	return 0
}

// 到期当日禁售：第 2 天到期的批次不能用于第 2 天的需求。
func TestExpiryDayForbidden(t *testing.T) {
	in := &Input{
		Days:             7,
		Demand:           []int64{0, 5, 0, 0, 0, 0, 0},
		TransferCapacity: []int64{100, 100, 100, 100, 100, 100, 100},
		Batches:          []Batch{{ID: "A", Quantity: 10, ExpiryDay: 2, TransferCost: 1}},
	}
	out, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := unmetOf(out, 2); got != 5 {
		t.Errorf("第 2 天不可满足需求 = %d, 期望 5（到期当日禁售）", got)
	}
	if got := wasteOf(out, "A"); got != 10 {
		t.Errorf("批次 A 报废 = %d, 期望 10", got)
	}
	if out.TotalSatisfied != 0 {
		t.Errorf("总满足 = %d, 期望 0", out.TotalSatisfied)
	}
}

// 报废最少优先于调拨成本：宁可用贵的临期批次，也不让库存报废。
func TestWasteBeatsCost(t *testing.T) {
	in := &Input{
		Days:             7,
		Demand:           []int64{10, 0, 0, 0, 0, 0, 0},
		TransferCapacity: []int64{100, 100, 100, 100, 100, 100, 100},
		Batches: []Batch{
			{ID: "near", Quantity: 10, ExpiryDay: 2, TransferCost: 100},
			{ID: "far", Quantity: 10, ExpiryDay: 99, TransferCost: 1},
		},
	}
	out, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := flowOf(out, 1, "near"); got != 10 {
		t.Errorf("第 1 天应全部使用临期批次 near, 实际 %d", got)
	}
	if out.TotalWaste != 0 {
		t.Errorf("总报废 = %d, 期望 0", out.TotalWaste)
	}
	if out.TotalTransferCost != 1000 {
		t.Errorf("总调拨成本 = %d, 期望 1000", out.TotalTransferCost)
	}
}

// 需求满足为第一目标：销量优先于一切。
func TestDemandFirst(t *testing.T) {
	in := &Input{
		Days:             7,
		Demand:           []int64{10, 0, 0, 0, 0, 0, 0},
		TransferCapacity: []int64{100, 100, 100, 100, 100, 100, 100},
		Batches: []Batch{
			{ID: "A", Quantity: 6, ExpiryDay: 99, TransferCost: 50},
			{ID: "B", Quantity: 6, ExpiryDay: 99, TransferCost: 1},
		},
	}
	out, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalSatisfied != 10 {
		t.Errorf("总满足 = %d, 期望 10", out.TotalSatisfied)
	}
	// 同为无期批次时选便宜的：B 用 6，A 用 4
	if got := flowOf(out, 1, "B"); got != 6 {
		t.Errorf("批次 B 用量 = %d, 期望 6", got)
	}
	if got := flowOf(out, 1, "A"); got != 4 {
		t.Errorf("批次 A 用量 = %d, 期望 4", got)
	}
	if out.TotalTransferCost != 6*1+4*50 {
		t.Errorf("总调拨成本 = %d, 期望 %d", out.TotalTransferCost, 6*1+4*50)
	}
}

// 每日调拨额度限制会产生不可满足需求。
func TestDailyCapacityLimit(t *testing.T) {
	in := &Input{
		Days:             7,
		Demand:           []int64{10, 10, 0, 0, 0, 0, 0},
		TransferCapacity: []int64{3, 4, 0, 0, 0, 0, 0},
		Batches:          []Batch{{ID: "A", Quantity: 100, ExpiryDay: 99, TransferCost: 1}},
	}
	out, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := unmetOf(out, 1); got != 7 {
		t.Errorf("第 1 天不可满足 = %d, 期望 7", got)
	}
	if got := unmetOf(out, 2); got != 6 {
		t.Errorf("第 2 天不可满足 = %d, 期望 6", got)
	}
	if out.TotalSatisfied != 7 {
		t.Errorf("总满足 = %d, 期望 7", out.TotalSatisfied)
	}
}

// 临期批次只能在其可售窗口内被消耗，窗口不足导致报废。
func TestExpiryWindowWaste(t *testing.T) {
	in := &Input{
		Days:             7,
		Demand:           []int64{4, 4, 4, 0, 0, 0, 0},
		TransferCapacity: []int64{100, 100, 100, 100, 100, 100, 100},
		Batches: []Batch{
			{ID: "A", Quantity: 10, ExpiryDay: 3, TransferCost: 1}, // 仅第 1、2 天可售
			{ID: "B", Quantity: 10, ExpiryDay: 99, TransferCost: 5},
		},
	}
	out, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	// A 最多在前两天卖出 8，报废 2；第 3 天需求由 B 满足
	if got := wasteOf(out, "A"); got != 2 {
		t.Errorf("批次 A 报废 = %d, 期望 2", got)
	}
	if got := flowOf(out, 3, "B"); got != 4 {
		t.Errorf("第 3 天批次 B 用量 = %d, 期望 4", got)
	}
	if out.TotalUnmet != 0 {
		t.Errorf("总不可满足 = %d, 期望 0", out.TotalUnmet)
	}
}

// 批次数量上限为 10。
func TestBatchLimit(t *testing.T) {
	in := &Input{Days: 7, Demand: make([]int64, 7)}
	for i := 0; i < 11; i++ {
		in.Batches = append(in.Batches, Batch{ID: string(rune('A' + i)), Quantity: 1, ExpiryDay: 99})
	}
	if _, err := Solve(in); err == nil {
		t.Error("超过 10 个批次应报错")
	}
}
