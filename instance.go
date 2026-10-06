// Package groupopt solves budget-constrained selection problems with
// per-group setup fees and inter-option dependencies.
//
// Model: up to MaxOptions candidate options each consume budget and yield
// profit. Selecting any option of a group charges that group's one-time
// setup fee (shared fee, charged once no matter how many options of the
// group are selected). An option may require other options to be selected
// as well; feasible selections are closed under the Requires relation.
//
// The objective is maximum total profit within budget. Ties are broken by
// lower total cost, then by the lexicographically smaller sorted id set.
package groupopt

import (
	"fmt"
	"sort"
)

// MaxOptions is the maximum number of candidate options.
const MaxOptions = 20

// Option is a single candidate.
type Option struct {
	ID       int   // unique option id
	Group    int   // group the option belongs to
	Cost     int64 // budget consumed when selected
	Profit   int64 // benefit when selected
	Requires []int // ids that must also be selected if this option is
}

// Instance is a selection problem.
type Instance struct {
	Options  []Option
	SetupFee map[int]int64 // group id -> one-time fee, charged if the group is used
}

// Validate checks structural constraints of the instance.
func (in Instance) Validate() error {
	n := len(in.Options)
	if n == 0 {
		return fmt.Errorf("groupopt: no options")
	}
	if n > MaxOptions {
		return fmt.Errorf("groupopt: %d options exceeds maximum %d", n, MaxOptions)
	}
	seen := make(map[int]bool, n)
	groups := make(map[int]bool)
	for _, o := range in.Options {
		if seen[o.ID] {
			return fmt.Errorf("groupopt: duplicate option id %d", o.ID)
		}
		seen[o.ID] = true
		if o.Cost < 0 {
			return fmt.Errorf("groupopt: option %d has negative cost", o.ID)
		}
		if o.Group < 0 {
			return fmt.Errorf("groupopt: option %d has negative group id", o.ID)
		}
		groups[o.Group] = true
	}
	if len(groups) > 63 {
		return fmt.Errorf("groupopt: %d distinct groups exceeds maximum 63", len(groups))
	}
	for _, o := range in.Options {
		for _, r := range o.Requires {
			if r == o.ID {
				return fmt.Errorf("groupopt: option %d requires itself", o.ID)
			}
			if !seen[r] {
				return fmt.Errorf("groupopt: option %d requires unknown option %d", o.ID, r)
			}
		}
	}
	for g, f := range in.SetupFee {
		if f < 0 {
			return fmt.Errorf("groupopt: group %d has negative setup fee", g)
		}
		if !groups[g] {
			return fmt.Errorf("groupopt: setup fee declared for unknown group %d", g)
		}
	}
	return nil
}

// compiled is the internal bitmask form of an Instance. Bit i of a
// selection mask refers to Options[i].
type compiled struct {
	n          int
	ids        []int
	index      map[int]int // option id -> position
	cost       []int64
	profit     []int64
	groupOf    []int       // option position -> group number
	groupNum   map[int]int // group id -> group number
	fee        []int64     // group number -> fee
	reqMask    []uint64    // option position -> bitmask of required positions
	requiredBy []uint64    // option position -> bitmask of positions requiring it
	ratioOrder []int       // positions sorted by profit/cost ratio, descending
	full       uint64      // mask of all n positions
}

func compile(in Instance) *compiled {
	n := len(in.Options)
	c := &compiled{
		n:          n,
		ids:        make([]int, n),
		index:      make(map[int]int, n),
		cost:       make([]int64, n),
		profit:     make([]int64, n),
		groupOf:    make([]int, n),
		groupNum:   make(map[int]int),
		reqMask:    make([]uint64, n),
		requiredBy: make([]uint64, n),
	}
	groupIDs := make([]int, 0, len(in.SetupFee))
	seenGroup := make(map[int]bool)
	for _, o := range in.Options {
		if !seenGroup[o.Group] {
			seenGroup[o.Group] = true
			groupIDs = append(groupIDs, o.Group)
		}
	}
	sort.Ints(groupIDs)
	for num, g := range groupIDs {
		c.groupNum[g] = num
	}
	c.fee = make([]int64, len(groupIDs))
	for g, num := range c.groupNum {
		c.fee[num] = in.SetupFee[g]
	}
	for i, o := range in.Options {
		c.ids[i] = o.ID
		c.index[o.ID] = i
		c.cost[i] = o.Cost
		c.profit[i] = o.Profit
		c.groupOf[i] = c.groupNum[o.Group]
	}
	for i, o := range in.Options {
		for _, r := range o.Requires {
			j := c.index[r]
			c.reqMask[i] |= 1 << uint(j)
			c.requiredBy[j] |= 1 << uint(i)
		}
	}
	for i := 0; i < n; i++ {
		c.full |= 1 << uint(i)
	}
	c.sortByRatio()
	return c
}

