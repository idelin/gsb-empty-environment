// Package verify independently checks results produced by package
// groupopt. It re-implements evaluation, relaxation bounds and exhaustive
// enumeration from scratch, and validates every reported bound against
// the committed closure of the epoch the result claims to belong to.
// Certificates from older epochs are rejected outright: an optimality
// proof is never carried across a checkpoint revision.
package verify

import (
	"fmt"
	"sort"

	"groupopt"
)

// model is the verifier's own compiled view of an instance. Bit i of a
// mask refers to inst.Options[i], matching the solver's certificate.
type model struct {
	inst     groupopt.Instance
	n        int
	index    map[int]int
	groupOf  []int
	fee      map[int]int64
	groupNum map[int]int
	feeByNum []int64
	reqMask  []uint64
}

func newModel(inst groupopt.Instance) (*model, error) {
	if err := inst.Validate(); err != nil {
		return nil, err
	}
	m := &model{inst: inst, n: len(inst.Options)}
	m.index = make(map[int]int, m.n)
	m.groupOf = make([]int, m.n)
	m.fee = make(map[int]int64, len(inst.SetupFee))
	for g, f := range inst.SetupFee {
		m.fee[g] = f
	}
	m.groupNum = make(map[int]int)
	for i, o := range inst.Options {
		m.index[o.ID] = i
		m.groupOf[i] = o.Group
		if _, ok := m.groupNum[o.Group]; !ok {
			m.groupNum[o.Group] = len(m.groupNum)
		}
	}
	m.feeByNum = make([]int64, len(m.groupNum))
	for g, num := range m.groupNum {
		m.feeByNum[num] = m.fee[g]
	}
	m.reqMask = make([]uint64, m.n)
	for i, o := range inst.Options {
		for _, r := range o.Requires {
			m.reqMask[i] |= 1 << uint(m.index[r])
		}
	}
	return m, nil
}

// evalMask recomputes cost and profit of a bitmask selection. ok is false
// if the mask is not dependency-closed.
func (m *model) evalMask(mask, paidGroups uint64) (cost, profit int64, ok bool) {
	var groups uint64
	for i := 0; i < m.n; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		if m.reqMask[i]&^mask != 0 {
			return 0, 0, false
		}
		o := m.inst.Options[i]
		cost += o.Cost
		profit += o.Profit
		groups |= 1 << uint(m.groupNum[o.Group])
	}
	groups |= paidGroups
	for groups != 0 {
		num := uint(trailingZeros64(groups))
		groups &= groups - 1
		cost += m.feeByNum[num]
	}
	return cost, profit, true
}

func trailingZeros64(x uint64) int {
	n := 0
	for x&1 == 0 {
		x >>= 1
		n++
	}
	return n
}

// closureOf returns the transitive Requires-closure of a selection, or
// false if the selection references an unknown option.
func (m *model) closureOf(sel map[int]bool) (map[int]bool, bool) {
	out := make(map[int]bool, len(sel))
	stack := make([]int, 0, len(sel))
	for id := range sel {
		if _, ok := m.index[id]; !ok {
			return nil, false
		}
		out[id] = true
		stack = append(stack, id)
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, r := range m.inst.Options[m.index[id]].Requires {
			if !out[r] {
				out[r] = true
				stack = append(stack, r)
			}
		}
	}
	return out, true
}

// Evaluate recomputes cost and profit of a selection from scratch. The
// shared setup fee is charged exactly once per used group, and fees of
// paid (sunk) groups are always counted. ok is false if the selection is
// not dependency-closed or references unknown options.
func Evaluate(inst groupopt.Instance, sel, paid map[int]bool) (cost, profit int64, ok bool) {
	m, err := newModel(inst)
	if err != nil {
		return 0, 0, false
	}
	closed, ok := m.closureOf(sel)
	if !ok {
		return 0, 0, false
	}
	for id := range closed {
		if !sel[id] {
			return 0, 0, false // missing a required option
		}
	}
	usedGroups := make(map[int]bool)
	for id := range sel {
		o := inst.Options[m.index[id]]
		cost += o.Cost
		profit += o.Profit
		usedGroups[o.Group] = true
	}
	for g := range usedGroups {
		cost += m.fee[g]
	}
	for g := range paid {
		if _, known := m.fee[g]; !known {
			return 0, 0, false
		}
		if usedGroups[g] {
			continue // already charged once via the used group
		}
		cost += m.fee[g] // sunk setup fee, charged once, non-refundable
	}
	return cost, profit, true
}

