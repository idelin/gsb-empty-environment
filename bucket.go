package netsec

import (
	"errors"
	"fmt"
	"math/bits"
)

// MaxEvents is the largest number of events a Problem may contain.
const MaxEvents = 18

// Problem describes a privacy-preserving event publication task.
//
// Times holds the integer occurrence time of every event. MaxDelay[i] is the
// largest delay that event i may incur, so its published time must stay in
// [Times[i], Times[i]+MaxDelay[i]]. Every directed edge [u, v] in Edges is a
// causal dependency that requires the published time of u not to exceed the
// published time of v.
type Problem struct {
	Times    []int
	MaxDelay []int
	Edges    [][2]int
}

// Solution is a legal time-bucketing of a Problem.
//
// Buckets[i] is the published time assigned to event i. Groups lists the
// events sharing each bucket, ordered by increasing publication time.
// DistinctBuckets is the number of distinct published times and TotalDelay is
// the sum of Buckets[i]-Times[i]. Among all legal assignments the solution
// minimizes DistinctBuckets first and TotalDelay second.
type Solution struct {
	Buckets         []int
	Groups          [][]int
	DistinctBuckets int
	TotalDelay      int
}

// InfeasibleError reports that no legal assignment exists.
//
// Event cannot be published earlier than LowerBound because causal
// dependencies propagate an earlier occurrence time forward, while its
// latest legal publication time is Limit. Witness lists the event indices on
// the propagation path, ordered from the originating event to Event.
type InfeasibleError struct {
	Event      int
	LowerBound int
	Limit      int
	Witness    []int
}

func (e *InfeasibleError) Error() string {
	return fmt.Sprintf("netsec: event %d cannot be published before %d but its deadline is %d (witness path %v)",
		e.Event, e.LowerBound, e.Limit, e.Witness)
}

// Solve runs the bucketing solver on a standalone set of arguments.
func Solve(times, maxDelay []int, edges [][2]int) (*Solution, error) {
	return (&Problem{Times: times, MaxDelay: maxDelay, Edges: edges}).Solve()
}

// Solve finds an optimal legal time-bucketing, or returns an *InfeasibleError
// carrying a witness path when the constraints cannot be satisfied.
func (p *Problem) Solve() (*Solution, error) {
	n := len(p.Times)
	if n == 0 {
		return nil, errors.New("netsec: at least one event is required")
	}
	if n > MaxEvents {
		return nil, fmt.Errorf("netsec: too many events: got %d, maximum is %d", n, MaxEvents)
	}
	if len(p.MaxDelay) != n {
		return nil, errors.New("netsec: Times and MaxDelay must have the same length")
	}
	for i, delay := range p.MaxDelay {
		if delay < 0 {
			return nil, fmt.Errorf("netsec: MaxDelay[%d] must not be negative", i)
		}
	}
	for edgeIndex, edge := range p.Edges {
		for _, event := range edge {
			if event < 0 || event >= n {
				return nil, fmt.Errorf("netsec: edge %d references event %d outside range [0,%d)",
					edgeIndex, event, n)
			}
		}
	}

	pred := p.predecessorSets()
	lowerBound, parent := p.longestPathLowerBounds()
	for i := 0; i < n; i++ {
		limit := p.Times[i] + p.MaxDelay[i]
		if lowerBound[i] > limit {
			return nil, &InfeasibleError{
				Event:      i,
				LowerBound: lowerBound[i],
				Limit:      limit,
				Witness:    witnessPath(parent, i),
			}
		}
	}

	return p.solveDP(n, pred)
}

// predecessorSets returns, for every event, the bitmask of itself together
// with all events that can reach it through one or more dependency edges.
func (p *Problem) predecessorSets() []uint32 {
	n := len(p.Times)
	pred := make([]uint32, n)
	for i := range pred {
		pred[i] = uint32(1) << uint(i)
	}
	for _, edge := range p.Edges {
		pred[edge[1]] |= uint32(1) << uint(edge[0])
	}
	for k := 0; k < n; k++ {
		bitK := uint32(1) << uint(k)
		for i := 0; i < n; i++ {
			if pred[i]&bitK != 0 {
				pred[i] |= pred[k]
			}
		}
	}
	return pred
}

