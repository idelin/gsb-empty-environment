package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// ---------- output schema ----------

type Assignment struct {
	ID         string `json:"id"`
	Spot       string `json:"spot"`
	OccupyFrom int64  `json:"occupy_from"`
	OccupyTo   int64  `json:"occupy_to"`
	Profit     int64  `json:"profit"`
	Confirmed  bool   `json:"confirmed"`
}

type Conflict struct {
	With        string `json:"with"`
	Spot        string `json:"spot"`
	OverlapFrom int64  `json:"overlap_from"`
	OverlapTo   int64  `json:"overlap_to"`
}

type Rejection struct {
	ID        string     `json:"id"`
	Reason    string     `json:"reason"`
	Conflicts []Conflict `json:"conflicts,omitempty"`
}

type Result struct {
	Status      string       `json:"status"` // "optimal" | "infeasible"
	TotalProfit int64        `json:"total_profit"`
	Accepted    []Assignment `json:"accepted"`
	Rejected    []Rejection  `json:"rejected"`
	Note        string       `json:"note,omitempty"`
}

// ---------- solver ----------

type solver struct {
	orders   []order // sorted by occupancy start
	spotIDs  []string
	spotSig  []string // per-spot accepted-type signature (for symmetry)
	occupied []int64  // per-spot: occupied until (half-open, <= next start is free)
	suffix   []int64  // suffix sums of profits (upper bound)

	bestProfit int64
	bestCount  int
	bestSpot   []int // bestSpot[i] = spot index for order i, -1 = rejected
	curSpot    []int
	found      bool
}

// Solve computes a maximum-profit feasible subset. Confirmed orders are
// never rejected; if they cannot all be placed the result is infeasible.
func Solve(in *Input) (*Result, error) {
	orders, spotIDs, err := buildOrders(in)
	if err != nil {
		return nil, err
	}

	// Sort by occupancy start, then end; confirmed first on ties so
	// mandatory work is placed as early as possible.
	sort.Slice(orders, func(i, j int) bool {
		a, b := orders[i], orders[j]
		if a.start != b.start {
			return a.start < b.start
		}
		if a.end != b.end {
			return a.end < b.end
		}
		if a.confirmed != b.confirmed {
			return a.confirmed
		}
		return a.id < b.id
	})

	sigs := make([]string, len(in.Spots))
	for i, sp := range in.Spots {
		ts := append([]string(nil), sp.VehicleTypes...)
		sort.Strings(ts)
		sigs[i] = strings.Join(ts, ",")
	}
	s := &solver{
		orders:   orders,
		spotIDs:  spotIDs,
		spotSig:  sigs,
		occupied: make([]int64, len(spotIDs)),
		suffix:   make([]int64, len(orders)+1),
		curSpot:  make([]int, len(orders)),
		bestSpot: make([]int, len(orders)),
	}
	for i := range s.occupied {
		s.occupied[i] = math.MinInt64
	}
	for i := range s.curSpot {
		s.curSpot[i] = -1
		s.bestSpot[i] = -1
	}
	for i := len(orders) - 1; i >= 0; i-- {
		p := orders[i].profit
		if p > 0 {
			s.suffix[i] = s.suffix[i+1] + p
		} else {
			s.suffix[i] = s.suffix[i+1]
		}
	}

	s.search(0, 0, 0)

	if !s.found {
		return infeasibleResult(orders, spotIDs), nil
	}
	return s.buildResult(), nil
}

func (s *solver) search(idx int, profit int64, count int) {
	n := len(s.orders)
	if idx == n {
		if !s.found || profit > s.bestProfit ||
			(profit == s.bestProfit && count > s.bestCount) {
			s.found = true
			s.bestProfit = profit
			s.bestCount = count
			copy(s.bestSpot, s.curSpot)
		}
		return
	}
	// Upper bound: even taking every remaining order cannot beat the best.
	if profit+s.suffix[idx] < s.bestProfit {
		return
	}
	if profit+s.suffix[idx] == s.bestProfit && s.found &&
		count+(n-idx) <= s.bestCount {
		return
	}

	o := s.orders[idx]

	// Branch 1: place on each compatible free spot. Spots with identical
	// occupied-until times AND identical accepted-type sets are symmetric
	// here; try each distinct combination once.
	tried := make(map[string]bool)
	for _, sp := range o.compat {
		until := s.occupied[sp]
		if until > o.start { // half-open: until == o.start is free
			continue
		}
		key := fmt.Sprintf("%d|%s", until, s.spotSig[sp])
		if tried[key] {
			continue
		}
		tried[key] = true
		s.occupied[sp] = o.end
		s.curSpot[idx] = sp
		s.search(idx+1, profit+o.profit, count+1)
		s.curSpot[idx] = -1
		s.occupied[sp] = until
	}

	// Branch 2: reject (never for confirmed reservations).
	if !o.confirmed {
		s.search(idx+1, profit, count)
	}
}

