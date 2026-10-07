// Package plansel selects a maximum-profit subset of candidate plans under a
// budget, where activating any plan of a group costs a one-time preparation
// fee for that group, and plans may require other plans to be selected first.
//
// Up to MaxPlans plans are supported. Among all feasible selections the
// solver maximizes total profit; ties are broken by smaller total cost, then
// by the lexicographically smaller sorted set of plan IDs.
//
// A Session additionally supports checkpoints: executed preparations and
// committed (irrevocable) plan selections are preserved across revisions of
// profits and budget, and their costs are sunk (never refunded, never
// charged against the revised budget again).
package plansel

import (
	"errors"
	"fmt"
)

// MaxPlans is the maximum number of candidate plans per instance.
const MaxPlans = 20

// Plan is a single candidate.
type Plan struct {
	ID       int   // unique identifier
	Group    int   // plans sharing a group share one preparation fee
	Cost     int64 // budget consumed when selected
	Profit   int64 // benefit gained when selected
	Requires []int // IDs of plans that must also be selected
}

// Instance is a full problem definition.
type Instance struct {
	Plans    []Plan
	GroupFee map[int]int64 // preparation fee per group, charged at most once
	Budget   int64
}

// Validate checks structural consistency of the instance.
func (inst Instance) Validate() error {
	if len(inst.Plans) == 0 {
		return errors.New("plansel: instance has no plans")
	}
	if len(inst.Plans) > MaxPlans {
		return fmt.Errorf("plansel: %d plans exceeds MaxPlans=%d", len(inst.Plans), MaxPlans)
	}
	if inst.Budget < 0 {
		return fmt.Errorf("plansel: negative budget %d", inst.Budget)
	}
	index := make(map[int]int, len(inst.Plans))
	for i, p := range inst.Plans {
		if _, dup := index[p.ID]; dup {
			return fmt.Errorf("plansel: duplicate plan ID %d", p.ID)
		}
		index[p.ID] = i
		if p.Cost < 0 {
			return fmt.Errorf("plansel: plan %d has negative cost %d", p.ID, p.Cost)
		}
	}
	for g, f := range inst.GroupFee {
		if f < 0 {
			return fmt.Errorf("plansel: group %d has negative fee %d", g, f)
		}
	}
	for _, p := range inst.Plans {
		for _, r := range p.Requires {
			if r == p.ID {
				return fmt.Errorf("plansel: plan %d requires itself", p.ID)
			}
			if _, ok := index[r]; !ok {
				return fmt.Errorf("plansel: plan %d requires unknown plan %d", p.ID, r)
			}
		}
	}
	// Dependency graph must be acyclic.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, len(inst.Plans))
	var visit func(i int) error
	visit = func(i int) error {
		color[i] = gray
		for _, r := range inst.Plans[i].Requires {
			j := index[r]
			switch color[j] {
			case gray:
				return fmt.Errorf("plansel: dependency cycle involving plan %d", inst.Plans[j].ID)
			case white:
				if err := visit(j); err != nil {
					return err
				}
			}
		}
		color[i] = black
		return nil
	}
	for i := range inst.Plans {
		if color[i] == white {
			if err := visit(i); err != nil {
				return err
			}
		}
	}
	return nil
}

// clone returns a deep copy so callers cannot mutate session state.
func (inst Instance) clone() Instance {
	out := Instance{
		Plans:  make([]Plan, len(inst.Plans)),
		Budget: inst.Budget,
	}
	for i, p := range inst.Plans {
		out.Plans[i] = p
		out.Plans[i].Requires = append([]int(nil), p.Requires...)
	}
	if inst.GroupFee != nil {
		out.GroupFee = make(map[int]int64, len(inst.GroupFee))
		for g, f := range inst.GroupFee {
			out.GroupFee[g] = f
		}
	}
	return out
}

// requiresOf returns the Requires list of the plan with the given ID.
func (inst Instance) requiresOf(id int) []int {
	for _, p := range inst.Plans {
		if p.ID == id {
			return p.Requires
		}
	}
	return nil
}