// longestPathLowerBounds computes the earliest publishable time of every
// event as the maximum occurrence time reachable through dependency edges.
// The returned parent slice records the edge that last raised each bound and
// supports witness-path reconstruction. Edge weights are zero, so the
// relaxation converges within n passes even in the presence of cycles.
func (p *Problem) longestPathLowerBounds() (lowerBound, parent []int) {
	n := len(p.Times)
	lowerBound = append([]int(nil), p.Times...)
	parent = make([]int, n)
	for i := range parent {
		parent[i] = -1
	}
	for iteration := 0; iteration < n; iteration++ {
		changed := false
		for _, edge := range p.Edges {
			from, to := edge[0], edge[1]
			if lowerBound[from] > lowerBound[to] {
				lowerBound[to] = lowerBound[from]
				parent[to] = from
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return lowerBound, parent
}

// witnessPath walks parent pointers from the violating event back to the
// originating event and returns the path in origin-to-violation order.
func witnessPath(parent []int, start int) []int {
	visited := map[int]bool{start: true}
	path := []int{start}
	current := start
	for parent[current] != -1 {
		current = parent[current]
		if visited[current] {
			break
		}
		visited[current] = true
		path = append(path, current)
	}
	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}
	return path
}

// solveDP performs subset dynamic programming over event masks.
//
// A bucket group is a non-empty subset of events published at one time. The
// cheapest legal publication time for group g is lo[g] = max Times[i] over
// i in g: it lies inside every member's window exactly when lo[g] <= hi[g]
// where hi[g] = min (Times[i]+MaxDelay[i]). Causality is respected when no
// group contains an event whose ancestor lies outside the subproblem mask.
//
// In every partition the event with the largest occurrence time (ties broken
// by index) must belong to the last group, since bucket times strictly
// increase, so dp[mask] only considers last groups containing that anchor.
// The objective is lexicographic: fewest groups, then smallest total delay.
func (p *Problem) solveDP(n int, pred []uint32) (*Solution, error) {
	size := 1 << uint(n)
	lo := make([]int, size)
	hi := make([]int, size)
	sumTime := make([]int, size)
	predUnion := make([]uint32, size)
	anchorBit := make([]uint32, size)

	for mask := 1; mask < size; mask++ {
		bit := uint32(mask & -mask)
		i := bits.TrailingZeros32(bit)
		previous := mask ^ int(bit)
		sumTime[mask] = sumTime[previous] + p.Times[i]
		predUnion[mask] = predUnion[previous] | pred[i]
		windowEnd := p.Times[i] + p.MaxDelay[i]
		if previous == 0 {
			lo[mask] = p.Times[i]
			hi[mask] = windowEnd
			anchorBit[mask] = bit
			continue
		}
		if p.Times[i] >= lo[previous] {
			lo[mask] = p.Times[i]
			anchorBit[mask] = bit
		} else {
			lo[mask] = lo[previous]
			anchorBit[mask] = anchorBit[previous]
		}
		if windowEnd < hi[previous] {
			hi[mask] = windowEnd
		} else {
			hi[mask] = hi[previous]
		}
	}

	groupCost := make([]int, size)
	for mask := 1; mask < size; mask++ {
		groupCost[mask] = bits.OnesCount32(uint32(mask))*lo[mask] - sumTime[mask]
	}

	const infeasible = 1 << 30
	bestGroups := make([]int, size)
	bestDelay := make([]int, size)
	lastGroup := make([]uint32, size)
	for mask := 1; mask < size; mask++ {
		bestGroups[mask] = infeasible
	}

	for mask := 1; mask < size; mask++ {
		anchor := anchorBit[mask]
		free := uint32(mask) ^ anchor
		sub := free
		for {
			group := sub | anchor
			groupMask := int(group)
			if lo[groupMask] <= hi[groupMask] && predUnion[group]&^uint32(mask) == 0 {
				rest := uint32(mask) ^ group
				if rest == 0 {
					consider(mask, 1, groupCost[groupMask], group, bestGroups, bestDelay, lastGroup)
				} else if bestGroups[rest] != infeasible && lo[rest] < lo[groupMask] {
					groupCount := bestGroups[rest] + 1
					totalDelay := bestDelay[rest] + groupCost[groupMask]
					consider(mask, groupCount, totalDelay, group, bestGroups, bestDelay, lastGroup)
				}
			}
			if sub == 0 {
				break
			}
			sub = (sub - 1) & free
		}
	}

	fullMask := uint32(size - 1)
	if bestGroups[fullMask] == infeasible {
		return nil, errors.New("netsec: internal error: feasibility check accepted an unsolvable problem")
	}
	return p.reconstruct(n, fullMask, lo, bestGroups, bestDelay, lastGroup), nil
}

// consider records a DP candidate when it improves the lexicographic objective.
func consider(mask, groupCount, totalDelay int, group uint32,
	bestGroups, bestDelay []int, lastGroup []uint32) {
	if groupCount < bestGroups[mask] ||
		(groupCount == bestGroups[mask] && totalDelay < bestDelay[mask]) {
		bestGroups[mask] = groupCount
		bestDelay[mask] = totalDelay
		lastGroup[mask] = group
	}
}

// reconstruct walks the stored DP choices and builds the public solution.
func (p *Problem) reconstruct(n int, fullMask uint32, lo, bestGroups, bestDelay []int,
	lastGroup []uint32) *Solution {
	buckets := make([]int, n)
	var groups [][]int
	for current := fullMask; current != 0; current ^= lastGroup[current] {
		group := lastGroup[current]
		bucketTime := lo[group]
		members := make([]int, 0, bits.OnesCount32(group))
		for remaining := group; remaining != 0; remaining &= remaining - 1 {
			bit := remaining & -remaining
			event := bits.TrailingZeros32(bit)
			buckets[event] = bucketTime
			members = append(members, event)
		}
		groups = append(groups, members)
	}
	for left, right := 0, len(groups)-1; left < right; left, right = left+1, right-1 {
		groups[left], groups[right] = groups[right], groups[left]
	}
	return &Solution{
		Buckets:         buckets,
		Groups:          groups,
		DistinctBuckets: bestGroups[fullMask],
		TotalDelay:      bestDelay[fullMask],
	}
}
