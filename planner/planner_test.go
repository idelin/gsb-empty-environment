package planner

import "testing"

func solve(t *testing.T, in Input) *Plan {
	t.Helper()
	s, err := NewSolver(in)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	plan, err := s.Solve()
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return plan
}

func deductAmount(pp PeriodPlan, batchID string) int {
	for _, d := range pp.Deductions {
		if d.BatchID == batchID {
			return d.Amount
		}
	}
	return 0
}

func expiredAmount(pp PeriodPlan, batchID string) int {
	for _, d := range pp.Expired {
		if d.BatchID == batchID {
			return d.Amount
		}
	}
	return 0
}

func TestBasicRedemption(t *testing.T) {
	plan := solve(t, Input{
		Periods: 1,
		Batches: []Batch{{ID: "B1", Source: "s", Amount: 100, Expiry: 1}},
		Rewards: []Reward{{ID: "R1", Category: "c", Cost: 50, Value: 40, Inventory: 5}},
	})
	if plan.TotalValue != 80 {
		t.Fatalf("TotalValue = %d, want 80", plan.TotalValue)
	}
	if got := deductAmount(plan.Periods[0], "B1"); got != 100 {
		t.Fatalf("B1 deducted = %d, want 100", got)
	}
	if plan.TotalExpired != 0 {
		t.Fatalf("TotalExpired = %d, want 0", plan.TotalExpired)
	}
}

func TestCashbackNotUsableSamePeriod(t *testing.T) {
	// Cashback equals cost, so if same-period cycling were allowed the
	// second unit could be paid for with the cashback of the first.
	plan := solve(t, Input{
		Periods: 1,
		Batches: []Batch{{ID: "B1", Source: "s", Amount: 100, Expiry: 1}},
		Rewards: []Reward{{ID: "R1", Category: "c", Cost: 100, Value: 10, Cashback: 100, Inventory: 2}},
	})
	if plan.TotalValue != 10 {
		t.Fatalf("TotalValue = %d, want 10 (cashback must not fund same-period redemption)", plan.TotalValue)
	}
	if plan.TotalCashback != 100 {
		t.Fatalf("TotalCashback = %d, want 100", plan.TotalCashback)
	}
	if got := expiredAmount(plan.Periods[0], RebateID); got != 100 {
		t.Fatalf("rebate expired = %d, want 100", got)
	}
}

func TestCashbackChainedAcrossPeriods(t *testing.T) {
	plan := solve(t, Input{
		Periods: 3,
		Batches: []Batch{{ID: "B1", Source: "s", Amount: 100, Expiry: 3}},
		Rewards: []Reward{{ID: "R1", Category: "c", Cost: 100, Value: 100, Cashback: 100, Inventory: 1}},
	})
	if plan.TotalValue != 300 {
		t.Fatalf("TotalValue = %d, want 300 (one redemption per period via cashback)", plan.TotalValue)
	}
	for i, pp := range plan.Periods {
		if len(pp.Redemptions) != 1 {
			t.Fatalf("period %d: redemptions = %d, want 1", i+1, len(pp.Redemptions))
		}
	}
	if got := expiredAmount(plan.Periods[2], RebateID); got != 100 {
		t.Fatalf("final rebate expired = %d, want 100", got)
	}
}

func TestExpiryLossAndEarliestBatchUsedFirst(t *testing.T) {
	plan := solve(t, Input{
		Periods: 2,
		Batches: []Batch{
			{ID: "B1", Source: "s", Amount: 60, Expiry: 1},
			{ID: "B2", Source: "s", Amount: 100, Expiry: 2},
		},
		Rewards: []Reward{{ID: "R1", Category: "c", Cost: 50, Value: 50, Inventory: 1}},
	})
	if plan.TotalValue != 100 {
		t.Fatalf("TotalValue = %d, want 100", plan.TotalValue)
	}
	if got := deductAmount(plan.Periods[0], "B1"); got != 50 {
		t.Fatalf("period 1 must spend expiring batch B1: deducted = %d, want 50", got)
	}
	if got := expiredAmount(plan.Periods[1], "B1"); got != 10 {
		t.Fatalf("B1 expired = %d, want 10", got)
	}
	if got := expiredAmount(plan.Periods[1], "B2"); got != 50 {
		t.Fatalf("B2 expired = %d, want 50", got)
	}
	if plan.TotalExpired != 60 {
		t.Fatalf("TotalExpired = %d, want 60", plan.TotalExpired)
	}
}

