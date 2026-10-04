package main

import "sort"

// assignRooms matches the exams of one slot to distinct rooms with sufficient
// capacity. It returns room indices aligned with the input exam slice, or nil
// if no feasible assignment exists.
func assignRooms(examSizes []int, rooms []Room) []int {
	order := make([]int, len(examSizes))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return examSizes[order[a]] > examSizes[order[b]] })

	result := make([]int, len(examSizes))
	used := make([]bool, len(rooms))
	var place func(k int) bool
	place = func(k int) bool {
		if k == len(order) {
			return true
		}
		ei := order[k]
		for r, room := range rooms {
			if used[r] || room.Capacity < examSizes[ei] {
				continue
			}
			used[r] = true
			result[ei] = r
			if place(k + 1) {
				return true
			}
			used[r] = false
		}
		return false
	}
	if !place(0) {
		return nil
	}
	return result
}
