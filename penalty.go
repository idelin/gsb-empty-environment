package main

import "sort"

// adjacentSlots reports whether slots a and b are consecutive in time.
// With SlotsPerDay > 0, slots on different days are never consecutive.
func adjacentSlots(a, b, slotsPerDay int) bool {
	if a > b {
		a, b = b, a
	}
	if b != a+1 {
		return false
	}
	if slotsPerDay > 0 && a/slotsPerDay != b/slotsPerDay {
		return false
	}
	return true
}

// penaltyTable returns the graded run-penalty table: table[l] is the penalty
// for a maximal run of l consecutive exams. Default is the triangular scale
// (2 exams -> 1, 3 -> 3, 4 -> 6, ...), so long runs are punished
// disproportionately.
func penaltyTable(in *Input, maxRun int) []int {
	t := make([]int, maxRun+1)
	for l := 2; l <= maxRun; l++ {
		if l < len(in.RunPenalty) && in.RunPenalty[l] > 0 {
			t[l] = in.RunPenalty[l]
		} else {
			t[l] = l * (l - 1) / 2
		}
	}
	return t
}

// studentPenalty computes the graded penalty for one student's exam slots.
func studentPenalty(slots []int, slotsPerDay int, table []int) int {
	if len(slots) < 2 {
		return 0
	}
	sorted := append([]int(nil), slots...)
	sort.Ints(sorted)
	total, run := 0, 1
	for i := 1; i < len(sorted); i++ {
		if adjacentSlots(sorted[i-1], sorted[i], slotsPerDay) {
			run++
		} else {
			total += table[run]
			run = 1
		}
	}
	return total + table[run]
}
