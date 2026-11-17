package taskpkg

import "math/bits"

// choosePartition exhaustively checks all 2^N candidate subsets (N <= 6).
// Empty columns are invalid, so unrelated extra candidates cannot manufacture
// separators. Among valid multi-column partitions the primary objective is the
// number of distinct non-heading blocks intersected by selected lines; ties
// prefer more selected lines and then the lexicographically earliest subset.
func choosePartition(items []item, candidates []float64) ([]float64, int) {
	if len(items) == 0 || len(candidates) == 0 {
		return nil, 0
	}

	crossingMasks := make([]uint32, len(candidates))
	for candidateIndex, candidate := range candidates {
		for blockIndex, item := range items {
			if !item.heading && crossesDivider(item.box, candidate) {
				crossingMasks[candidateIndex] |= uint32(1) << uint(blockIndex)
			}
		}
	}

	bestMask := uint32(0)
	bestScore := int(^uint(0) >> 1)
	bestCount := -1
	found := false

	for mask := uint32(1); mask < uint32(1)<<uint(len(candidates)); mask++ {
		lines := maskDividers(mask, candidates)
		if !validPartition(items, lines) {
			continue
		}

		var crossed uint32
		for candidateIndex := range candidates {
			if mask&(uint32(1)<<uint(candidateIndex)) != 0 {
				crossed |= crossingMasks[candidateIndex]
			}
		}
		score := bits.OnesCount32(crossed)
		count := len(lines)

		if !found || score < bestScore || (score == bestScore && count > bestCount) ||
			(score == bestScore && count == bestCount && mask < bestMask) {
			found = true
			bestMask = mask
			bestScore = score
			bestCount = count
		}
	}

	if !found {
		return nil, 0
	}
	return maskDividers(bestMask, candidates), bestScore
}

func maskDividers(mask uint32, candidates []float64) []float64 {
	lines := make([]float64, 0, bits.OnesCount32(mask))
	for i, candidate := range candidates {
		if mask&(uint32(1)<<uint(i)) != 0 {
			lines = append(lines, candidate)
		}
	}
	return lines
}

func validPartition(items []item, dividers []float64) bool {
	if len(dividers) == 0 {
		return false
	}
	seen := make([]bool, len(dividers)+1)
	for i, item := range items {
		if item.topRoot != i {
			continue
		}
		seen[columnAt(item.box.centerX(), dividers)] = true
	}
	for _, nonEmpty := range seen {
		if !nonEmpty {
			return false
		}
	}
	return true
}
