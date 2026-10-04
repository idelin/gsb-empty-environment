package planner

import (
	"errors"
	"strconv"
	"strings"
)

// ErrStateOverflow is returned when the memoized search exceeds the
// state budget, which can happen for very large inputs.
var ErrStateOverflow = errors.New("planner: state space budget exceeded")

// class is a spendable point class: one per batch plus the rebate pool.
type class struct {
	id      string
	source  string
	expiry  int
	allCats bool
	cats    map[string]bool
}

func (c class) allows(category string) bool {
	return c.allCats || c.cats[category]
}

// decision records the chosen action at a slot: skip, or redeem one unit
// paying from the given class.
type decision struct {
	take  bool
	class int
}

// result is the DP objective: total value, with total points spent as a
// tie-breaker (spending more means less is lost to expiry).
type result struct {
	value int
	spent int
}

func better(a, b result) bool {
	return a.value > b.value || (a.value == b.value && a.spent > b.spent)
}

// Solver computes an optimal redemption plan via memoized search over
// (period, reward slot, holdings, pending cashback) states.
type Solver struct {
	periods   int
	rewards   []Reward
	classes   []class
	initial   []int
	slots     []int // one slot per redeemable unit; value is the reward index
	rebateIdx int

	maxStates int
	states    int
	memo      map[string]result
	choice    map[string]decision
}

// NewSolver validates the input and prepares a Solver.
func NewSolver(in Input) (*Solver, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	s := &Solver{
		periods:   in.Periods,
		rewards:   in.Rewards,
		maxStates: 4000000,
		memo:      make(map[string]result),
		choice:    make(map[string]decision),
	}
	for _, b := range in.Batches {
		cl := class{id: b.ID, source: b.Source, expiry: b.Expiry, allCats: len(b.Categories) == 0}
		if !cl.allCats {
			cl.cats = make(map[string]bool, len(b.Categories))
			for _, c := range b.Categories {
				cl.cats[c] = true
			}
		}
		s.classes = append(s.classes, cl)
		s.initial = append(s.initial, b.Amount)
	}
	// The rebate pool is unrestricted and expires at the end of the horizon.
	s.rebateIdx = len(s.classes)
	s.classes = append(s.classes, class{id: RebateID, source: "rebate", expiry: in.Periods, allCats: true})
	s.initial = append(s.initial, 0)
	for i, r := range in.Rewards {
		for k := 0; k < r.Inventory; k++ {
			s.slots = append(s.slots, i)
		}
	}
	return s, nil
}

// Solve computes the optimal plan.
func (s *Solver) Solve() (*Plan, error) {
	if _, err := s.solvePeriod(1, s.initial); err != nil {
		return nil, err
	}
	return s.reconstruct(), nil
}

// solvePeriod returns the best result obtainable from period t onward,
// given holdings h at the start of period t (before expiry is applied).
func (s *Solver) solvePeriod(t int, h []int) (result, error) {
	if t > s.periods {
		return result{}, nil
	}
	h2 := make([]int, len(h))
	copy(h2, h)
	for c := range s.classes {
		if s.classes[c].expiry < t {
			h2[c] = 0
		}
	}
	k := stateKey(t, -1, 0, h2)
	if v, ok := s.memo[k]; ok {
		return v, nil
	}
	v, err := s.solveSlot(t, 0, h2, 0)
	if err != nil {
		return result{}, err
	}
	s.memo[k] = v
	return v, nil
}

// solveSlot returns the best result obtainable from slot j of period t
// onward. pending holds cashback earned earlier in period t; it is not
// spendable in this period and is credited to the rebate pool when the
// period ends.
func (s *Solver) solveSlot(t, j int, h []int, pending int) (result, error) {
	if j == len(s.slots) {
		h2 := make([]int, len(h))
		copy(h2, h)
		h2[s.rebateIdx] += pending
		return s.solvePeriod(t+1, h2)
	}
	k := stateKey(t, j, pending, h)
	if v, ok := s.memo[k]; ok {
		return v, nil
	}
	s.states++
	if s.states > s.maxStates {
		return result{}, ErrStateOverflow
	}
	// Option 1: skip this unit.
	best, err := s.solveSlot(t, j+1, h, pending)
	if err != nil {
		return result{}, err
	}
	bestDec := decision{}
	// Option 2: redeem this unit, paying from each eligible class in turn.
	r := s.rewards[s.slots[j]]
	for c := range s.classes {
		if h[c] < r.Cost || !s.classes[c].allows(r.Category) {
			continue
		}
		h[c] -= r.Cost
		sub, err := s.solveSlot(t, j+1, h, pending+r.Cashback)
		h[c] += r.Cost
		if err != nil {
			return result{}, err
		}
		cand := result{value: sub.value + r.Value, spent: sub.spent + r.Cost}
		if better(cand, best) {
			best = cand
			bestDec = decision{take: true, class: c}
		}
	}
	s.memo[k] = best
	s.choice[k] = bestDec
	return best, nil
}

// reconstruct walks the stored optimal decisions and builds the plan.
func (s *Solver) reconstruct() *Plan {
	plan := &Plan{Periods: make([]PeriodPlan, 0, s.periods)}
	h := make([]int, len(s.initial))
	copy(h, s.initial)
	for t := 1; t <= s.periods; t++ {
		pp := PeriodPlan{Period: t}
		// Points whose last usable period has passed expire now.
		for c := range s.classes {
			if s.classes[c].expiry < t && h[c] > 0 {
				pp.Expired = append(pp.Expired, Deduction{BatchID: s.classes[c].id, Source: s.classes[c].source, Amount: h[c]})
				plan.TotalExpired += h[c]
				h[c] = 0
			}
		}
		pending := 0
		ded := make(map[int]int)
		qty := make(map[int]int)
		for j := 0; j < len(s.slots); j++ {
			d := s.choice[stateKey(t, j, pending, h)]
			if !d.take {
				continue
			}
			r := s.rewards[s.slots[j]]
			h[d.class] -= r.Cost
			pending += r.Cashback
			ded[d.class] += r.Cost
			qty[s.slots[j]]++
			plan.TotalValue += r.Value
			plan.TotalCost += r.Cost
		}
		for i, r := range s.rewards {
			if qty[i] > 0 {
				pp.Redemptions = append(pp.Redemptions, Redemption{
					RewardID: r.ID,
					Quantity: qty[i],
					Cost:     qty[i] * r.Cost,
					Value:    qty[i] * r.Value,
					Cashback: qty[i] * r.Cashback,
				})
			}
		}
		for c := range s.classes {
			if ded[c] > 0 {
				pp.Deductions = append(pp.Deductions, Deduction{BatchID: s.classes[c].id, Source: s.classes[c].source, Amount: ded[c]})
			}
		}
		pp.CashbackEarned = pending
		plan.TotalCashback += pending
		h[s.rebateIdx] += pending
		plan.Periods = append(plan.Periods, pp)
	}
	// Anything left at the end of the horizon expires.
	for c := range s.classes {
		if h[c] > 0 {
			last := &plan.Periods[len(plan.Periods)-1]
			last.Expired = append(last.Expired, Deduction{BatchID: s.classes[c].id, Source: s.classes[c].source, Amount: h[c]})
			plan.TotalExpired += h[c]
		}
	}
	return plan
}

func stateKey(t, j, pending int, h []int) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(t))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(j))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(pending))
	for _, v := range h {
		b.WriteByte(',')
		b.WriteString(strconv.Itoa(v))
	}
	return b.String()
}
