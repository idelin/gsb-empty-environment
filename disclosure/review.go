package disclosure

import (
	"fmt"
	"slices"
	"sort"
)

// maxGroups keeps subset enumeration within a uint32 mask and makes the
// exhaustive group search trivially bounded for 4x4 tables.
const maxGroups = 20

const noSolution uint32 = 1<<32 - 1

// Review runs the disclosure review. Malformed input returns an error. A valid
// request that cannot be approved, including budget exhaustion or an actual
// table that violates always-published constraints, returns a non-nil Decision
// with Approved set to false and a re-checkable reason.
func Review(req *Request) (*Decision, error) {
	c, err := validate(req)
	if err != nil {
		return nil, err
	}
	if len(c.groups) > maxGroups {
		return nil, fmt.Errorf("disclosure: at most %d suppression groups are supported, got %d", maxGroups, len(c.groups))
	}

	base := &Decision{Budget: req.Budget}
	if id, reason := c.checkActual(); id != "" {
		base.Approved = false
		base.InconsistentConstraintID = id
		base.RejectedReason = reason
		return base, nil
	}

	g := len(c.groups)
	allMask := uint32(0)
	if g > 0 {
		allMask = uint32(1)<<uint(g) - 1
	}
	bestMask, bestBudgetMask, totalNodes, limitHit := c.search()
	if limitHit {
		return c.limitDecision(totalNodes), nil
	}

	// Reports always describe the strongest candidate publication set: every
	// group hidden. When even that leaks, the reports double as the
	// re-checkable exclusion evidence.
	reportMask := allMask
	if bestBudgetMask != noSolution {
		reportMask = bestBudgetMask
	}
	published := c.publishedFor(reportMask)
	full := c.enumerate(enumConfig{published: published, collect: true})
	totalNodes += full.nodes
	if full.limitHit {
		return c.limitDecision(totalNodes), nil
	}

	return c.buildDecision(bestMask, bestBudgetMask, reportMask, published, full, totalNodes), nil
}

// search walks every subset of groups. bestMask is the cheapest feasible
// subset ignoring the budget; bestBudgetMask additionally respects it.
// Hiding more groups only enlarges the feasible region, so an infeasible
// subset cannot prune its supersets; the only safe pruning is cost-based.
// Equal-cost ties compare sorted group ID sequences, so equal-cost subtrees
// (which may hide extra zero-cost groups) are still walked.
func (c *compiled) search() (bestMask, bestBudgetMask uint32, nodes int64, limitHit bool) {
	g := len(c.groups)
	bestCost := int64(1) << 62
	bestBudgetCost := int64(1) << 62
	bestMask, bestBudgetMask = noSolution, noSolution

	consider := func(mask uint32, cost int64) {
		if cost < bestCost || (cost == bestCost && c.maskLess(mask, bestMask)) {
			bestCost = cost
			bestMask = mask
		}
		withinBudget := c.req.Budget == 0 || cost <= c.req.Budget
		if withinBudget && (cost < bestBudgetCost || (cost == bestBudgetCost && c.maskLess(mask, bestBudgetMask))) {
			bestBudgetCost = cost
			bestBudgetMask = mask
		}
	}

	var dfs func(i int, mask uint32, cost int64)
	dfs = func(i int, mask uint32, cost int64) {
		// Strict bounds only: a descendant can hide additional zero-cost
		// groups and win an equal-cost tie with a lexicographically smaller
		// ID sequence, so equal-cost subtrees must still be walked.
		if cost > bestCost && cost > bestBudgetCost {
			return
		}
		if i == g {
			published := c.publishedFor(mask)
			ok, used, limited := c.feasibility(published)
			nodes += used
			if limited {
				limitHit = true
				return
			}
			if ok {
				consider(mask, cost)
			}
			return
		}

		// Keep group i published first, then hide it. Hiding only enlarges the
		// feasible region, so both branches are always worth exploring; the
		// number of groups is bounded by maxGroups.
		dfs(i+1, mask, cost)
		if limitHit {
			return
		}
		nextCost := cost + c.groupCost[i]
		dfs(i+1, mask|1<<uint(i), nextCost)
		if limitHit {
			return
		}
	}

	dfs(0, 0, 0)
	return bestMask, bestBudgetMask, nodes, limitHit
}

// maskLess compares the sorted ID sequences of two masks lexicographically.
// The absent solution sorts after every concrete mask.
func (c *compiled) maskLess(a, b uint32) bool {
	if a == noSolution {
		return false
	}
	if b == noSolution {
		return true
	}
	return slices.Compare(c.maskIDs(a), c.maskIDs(b)) < 0
}

