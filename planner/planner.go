// Package planner implements a points redemption planner.
//
// Model:
//   - Time is divided into periods 1..N.
//   - Points are held in batches. Each batch has an amount, an expiry
//     period (the last period in which its points may be spent) and an
//     optional list of reward categories its points are allowed to pay
//     for (empty means unrestricted).
//   - Each reward has a per-period inventory, a point cost, a redemption
//     value and a cashback amount. Cashback is credited to a rebate pool
//     at the END of the period and can only be spent from the NEXT period
//     on, so same-period redemption cycling is impossible by construction.
//   - The rebate pool is unrestricted and expires at the end of the
//     horizon; leftover rebate points are reported as expiry loss.
//
// The solver computes a plan that maximizes the total redemption value.
// Ties are broken by preferring plans that spend more points (i.e. waste
// less to expiry).
package planner

import (
	"errors"
	"fmt"
)

// RebateID identifies the rebate pool in deduction/expiry reports.
const RebateID = "REBATE"

// Batch is a group of points from one source with a common expiry and
// usage restriction.
type Batch struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Amount int    `json:"amount"`
	// Expiry is the last period (1-based) in which the points can be used.
	Expiry int `json:"expiry"`
	// Categories lists the reward categories this batch may pay for.
	// Empty means the points are unrestricted.
	Categories []string `json:"categories,omitempty"`
}

// Reward is a redeemable item.
type Reward struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Cost     int    `json:"cost"`
	Value    int    `json:"value"`
	// Cashback points are returned at the end of the period and become
	// spendable only from the next period.
	Cashback int `json:"cashback"`
	// Inventory is the number of units redeemable per period.
	Inventory int `json:"inventory"`
}

// Input describes a planning problem.
type Input struct {
	Periods int      `json:"periods"`
	Batches []Batch  `json:"batches"`
	Rewards []Reward `json:"rewards"`
}

// Deduction reports points removed from a batch, either spent or expired.
type Deduction struct {
	BatchID string `json:"batchId"`
	Source  string `json:"source,omitempty"`
	Amount  int    `json:"amount"`
}

// Redemption reports redeemed units of one reward within one period.
type Redemption struct {
	RewardID string `json:"rewardId"`
	Quantity int    `json:"quantity"`
	Cost     int    `json:"cost"`
	Value    int    `json:"value"`
	Cashback int    `json:"cashback"`
}

// PeriodPlan reports what happened in one period.
type PeriodPlan struct {
	Period         int          `json:"period"`
	Redemptions    []Redemption `json:"redemptions,omitempty"`
	Deductions     []Deduction  `json:"deductions,omitempty"`
	CashbackEarned int          `json:"cashbackEarned"`
	// Expired lists points that became unusable at the start of this
	// period; for the final period it also includes points left over at
	// the end of the horizon.
	Expired []Deduction `json:"expired,omitempty"`
}

// Plan is the result of solving.
type Plan struct {
	TotalValue    int          `json:"totalValue"`
	TotalCost     int          `json:"totalCost"`
	TotalCashback int          `json:"totalCashback"`
	TotalExpired  int          `json:"totalExpired"`
	Periods       []PeriodPlan `json:"periods"`
}

// Validate checks the input for consistency.
func (in Input) Validate() error {
	if in.Periods < 1 {
		return errors.New("planner: periods must be >= 1")
	}
	seen := make(map[string]bool)
	for _, b := range in.Batches {
		if b.ID == "" {
			return errors.New("planner: batch id must not be empty")
		}
		if b.ID == RebateID {
			return fmt.Errorf("planner: batch id %q is reserved", RebateID)
		}
		if seen[b.ID] {
			return fmt.Errorf("planner: duplicate batch id %q", b.ID)
		}
		seen[b.ID] = true
		if b.Amount < 0 {
			return fmt.Errorf("planner: batch %q: negative amount", b.ID)
		}
		if b.Expiry < 1 || b.Expiry > in.Periods {
			return fmt.Errorf("planner: batch %q: expiry %d outside [1, %d]", b.ID, b.Expiry, in.Periods)
		}
	}
	seen = make(map[string]bool)
	for _, r := range in.Rewards {
		if r.ID == "" {
			return errors.New("planner: reward id must not be empty")
		}
		if seen[r.ID] {
			return fmt.Errorf("planner: duplicate reward id %q", r.ID)
		}
		seen[r.ID] = true
		if r.Cost < 0 || r.Value < 0 || r.Cashback < 0 {
			return fmt.Errorf("planner: reward %q: negative cost/value/cashback", r.ID)
		}
		if r.Inventory < 0 {
			return fmt.Errorf("planner: reward %q: negative inventory", r.ID)
		}
	}
	return nil
}