// partialCost recomputes cost and profit of a partial (not necessarily
// dependency-closed) selection, as used by frontier nodes. Shared fees
// are charged once per used-or-paid group.
func (m *model) partialCost(sel map[int]bool, paid map[int]bool) (cost, profit int64, ok bool) {
	usedGroups := make(map[int]bool)
	for id := range sel {
		i, known := m.index[id]
		if !known {
			return 0, 0, false
		}
		o := m.inst.Options[i]
		cost += o.Cost
		profit += o.Profit
		usedGroups[o.Group] = true
	}
	for g := range usedGroups {
		cost += m.fee[g]
	}
	for g := range paid {
		if _, known := m.fee[g]; !known {
			return 0, 0, false
		}
		if !usedGroups[g] {
			cost += m.fee[g]
		}
	}
	return cost, profit, true
}

// better implements the canonical tie-break: profit desc, cost asc, then
// lexicographically smaller sorted id set.
func better(profitA, costA int64, idsA []int, profitB, costB int64, idsB []int) bool {
	if profitA != profitB {
		return profitA > profitB
	}
	if costA != costB {
		return costA < costB
	}
	for i := 0; i < len(idsA) && i < len(idsB); i++ {
		if idsA[i] != idsB[i] {
			return idsA[i] < idsB[i]
		}
	}
	return len(idsA) < len(idsB)
}

// BruteForce enumerates all 2^n subsets, keeps those containing the
// committed closure, dependency-closed and within budget, and returns
// the best one under the canonical tie-break. This is the ground truth
// every solver result is checked against.
func BruteForce(inst groupopt.Instance, budget int64, committed, paid map[int]bool) (best map[int]bool, profit, cost int64, ok bool) {
	m, err := newModel(inst)
	if err != nil {
		return nil, 0, 0, false
	}
	base, ok := m.closureOf(committed)
	if !ok {
		return nil, 0, 0, false
	}
	baseMask := uint64(0)
	for id := range base {
		baseMask |= 1 << uint(m.index[id])
	}
	paidMask := uint64(0)
	for g := range paid {
		num, known := m.groupNum[g]
		if !known {
			return nil, 0, 0, false
		}
		paidMask |= 1 << uint(num)
	}
	n := m.n
	var bestMask uint64
	var bestIDs []int
	found := false
	for mask := uint64(0); mask < (uint64(1) << uint(n)); mask++ {
		if mask&baseMask != baseMask {
			continue
		}
		c, p, feasible := m.evalMask(mask, paidMask)
		if !feasible || c > budget {
			continue
		}
		ids := make([]int, 0, n)
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) != 0 {
				ids = append(ids, inst.Options[i].ID)
			}
		}
		sort.Ints(ids)
		if !found || better(p, c, ids, profit, cost, bestIDs) {
			found = true
			bestMask = mask
			profit, cost = p, c
			bestIDs = ids
		}
	}
	if !found {
		return nil, 0, 0, false
	}
	best = make(map[int]bool, n)
	for i := 0; i < n; i++ {
		if bestMask&(1<<uint(i)) != 0 {
			best[inst.Options[i].ID] = true
		}
	}
	return best, profit, cost, true
}

