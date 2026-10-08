// Package facilitycover solves tree-constrained facility coverage problems.
//
// Facilities form a forest: opening a facility requires all of its ancestors
// to be open as well (the open set is an ancestor-closed "closure").
// Each demand point yields its revenue at most once when covered by any open
// facility; overlapping coverage is never double counted.
package facilitycover

import (
	"errors"
	"fmt"
	"sort"
)

// Facility is a single candidate site in the tree.
type Facility struct {
	ID     int   // unique identifier, also used as index into Coverage rows
	Parent int   // ID of the parent facility, or -1 for a root
	Cost   int64 // integer opening cost
}

// Instance is the deterministic (single-scenario) coverage problem.
type Instance struct {
	Facilities    []Facility
	Budget        int64   // integer budget on total opening cost
	DemandRevenue []int64 // revenue per demand point, indexed by demand ID
	// Coverage[f] lists the demand points covered by facility f.
	Coverage map[int][]int
}

// Scenario describes one possible coverage realisation for the robust problem.
type Scenario struct {
	Group       int     // observation group: scenarios in a group share the emergency action
	Probability float64 // used for the expected-cost tie-break
	// Coverage[f] lists the demand points covered by facility f in this scenario.
	Coverage map[int][]int
}

// RobustInstance is the two-stage problem: first-stage openings are fixed, then a
// limited number of emergency facilities may be enabled per observation group.
type RobustInstance struct {
	Instance
	EmergencyLimit int // max number of emergency facilities per scenario
	Scenarios      []Scenario
}

// MaxDeterministicFacilities bounds the deterministic solver/verifier.
const MaxDeterministicFacilities = 20

// MaxRobustFacilities and MaxRobustScenarios bound the robust solver.
const (
	MaxRobustFacilities = 12
	MaxRobustScenarios  = 5
)

// bitset is a small fixed-capacity bit set over demand point IDs.
type bitset []uint64

func newBitset(n int) bitset { return make(bitset, (n+63)/64) }

func (b bitset) set(i int) { b[i/64] |= 1 << (uint(i) % 64) }

func (b bitset) has(i int) bool { return b[i/64]&(1<<(uint(i)%64)) != 0 }

func (b bitset) orInto(o bitset) {
	for i := range b {
		b[i] |= o[i]
	}
}

func (b bitset) clone() bitset {
	c := make(bitset, len(b))
	copy(c, b)
	return c
}

// revenueOf sums the revenues of all demands present in b (each counted once).
func revenueOf(b bitset, weights []int64) int64 {
	var total int64
	for i, w := range weights {
		if b.has(i) {
			total += w
		}
	}
	return total
}

// prepared holds derived data shared by the solvers.
type prepared struct {
	inst      *Instance
	n         int
	order     []int   // facility indices, parents before children
	idxOf     []int   // facility ID -> index in inst.Facilities
	parentIdx []int   // facility index -> parent facility index, -1 if root
	costs     []int64 // facility index -> cost
	cov       []bitset
}

func prepare(inst *Instance, coverage map[int][]int) (*prepared, error) {
	n := len(inst.Facilities)
	if n == 0 {
		return nil, errors.New("facilitycover: no facilities")
	}
	if inst.Budget < 0 {
		return nil, errors.New("facilitycover: negative budget")
	}
	idxOf := make([]int, n)
	seen := make(map[int]bool, n)
	maxID := -1
	for i, f := range inst.Facilities {
		if seen[f.ID] {
			return nil, fmt.Errorf("facilitycover: duplicate facility ID %d", f.ID)
		}
		seen[f.ID] = true
		if f.ID > maxID {
			maxID = f.ID
		}
		if f.Cost < 0 {
			return nil, fmt.Errorf("facilitycover: facility %d has negative cost", f.ID)
		}
		idxOf[i] = f.ID
	}
	idToIdx := make(map[int]int, n)
	for i, f := range inst.Facilities {
		idToIdx[f.ID] = i
	}
	parentIdx := make([]int, n)
	depth := make([]int, n)
	for i, f := range inst.Facilities {
		if f.Parent == -1 {
			parentIdx[i] = -1
			continue
		}
		p, ok := idToIdx[f.Parent]
		if !ok {
			return nil, fmt.Errorf("facilitycover: facility %d has unknown parent %d", f.ID, f.Parent)
		}
		parentIdx[i] = p
	}
	// Cycle check via depth computation with memoisation.
	for i := range inst.Facilities {
		d := 0
		for j := i; parentIdx[j] != -1; j = parentIdx[j] {
			d++
			if d > n {
				return nil, fmt.Errorf("facilitycover: cycle detected at facility %d", inst.Facilities[i].ID)
			}
		}
		depth[i] = d
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return depth[order[a]] < depth[order[b]] })

	costs := make([]int64, n)
	for i, f := range inst.Facilities {
		costs[i] = f.Cost
	}
	d := len(inst.DemandRevenue)
	cov := make([]bitset, n)
	for i, f := range inst.Facilities {
		cov[i] = newBitset(d)
		for _, dem := range coverage[f.ID] {
			if dem < 0 || dem >= d {
				return nil, fmt.Errorf("facilitycover: facility %d covers unknown demand %d", f.ID, dem)
			}
			cov[i].set(dem)
		}
	}
	return &prepared{
		inst: inst, n: n, order: order, idxOf: idxOf,
		parentIdx: parentIdx, costs: costs, cov: cov,
	}, nil
}

// isClosure reports whether the open set (by facility index) is ancestor-closed.
func (p *prepared) isClosure(open []bool) bool {
	for i := 0; i < p.n; i++ {
		if open[i] && p.parentIdx[i] != -1 && !open[p.parentIdx[i]] {
			return false
		}
	}
	return true
}

// unionCoverage returns the coverage bitset of the given open facilities.
func (p *prepared) unionCoverage(cov []bitset, open []bool) bitset {
	u := newBitset(len(p.inst.DemandRevenue))
	for i := 0; i < p.n; i++ {
		if open[i] {
			u.orInto(cov[i])
		}
	}
	return u
}

func (p *prepared) costOf(open []bool) int64 {
	var c int64
	for i := 0; i < p.n; i++ {
		if open[i] {
			c += p.costs[i]
		}
	}
	return c
}

// openIDs converts an open-by-index slice to sorted facility IDs.
func (p *prepared) openIDs(open []bool) []int {
	ids := make([]int, 0, p.n)
	for i := 0; i < p.n; i++ {
		if open[i] {
			ids = append(ids, p.inst.Facilities[i].ID)
		}
	}
	sort.Ints(ids)
	return ids
}