// sortByRatio orders positions by profit/cost ratio descending; zero-cost
// profitable options sort first. Used for relaxation bounds and branching.
func (c *compiled) sortByRatio() {
	order := make([]int, c.n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		i, j := order[a], order[b]
		pi, pj := c.profit[i], c.profit[j]
		ci, cj := c.cost[i], c.cost[j]
		if pi <= 0 && pj <= 0 {
			return i < j
		}
		if pi <= 0 {
			return false
		}
		if pj <= 0 {
			return true
		}
		if ci == 0 && cj == 0 {
			return i < j
		}
		if ci == 0 {
			return true
		}
		if cj == 0 {
			return false
		}
		if pi*cj != pj*ci {
			return pi*cj > pj*ci
		}
		return i < j
	})
	c.ratioOrder = order
}

// closure returns the smallest superset of mask closed under Requires.
func (c *compiled) closure(mask uint64) uint64 {
	for {
		grown := mask
		m := mask
		for m != 0 {
			i := uint(trailingZeros(m))
			m &= m - 1
			grown |= c.reqMask[i]
		}
		if grown == mask {
			return mask
		}
		mask = grown
	}
}

// closed reports whether mask satisfies all Requires edges.
func (c *compiled) closed(mask uint64) bool {
	m := mask
	for m != 0 {
		i := uint(trailingZeros(m))
		m &= m - 1
		if c.reqMask[i]&^mask != 0 {
			return false
		}
	}
	return true
}

// groupsOf returns the group-number bitmask used by a selection.
func (c *compiled) groupsOf(sel uint64) uint64 {
	var g uint64
	m := sel
	for m != 0 {
		i := uint(trailingZeros(m))
		m &= m - 1
		g |= 1 << uint(c.groupOf[i])
	}
	return g
}

// costOf computes total spend: option costs plus one fee per group that is
// either used by sel or already paid (sunk, non-refundable).
func (c *compiled) costOf(sel, paidGroups uint64) int64 {
	var total int64
	m := sel
	for m != 0 {
		i := uint(trailingZeros(m))
		m &= m - 1
		total += c.cost[i]
	}
	g := c.groupsOf(sel) | paidGroups
	for g != 0 {
		num := uint(trailingZeros(g))
		g &= g - 1
		total += c.fee[num]
	}
	return total
}

// profitOf sums profits of a selection.
func (c *compiled) profitOf(sel uint64) int64 {
	var total int64
	m := sel
	for m != 0 {
		i := uint(trailingZeros(m))
		m &= m - 1
		total += c.profit[i]
	}
	return total
}

// relax is the Dantzig bound: the fractional-knapsack optimum over
// undecided positions with the given remaining capacity, ignoring setup
// fees and dependencies. It is a valid upper bound on the additional
// profit any completion of the partial state can achieve.
func (c *compiled) relax(undecided uint64, capacity int64) int64 {
	if capacity < 0 {
		return 0
	}
	var bound int64
	rem := capacity
	for _, i := range c.ratioOrder {
		if undecided&(1<<uint(i)) == 0 {
			continue
		}
		p, co := c.profit[i], c.cost[i]
		if p <= 0 {
			continue
		}
		if co == 0 {
			bound += p
			continue
		}
		if co <= rem {
			bound += p
			rem -= co
			continue
		}
		// Fractional take of the critical item; the floor keeps the
		// bound integral and still valid because profits are integers.
		bound += p * rem / co
		break
	}
	return bound
}

// idList returns the sorted option ids of a selection mask.
func (c *compiled) idList(mask uint64) []int {
	out := make([]int, 0, c.n)
	m := mask
	for m != 0 {
		i := uint(trailingZeros(m))
		m &= m - 1
		out = append(out, c.ids[i])
	}
	sort.Ints(out)
	return out
}

// lessIDs compares two sorted id lists lexicographically; a strict prefix
// is smaller than its extension.
func lessIDs(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// maskLess compares two selection masks by the id-set tie-break rule.
func (c *compiled) maskLess(a, b uint64) bool {
	return lessIDs(c.idList(a), c.idList(b))
}

func trailingZeros(x uint64) int {
	n := 0
	for x&1 == 0 {
		x >>= 1
		n++
	}
	return n
}