func TestSourceCategoryRestriction(t *testing.T) {
	plan := solve(t, Input{
		Periods: 1,
		Batches: []Batch{{ID: "B1", Source: "s", Amount: 100, Expiry: 1, Categories: []string{"gift"}}},
		Rewards: []Reward{
			{ID: "R1", Category: "coupon", Cost: 50, Value: 50, Inventory: 1},
			{ID: "R2", Category: "gift", Cost: 50, Value: 40, Inventory: 1},
		},
	})
	if plan.TotalValue != 40 {
		t.Fatalf("TotalValue = %d, want 40 (restricted batch cannot buy coupon reward)", plan.TotalValue)
	}
	if got := deductAmount(plan.Periods[0], "B1"); got != 50 {
		t.Fatalf("B1 deducted = %d, want 50", got)
	}
	if got := expiredAmount(plan.Periods[0], "B1"); got != 50 {
		t.Fatalf("B1 expired = %d, want 50", got)
	}
}

func TestRebateIsUnrestricted(t *testing.T) {
	// B1 can only pay for "gift"; the cashback it earns is unrestricted
	// and can pay for a "coupon" reward in the next period.
	plan := solve(t, Input{
		Periods: 2,
		Batches: []Batch{{ID: "B1", Source: "s", Amount: 100, Expiry: 1, Categories: []string{"gift"}}},
		Rewards: []Reward{
			{ID: "R1", Category: "gift", Cost: 100, Value: 10, Cashback: 50, Inventory: 1},
			{ID: "R2", Category: "coupon", Cost: 50, Value: 30, Inventory: 1},
		},
	})
	if plan.TotalValue != 40 {
		t.Fatalf("TotalValue = %d, want 40", plan.TotalValue)
	}
	if got := deductAmount(plan.Periods[1], RebateID); got != 50 {
		t.Fatalf("rebate deducted in period 2 = %d, want 50", got)
	}
}

func TestOptimalRewardChoice(t *testing.T) {
	plan := solve(t, Input{
		Periods: 1,
		Batches: []Batch{{ID: "B1", Source: "s", Amount: 100, Expiry: 1}},
		Rewards: []Reward{
			{ID: "R1", Category: "c", Cost: 100, Value: 150, Inventory: 1},
			{ID: "R2", Category: "c", Cost: 50, Value: 60, Inventory: 2},
		},
	})
	if plan.TotalValue != 150 {
		t.Fatalf("TotalValue = %d, want 150", plan.TotalValue)
	}
	if len(plan.Periods[0].Redemptions) != 1 || plan.Periods[0].Redemptions[0].RewardID != "R1" {
		t.Fatalf("expected only R1 redeemed, got %+v", plan.Periods[0].Redemptions)
	}
}

func TestValueTieBreakPrefersSpending(t *testing.T) {
	// Both plans reach value 10; spending 100 leaves less to expire.
	plan := solve(t, Input{
		Periods: 1,
		Batches: []Batch{{ID: "B1", Source: "s", Amount: 200, Expiry: 1}},
		Rewards: []Reward{
			{ID: "R1", Category: "c", Cost: 50, Value: 10, Inventory: 1},
			{ID: "R2", Category: "c", Cost: 50, Value: 0, Inventory: 1},
		},
	})
	if plan.TotalValue != 10 {
		t.Fatalf("TotalValue = %d, want 10", plan.TotalValue)
	}
	if plan.TotalCost != 100 {
		t.Fatalf("TotalCost = %d, want 100 (tie-break should prefer spending more)", plan.TotalCost)
	}
	if plan.TotalExpired != 100 {
		t.Fatalf("TotalExpired = %d, want 100", plan.TotalExpired)
	}
}

func TestValidation(t *testing.T) {
	cases := []Input{
		{Periods: 0},
		{Periods: 1, Batches: []Batch{{ID: "B1", Amount: 10, Expiry: 2}}},
		{Periods: 1, Batches: []Batch{{ID: "B1", Amount: -5, Expiry: 1}}},
		{Periods: 1, Batches: []Batch{{ID: "B1", Amount: 1, Expiry: 1}, {ID: "B1", Amount: 1, Expiry: 1}}},
		{Periods: 1, Rewards: []Reward{{ID: "R1", Cost: 1, Inventory: -1}}},
	}
	for i, in := range cases {
		if _, err := NewSolver(in); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}
