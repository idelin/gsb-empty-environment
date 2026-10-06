package trajsimp

import (
	"errors"
	"math/big"
)

// BruteForceMinKeep independently recomputes the answer of MinKeepIndices
// by exhaustive subset enumeration: it tries every kept-index subset that
// contains the mandatory points (and the endpoints), in increasing subset
// size and, within one size, in lexicographic order, returning the first
// valid one. Its running time is exponential in the number of optional
// points, so it is only practical for small inputs; it exists to
// cross-check MinKeepIndices (e.g. in tests) with a completely
// independent, obviously-correct method.
func BruteForceMinKeep(pts []Point, threshold *big.Rat, mandatory []int) ([]int, error) {
	n := len(pts)
	if n == 0 {
		return nil, nil
	}
	if threshold == nil {
		return nil, errors.New("trajsimp: nil threshold")
	}
	if threshold.Sign() < 0 {
		return nil, errors.New("trajsimp: negative threshold")
	}
	must, err := normalizeMandatory(n, mandatory)
	if err != nil {
		return nil, err
	}
	isMust := make([]bool, n)
	for _, idx := range must {
		isMust[idx] = true
	}
	var optional []int
	for i := 0; i < n; i++ {
		if !isMust[i] {
			optional = append(optional, i)
		}
	}

	// Try subsets of the optional points of increasing size; combinations
	// are generated in lexicographic order, and since the mandatory part
	// is fixed, the first valid full sequence found is the answer.
	for size := 0; size <= len(optional); size++ {
		chosen := make([]int, size)
		var walk func(start, depth int) []int
		walk = func(start, depth int) []int {
			if depth == size {
				seq := mergeSorted(must, chosen)
				if SequenceValid(pts, threshold, seq) {
					return seq
				}
				return nil
			}
			for i := start; i <= len(optional)-(size-depth); i++ {
				chosen[depth] = optional[i]
				if got := walk(i+1, depth+1); got != nil {
					return got
				}
			}
			return nil
		}
		if got := walk(0, 0); got != nil {
			return got, nil
		}
	}
	return nil, errors.New("trajsimp: no valid subset (unreachable)")
}

// mergeSorted merges the mandatory indices with the chosen optional
// indices into one sorted sequence.
func mergeSorted(must, chosen []int) []int {
	seq := make([]int, 0, len(must)+len(chosen))
	i, j := 0, 0
	for i < len(must) || j < len(chosen) {
		switch {
		case j >= len(chosen) || (i < len(must) && must[i] < chosen[j]):
			seq = append(seq, must[i])
			i++
		default:
			seq = append(seq, chosen[j])
			j++
		}
	}
	return seq
}
