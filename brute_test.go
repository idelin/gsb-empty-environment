package main

import (
	"math/rand"
	"testing"
)

// bruteForce enumerates every subset that includes all confirmed orders,
// checks feasibility by plain backtracking placement, and returns the
// maximum profit. Independent of the branch-and-bound solver.
func bruteForce(orders []order, nSpots int) (int64, bool) {
	var best int64
	feasible := false
	var occupy []int64
	var sorted []order
	var place func(idx int) bool
	place = func(idx int) bool {
		if idx == len(sorted) {
			return true
		}
		o := sorted[idx]
		for _, sp := range o.compat {
			if occupy[sp] <= o.start {
				old := occupy[sp]
				occupy[sp] = o.end
				if place(idx + 1) {
					return true
				}
				occupy[sp] = old
			}
		}
		return false
	}
	var enum func(i int, chosen []order, profit int64)
	enum = func(i int, chosen []order, profit int64) {
		if i == len(orders) {
			occupy = make([]int64, nSpots)
			for k := range occupy {
				occupy[k] = -1 << 62
			}
			sorted = append([]order(nil), chosen...)
			// sort by start for the placement check
			for a := 0; a < len(sorted); a++ {
				for b := a + 1; b < len(sorted); b++ {
					if sorted[b].start < sorted[a].start {
						sorted[a], sorted[b] = sorted[b], sorted[a]
					}
				}
			}
			if place(0) {
				feasible = true
				if profit > best {
					best = profit
				}
			}
			return
		}
		o := orders[i]
		if !o.confirmed {
			enum(i+1, chosen, profit)
		}
		enum(i+1, append(chosen, o), profit+o.profit)
	}
	enum(0, nil, 0)
	return best, feasible
}

func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		nSpots := 1 + rng.Intn(3)
		types := []string{"sedan", "truck"}
		in := &Input{
			Defaults: Defaults{EntryBuffer: int64(rng.Intn(3)), ExitBuffer: int64(rng.Intn(3))},
		}
		for s := 0; s < nSpots; s++ {
			sp := Spot{ID: string(rune('A' + s))}
			for _, vt := range types {
				if rng.Intn(2) == 0 {
					sp.VehicleTypes = append(sp.VehicleTypes, vt)
				}
			}
			if len(sp.VehicleTypes) == 0 {
				sp.VehicleTypes = append(sp.VehicleTypes, types[rng.Intn(2)])
			}
			in.Spots = append(in.Spots, sp)
		}
		n := 1 + rng.Intn(9)
		for i := 0; i < n; i++ {
			start := int64(rng.Intn(40))
			in.Reservations = append(in.Reservations, Reservation{
				ID:          string(rune('a'+i)) + string(rune('0'+i%10)),
				VehicleType: types[rng.Intn(2)],
				Start:       start,
				End:         start + 1 + int64(rng.Intn(15)),
				Profit:      int64(1 + rng.Intn(50)),
				Confirmed:   rng.Intn(4) == 0,
			})
		}
		orders, _, err := buildOrders(in)
		if err != nil {
			t.Fatalf("buildOrders: %v", err)
		}
		wantProfit, wantFeasible := bruteForce(orders, nSpots)
		res, err := Solve(in)
		if err != nil {
			t.Fatalf("Solve: %v", err)
		}
		if !wantFeasible {
			if res.Status != "infeasible" {
				t.Fatalf("trial %d: expected infeasible, got %+v", trial, res)
			}
			continue
		}
		if res.Status != "optimal" || res.TotalProfit != wantProfit {
			t.Fatalf("trial %d: got status=%s profit=%d, want %d\ninput=%+v",
				trial, res.Status, res.TotalProfit, wantProfit, in)
		}
		verifyAssignment(t, trial, orders, res)
		// every confirmed order must be accepted
		acc := make(map[string]bool)
		for _, a := range res.Accepted {
			acc[a.ID] = true
		}
		for _, o := range orders {
			if o.confirmed && !acc[o.id] {
				t.Fatalf("trial %d: confirmed %s not accepted", trial, o.id)
			}
		}
	}
}

// verifyAssignment independently checks the produced spot plan: every
// accepted order's occupancy window matches its buffers and no two
// accepted orders on the same spot overlap (half-open rule).
func verifyAssignment(t *testing.T, trial int, orders []order, res *Result) {
	t.Helper()
	byID := make(map[string]order)
	for _, o := range orders {
		byID[o.id] = o
	}
	bySpot := make(map[string][]Assignment)
	for _, a := range res.Accepted {
		bySpot[a.Spot] = append(bySpot[a.Spot], a)
		o := byID[a.ID]
		if a.OccupyFrom != o.start || a.OccupyTo != o.end {
			t.Fatalf("trial %d: %s occupancy mismatch", trial, a.ID)
		}
	}
	for spot, list := range bySpot {
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				a, b := list[i], list[j]
				if overlap(a.OccupyFrom, a.OccupyTo, b.OccupyFrom, b.OccupyTo) {
					t.Fatalf("trial %d: spot %s double-booked: %s [%d,%d) vs %s [%d,%d)",
						trial, spot, a.ID, a.OccupyFrom, a.OccupyTo, b.ID, b.OccupyFrom, b.OccupyTo)
				}
			}
		}
	}
}
