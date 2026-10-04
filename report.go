package main

import (
	"fmt"
	"strings"
)

type Redemption struct {
	OfferID string `json:"offer_id"`
	Units   int    `json:"units"`
	Points  int    `json:"points"`
	Value   int    `json:"value"`
	Rebate  int    `json:"rebate"`
}

type Deduction struct {
	BatchID string `json:"batch_id"`
	OfferID string `json:"offer_id"`
	Amount  int    `json:"amount"`
}

type Arrival struct {
	BatchID      string `json:"batch_id"`
	Source       string `json:"source"`
	Amount       int    `json:"amount"`
	ExpirePeriod int    `json:"expire_period"`
}

type ExpireLoss struct {
	BatchID string `json:"batch_id"`
	Amount  int    `json:"amount"`
}

type PeriodResult struct {
	Period      int          `json:"period"`
	Arrivals    []Arrival    `json:"arrivals,omitempty"`
	Redemptions []Redemption `json:"redemptions,omitempty"`
	Deductions  []Deduction  `json:"deductions,omitempty"`
	Expired     []ExpireLoss `json:"expired,omitempty"`
}

type Result struct {
	TotalValue      int            `json:"total_value"`
	TotalSpent      int            `json:"total_spent"`
	TotalExpired    int            `json:"total_expired"`
	ForfeitedRebate int            `json:"forfeited_rebate"`
	Truncated       bool           `json:"truncated"`
	Periods         []PeriodResult `json:"periods"`
}

// Replay 沿最优决策重放一遍，产出每个时段的到账、兑换、
// 批次扣减与到期损失明细。
func (s *Solver) Replay() Result {
	res := Result{Truncated: s.truncated}
	var batches []stateBatch
	for _, b := range s.in.Batches {
		if b.Amount > 0 {
			batches = append(batches, stateBatch{b.ID, b.Source, b.Amount, b.ExpirePeriod})
		}
	}
	for t := 1; t <= s.in.Periods; t++ {
		batches = normalizeBatches(batches, t)
		pr := PeriodResult{Period: t}
		arrivalPrefix := fmt.Sprintf("rebate#%d:", t)
		for _, b := range batches {
			if strings.HasPrefix(b.id, arrivalPrefix) {
				pr.Arrivals = append(pr.Arrivals, Arrival{b.id, b.source, b.remaining, b.expiry})
			}
		}
		offers := s.offersByPeriod[t]
		units := s.memo[stateKey(t, batches)].move
		if units == nil {
			units = make([]int, len(offers))
		}
		ded, ok := pay(batches, offers, units)
		if !ok {
			ded = make([][]int, len(offers))
			for j := range ded {
				ded[j] = make([]int, len(batches))
			}
		}
		for j, o := range offers {
			if units[j] > 0 {
				pr.Redemptions = append(pr.Redemptions, Redemption{
					OfferID: o.ID,
					Units:   units[j],
					Points:  units[j] * o.Cost,
					Value:   units[j] * o.Value,
					Rebate:  units[j] * o.Rebate,
				})
				res.TotalValue += units[j] * o.Value
				res.TotalSpent += units[j] * o.Cost
			}
			for i := range batches {
				if ded[j][i] > 0 {
					pr.Deductions = append(pr.Deductions, Deduction{batches[i].id, o.ID, ded[j][i]})
				}
			}
		}
		next := applyDeductions(batches, ded)
		var survivors []stateBatch
		for _, b := range next {
			switch {
			case b.remaining <= 0:
			case b.expiry == t:
				pr.Expired = append(pr.Expired, ExpireLoss{b.id, b.remaining})
				res.TotalExpired += b.remaining
			default:
				survivors = append(survivors, b)
			}
		}
		if t+1 <= s.in.Periods {
			survivors = append(survivors, rebateBatches(t, offers, units)...)
		} else {
			for j, o := range offers {
				res.ForfeitedRebate += units[j] * o.Rebate
			}
		}
		res.Periods = append(res.Periods, pr)
		batches = survivors
	}
	return res
}
