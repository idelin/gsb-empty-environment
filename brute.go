package tdeq

// CheckExhaustive decides bounded equivalence by brute force: it
// enumerates every input string of length exactly bound over the joint
// alphabet and compares the complete concatenated outputs of both
// machines (error outputs included). It is the reference implementation
// used to cross-validate the product-graph Checker.
func CheckExhaustive(a, b *Transducer, bound int) Result {
	if bound <= 0 {
		// Only the empty input exists; both machines output nothing.
		return Result{Status: Equivalent}
	}
	alpha := unionAlphabet(a, b)
	if len(alpha) == 0 {
		// No input string of positive length exists.
		return Result{Status: Equivalent}
	}
	indices := make([]int, bound)
	buf := make([]rune, bound)
	for {
		for i, idx := range indices {
			buf[i] = alpha[idx]
		}
		witness := string(buf)
		outA, okA := a.Run(witness)
		outB, okB := b.Run(witness)
		if okA != okB || (okA && outA != outB) {
			return Result{
				Status:  Inequivalent,
				Witness: witness,
				OutA:    outA,
				OutB:    outB,
				ErrA:    !okA,
				ErrB:    !okB,
			}
		}
		// Odometer increment over the alphabet.
		pos := bound - 1
		for pos >= 0 {
			indices[pos]++
			if indices[pos] < len(alpha) {
				break
			}
			indices[pos] = 0
			pos--
		}
		if pos < 0 {
			return Result{Status: Equivalent}
		}
	}
}
