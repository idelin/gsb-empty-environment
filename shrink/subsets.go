package shrink

// presenceSubsets groups every n-bit mask by its popcount (number of present
// optional fields). Index k holds all masks with exactly k bits set, in
// ascending numeric order so enumeration within a tier is deterministic.
func presenceSubsets(n int) [][]int {
	tiers := make([][]int, n+1)
	for mask := 0; mask < (1 << n); mask++ {
		k := popcount(mask)
		tiers[k] = append(tiers[k], mask)
	}
	return tiers
}

func popcount(mask int) int {
	count := 0
	for mask != 0 {
		mask &= mask - 1
		count++
	}
	return count
}
