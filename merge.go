package freqsketch

// MergeSequential folds summaries left to right: ((s0+s1)+s2)+...
// The input slice is not modified; the input summaries are not modified.
func MergeSequential(summaries []*Summary) *Summary {
	if len(summaries) == 0 {
		panic("freqsketch: nothing to merge")
	}
	acc := summaries[0]
	for _, s := range summaries[1:] {
		acc = acc.Merge(s)
	}
	return acc
}

// MergeBalanced merges summaries as a balanced binary tree, pairing
// neighbours level by level. The inputs are not modified.
func MergeBalanced(summaries []*Summary) *Summary {
	if len(summaries) == 0 {
		panic("freqsketch: nothing to merge")
	}
	level := make([]*Summary, len(summaries))
	copy(level, summaries)
	for len(level) > 1 {
		next := make([]*Summary, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 < len(level) {
				next = append(next, level[i].Merge(level[i+1]))
			} else {
				next = append(next, level[i])
			}
		}
		level = next
	}
	return level[0]
}

// Permuted returns a copy of summaries reordered by perm (a permutation of
// 0..len-1), so callers can study arbitrary merge orders.
func Permuted(summaries []*Summary, perm []int) []*Summary {
	out := make([]*Summary, len(summaries))
	for i, p := range perm {
		out[i] = summaries[p]
	}
	return out
}
