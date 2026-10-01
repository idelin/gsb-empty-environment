package main

import "math"

// enumerateOptimal brute-forces every one-to-one matching between faults and
// alerts and returns the optimal objective: maximize matched pairs first,
// then minimize total cost. Used to verify the Hungarian solver on small
// instances.
func enumerateOptimal(faults []Fault, alerts []Alert, cfg Config) (int, float64) {
	bestCount := 0
	bestCost := math.Inf(1)
	used := make([]bool, len(alerts))
	var rec func(i, count int, cost float64)
	rec = func(i, count int, cost float64) {
		if i == len(faults) {
			if count > bestCount || (count == bestCount && cost < bestCost-1e-9) {
				bestCount, bestCost = count, cost
			}
			return
		}
		rec(i+1, count, cost) // leave fault i unmatched
		for j := range alerts {
			if used[j] || !feasible(faults[i], alerts[j], cfg) {
				continue
			}
			used[j] = true
			rec(i+1, count+1, cost+pairCost(faults[i], alerts[j]))
			used[j] = false
		}
	}
	rec(0, 0, 0)
	if bestCount == 0 {
		bestCost = 0
	}
	return bestCount, bestCost
}