func (c *compiled) publishedFor(mask uint32) []bool {
	published := make([]bool, len(c.constraints))
	for i, gi := range c.constraintOf {
		if gi == -1 || mask&(1<<uint(gi)) == 0 {
			published[i] = true
		}
	}
	return published
}

func (c *compiled) buildDecision(bestMask, bestBudgetMask, reportMask uint32, published []bool, full enumResult, nodes int64) *Decision {
	decision := &Decision{
		Budget:    c.req.Budget,
		EnumNodes: nodes,
		Reports:   c.buildReports(published, full),
	}

	if bestBudgetMask != noSolution {
		decision.Approved = true
		decision.HiddenGroupIDs = c.maskIDs(bestBudgetMask)
		decision.PublishedIDs = c.publishedConstraintIDs(bestBudgetMask)
		decision.TotalCost = c.maskCost(bestBudgetMask)
		return decision
	}

	decision.Approved = false
	decision.HiddenGroupIDs = c.maskIDs(reportMask)
	decision.PublishedIDs = c.publishedConstraintIDs(reportMask)
	decision.TotalCost = c.maskCost(reportMask)

	switch {
	case bestMask == noSolution:
		decision.RejectedReason = "no publication set protects every sensitive cell: even hiding every candidate summary leaves a sensitive cell with fewer than K distinct feasible integer values"
	default:
		decision.RejectedReason = fmt.Sprintf(
			"budget exhausted: the cheapest protecting publication hides groups %s at total cost %d, but the budget is %d",
			fmtIDs(c.maskIDs(bestMask)), c.maskCost(bestMask), c.req.Budget)
	}
	return decision
}

func (c *compiled) buildReports(published []bool, full enumResult) []CellReport {
	reports := make([]CellReport, len(c.sensitive))
	for i, sc := range c.sensitive {
		idx := c.sensIdx[i]
		values := sortedValues(full.cells[i].set)
		report := CellReport{
			Row:            sc.Row,
			Col:            sc.Col,
			K:              sc.K,
			FeasibleValues: values,
			Protected:      len(values) >= sc.K,
		}
		for _, value := range values {
			flat := full.cells[i].witness[value]
			report.Witnesses = append(report.Witnesses, Witness{
				Value: value,
				Cells: c.reshape(flat),
			})
		}

		excluded := 0
		for value := c.cellMin[idx]; value <= c.cellMax[idx]; value++ {
			if _, ok := full.cells[i].set[value]; ok {
				continue
			}
			if excluded >= maxExclusionsPerCell {
				report.ExclusionsLimited = true
				break
			}
			report.Exclusions = append(report.Exclusions, Exclusion{
				Value:  value,
				Reason: c.explainInfeasible(published, idx, value, full.nodes),
			})
			excluded++
		}
		reports[i] = report
	}
	return reports
}

func (c *compiled) reshape(flat []int64) [][]int64 {
	out := make([][]int64, c.rows)
	for r := 0; r < c.rows; r++ {
		out[r] = make([]int64, c.cols)
		copy(out[r], flat[r*c.cols:(r+1)*c.cols])
	}
	return out
}

func (c *compiled) maskIDs(mask uint32) []string {
	if mask == noSolution {
		return nil
	}
	ids := make([]string, 0, len(c.groups))
	for i, g := range c.groups {
		if mask&(1<<uint(i)) != 0 {
			ids = append(ids, g.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (c *compiled) maskCost(mask uint32) int64 {
	var cost int64
	for i := range c.groups {
		if mask&(1<<uint(i)) != 0 {
			cost += c.groupCost[i]
		}
	}
	return cost
}

func (c *compiled) publishedConstraintIDs(mask uint32) []string {
	ids := make([]string, 0, len(c.constraints))
	for i, con := range c.constraints {
		gi := c.constraintOf[i]
		if gi != -1 && mask&(1<<uint(gi)) != 0 {
			continue
		}
		if con.ID != "" {
			ids = append(ids, con.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (c *compiled) limitDecision(nodes int64) *Decision {
	return &Decision{
		Approved:       false,
		Budget:         c.req.Budget,
		EnumNodes:      nodes,
		EnumLimit:      true,
		RejectedReason: fmt.Sprintf("enumeration budget exhausted after %d partial assignments; the request is rejected without approval (fail closed)", nodes),
	}
}

func fmtIDs(ids []string) string {
	out := "["
	for i, id := range ids {
		if i > 0 {
			out += ", "
		}
		out += id
	}
	return out + "]"
}