func (s *solver) buildResult() *Result {
	res := &Result{Status: "optimal", TotalProfit: s.bestProfit}
	for i, o := range s.orders {
		sp := s.bestSpot[i]
		if sp >= 0 {
			res.Accepted = append(res.Accepted, Assignment{
				ID:         o.id,
				Spot:       s.spotIDs[sp],
				OccupyFrom: o.start,
				OccupyTo:   o.end,
				Profit:     o.profit,
				Confirmed:  o.confirmed,
			})
		}
	}
	// Conflict evidence for rejected orders: accepted orders whose
	// occupancy overlaps and whose assigned spot accepts this vehicle type.
	for i, o := range s.orders {
		if s.bestSpot[i] >= 0 {
			continue
		}
		rej := Rejection{ID: o.id}
		if len(o.compat) == 0 {
			rej.Reason = fmt.Sprintf("no spot accepts vehicle type %q", o.vtype)
		} else {
			rej.Reason = "all compatible spots occupied by higher-value schedule"
		}
		compat := make(map[int]bool, len(o.compat))
		for _, sp := range o.compat {
			compat[sp] = true
		}
		for j, a := range s.orders {
			sp := s.bestSpot[j]
			if sp < 0 || !compat[sp] {
				continue
			}
			if overlap(o.start, o.end, a.start, a.end) {
				lo := o.start
				if a.start > lo {
					lo = a.start
				}
				hi := o.end
				if a.end < hi {
					hi = a.end
				}
				rej.Conflicts = append(rej.Conflicts, Conflict{
					With:        a.id,
					Spot:        s.spotIDs[sp],
					OverlapFrom: lo,
					OverlapTo:   hi,
				})
			}
		}
		res.Rejected = append(res.Rejected, rej)
	}
	sort.Slice(res.Accepted, func(i, j int) bool { return res.Accepted[i].ID < res.Accepted[j].ID })
	sort.Slice(res.Rejected, func(i, j int) bool { return res.Rejected[i].ID < res.Rejected[j].ID })
	return res
}

// infeasibleResult is produced when confirmed reservations cannot all be
// placed. It places confirmed orders greedily (start order) and reports
// the first one that fails together with the blocking confirmed orders.
func infeasibleResult(orders []order, spotIDs []string) *Result {
	res := &Result{Status: "infeasible"}
	occupied := make([]int64, len(spotIDs))
	for i := range occupied {
		occupied[i] = math.MinInt64
	}
	placedSpot := make(map[string]int)
	var placed []order
	for _, o := range orders {
		if !o.confirmed {
			continue
		}
		if len(o.compat) == 0 {
			res.Note = fmt.Sprintf("confirmed reservation %q has no compatible spot for vehicle type %q", o.id, o.vtype)
			res.Rejected = append(res.Rejected, Rejection{
				ID:     o.id,
				Reason: fmt.Sprintf("no spot accepts vehicle type %q", o.vtype),
			})
			return res
		}
		spot := -1
		for _, sp := range o.compat {
			if occupied[sp] <= o.start {
				spot = sp
				break
			}
		}
		if spot < 0 {
			res.Note = fmt.Sprintf("confirmed reservation %q cannot be placed: every compatible spot is occupied by another confirmed reservation", o.id)
			rej := Rejection{ID: o.id, Reason: "confirmed reservations mutually exceed spot capacity"}
			for _, a := range placed {
				sp := placedSpot[a.id]
				mine := false
				for _, cp := range o.compat {
					if cp == sp {
						mine = true
						break
					}
				}
				if !mine || !overlap(o.start, o.end, a.start, a.end) {
					continue
				}
				lo := o.start
				if a.start > lo {
					lo = a.start
				}
				hi := o.end
				if a.end < hi {
					hi = a.end
				}
				rej.Conflicts = append(rej.Conflicts, Conflict{
					With:        a.id,
					Spot:        spotIDs[sp],
					OverlapFrom: lo,
					OverlapTo:   hi,
				})
			}
			res.Rejected = append(res.Rejected, rej)
			return res
		}
		occupied[spot] = o.end
		placedSpot[o.id] = spot
		placed = append(placed, o)
	}
	res.Note = "confirmed reservations are mutually infeasible"
	return res
}