// relaxBound is the verifier's own fractional-knapsack bound over the
// undecided positions, ignoring setup fees and dependencies.
func (m *model) relaxBound(undecided uint64, capacity int64) int64 {
	if capacity < 0 {
		return 0
	}
	type item struct {
		i    int
		p, c int64
	}
	items := make([]item, 0, m.n)
	for i := 0; i < m.n; i++ {
		if undecided&(1<<uint(i)) == 0 {
			continue
		}
		o := m.inst.Options[i]
		if o.Profit > 0 {
			items = append(items, item{i, o.Profit, o.Cost})
		}
	}
	sort.Slice(items, func(a, b int) bool {
		x, y := items[a], items[b]
		if x.c == 0 && y.c == 0 {
			return x.i < y.i
		}
		if x.c == 0 {
			return true
		}
		if y.c == 0 {
			return false
		}
		if x.p*y.c != y.p*x.c {
			return x.p*y.c > y.p*x.c
		}
		return x.i < y.i
	})
	var bound int64
	rem := capacity
	for _, it := range items {
		if it.c == 0 {
			bound += it.p
			continue
		}
		if it.c <= rem {
			bound += it.p
			rem -= it.c
			continue
		}
		bound += it.p * rem / it.c
		break
	}
	return bound
}

// CheckResult validates res against the instance, committed closure and
// epoch given by st. It confirms:
//
//   - the certificate belongs to the current epoch (no stale proofs);
//   - the lower-bound witness is dependency-closed, contains the
//     committed closure, respects the budget, and its recomputed cost and
//     profit match the report (shared fees charged once, sunk fees of
//     paid groups counted);
//   - every frontier node in the certificate is consistent with the
//     committed closure, and its bound recomputes exactly under the
//     verifier's own relaxation, so Upper is a genuine upper bound;
//   - for Exact results, exhaustive enumeration confirms Lower is
//     optimal and the witness wins the tie-break;
//   - for Infeasible results, exhaustive enumeration confirms no feasible
//     selection exists.
func CheckResult(inst groupopt.Instance, st groupopt.State, res groupopt.Result) error {
	m, err := newModel(inst)
	if err != nil {
		return err
	}
	if res.Cert == nil {
		return fmt.Errorf("verify: missing certificate")
	}
	if res.Epoch != st.Epoch || res.Cert.Epoch != st.Epoch {
		return fmt.Errorf("verify: stale proof: result epoch %d, cert epoch %d, current epoch %d",
			res.Epoch, res.Cert.Epoch, st.Epoch)
	}
	committed := make(map[int]bool, len(st.Committed))
	for _, id := range st.Committed {
		committed[id] = true
	}
	paid := make(map[int]bool, len(st.PaidGroups))
	for _, g := range st.PaidGroups {
		paid[g] = true
	}
	closure, ok := m.closureOf(committed)
	if !ok {
		return fmt.Errorf("verify: committed set references unknown option")
	}
	full := uint64(0)
	for i := 0; i < m.n; i++ {
		full |= 1 << uint(i)
	}
	closureMask := uint64(0)
	for id := range closure {
		closureMask |= 1 << uint(m.index[id])
	}

	switch res.Status {
	case groupopt.Infeasible:
		if res.HasSel {
			return fmt.Errorf("verify: infeasible result carries a selection")
		}
		if _, _, _, ok := BruteForce(inst, st.Budget, committed, paid); ok {
			return fmt.Errorf("verify: result claims infeasible but enumeration found a feasible selection")
		}
		return nil

	case groupopt.Exact, groupopt.Truncated:
		if !res.HasSel {
			return fmt.Errorf("verify: %v result without a witness selection", res.Status)
		}
	default:
		return fmt.Errorf("verify: unknown status %d", int(res.Status))
	}

	// Lower bound: re-evaluate the witness independently.
	sel := make(map[int]bool, len(res.Selection))
	prev := -1
	first := true
	for _, id := range res.Selection {
		if _, known := m.index[id]; !known {
			return fmt.Errorf("verify: witness contains unknown option %d", id)
		}
		if sel[id] {
			return fmt.Errorf("verify: witness lists option %d twice", id)
		}
		if !first && id <= prev {
			return fmt.Errorf("verify: witness ids not sorted ascending")
		}
		first = false
		prev = id
		sel[id] = true
	}
	for id := range closure {
		if !sel[id] {
			return fmt.Errorf("verify: witness omits committed option %d (closure not preserved)", id)
		}
	}
	cost, profit, ok := Evaluate(inst, sel, paid)
	if !ok {
		return fmt.Errorf("verify: witness is not dependency-closed")
	}
	if cost != res.Cost {
		return fmt.Errorf("verify: witness cost %d, result claims %d", cost, res.Cost)
	}
	if profit != res.Lower {
		return fmt.Errorf("verify: witness profit %d, result claims lower bound %d", profit, res.Lower)
	}
	if cost > st.Budget {
		return fmt.Errorf("verify: witness cost %d exceeds budget %d", cost, st.Budget)
	}

	// Upper bound.
	if res.Upper < res.Lower {
		return fmt.Errorf("verify: upper bound %d below lower bound %d", res.Upper, res.Lower)
	}
	if res.Status == groupopt.Truncated {
		if len(res.Cert.Frontier) == 0 {
			return fmt.Errorf("verify: truncated result with empty frontier certificate")
		}
		ub := res.Lower
		for k, fn := range res.Cert.Frontier {
			if fn.Sel&^fn.Decided != 0 {
				return fmt.Errorf("verify: frontier node %d selects undecided positions", k)
			}
			if fn.Decided&^full != 0 {
				return fmt.Errorf("verify: frontier node %d decides positions outside the instance", k)
			}
			if fn.Sel&closureMask != closureMask {
				return fmt.Errorf("verify: frontier node %d drops the committed closure", k)
			}
			// Partial dependency consistency: nothing selected may
			// require an already-excluded position.
			for i := 0; i < m.n; i++ {
				if fn.Sel&(1<<uint(i)) == 0 {
					continue
				}
				for _, r := range m.inst.Options[i].Requires {
					bit := uint64(1) << uint(m.index[r])
					if fn.Decided&bit != 0 && fn.Sel&bit == 0 {
						return fmt.Errorf("verify: frontier node %d excludes a required option", k)
					}
				}
			}
			nodeSel := make(map[int]bool, m.n)
			for i := 0; i < m.n; i++ {
				if fn.Sel&(1<<uint(i)) != 0 {
					nodeSel[m.inst.Options[i].ID] = true
				}
			}
			nc, np, ok := m.partialCost(nodeSel, paid)
			if !ok {
				return fmt.Errorf("verify: frontier node %d selection invalid", k)
			}
			recomputed := np + m.relaxBound(^fn.Decided&full, st.Budget-nc)
			if recomputed != fn.Bound {
				return fmt.Errorf("verify: frontier node %d bound %d, recomputed %d", k, fn.Bound, recomputed)
			}
			if fn.Bound > ub {
				ub = fn.Bound
			}
		}
		if res.Upper != ub {
			return fmt.Errorf("verify: upper bound %d does not match frontier maximum %d", res.Upper, ub)
		}
		return nil
	}

	// Exact: no frontier may remain, the bounds must meet, and exhaustive
	// enumeration must confirm the optimum and the tie-break winner.
	if len(res.Cert.Frontier) != 0 {
		return fmt.Errorf("verify: exact result carries a frontier")
	}
	if res.Upper != res.Lower {
		return fmt.Errorf("verify: exact result with upper %d != lower %d", res.Upper, res.Lower)
	}
	best, bp, bc, ok := BruteForce(inst, st.Budget, committed, paid)
	if !ok {
		return fmt.Errorf("verify: exact result but enumeration finds nothing feasible")
	}
	if bp != res.Lower {
		return fmt.Errorf("verify: optimum %d by enumeration, result claims %d", bp, res.Lower)
	}
	if bc != res.Cost {
		return fmt.Errorf("verify: optimal cost %d by enumeration, result claims %d", bc, res.Cost)
	}
	if len(best) != len(sel) {
		return fmt.Errorf("verify: witness is not the tie-break winner")
	}
	for id := range best {
		if !sel[id] {
			return fmt.Errorf("verify: witness is not the tie-break winner")
		}
	}
	return nil
}
