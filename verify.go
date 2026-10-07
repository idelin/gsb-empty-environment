package dfeq

// Enumerate yields all words over the alphabet up to maxLen in
// shortlex order (shorter first, then lexicographic), starting with ε.
func Enumerate(alphabet []string, maxLen int) [][]string {
	words := [][]string{{}}
	cur := [][]string{{}}
	for depth := 0; depth < maxLen; depth++ {
		var next [][]string
		for _, w := range cur {
			for _, sym := range alphabet {
				nw := append(append([]string(nil), w...), sym)
				words = append(words, nw)
				next = append(next, nw)
			}
		}
		cur = next
	}
	return words
}

// CrossCheck enumerates all words up to maxLen over the union alphabet
// and returns the first word accepted by exactly one of the automata,
// or nil if none exists. It is an independent, brute-force核对 of Compare.
func CrossCheck(a, b *DFA, maxLen int) []string {
	for _, w := range Enumerate(unionAlphabet(a, b), maxLen) {
		if a.Simulate(w) != b.Simulate(w) {
			return w
		}
	}
	return nil
}

// VerifyCounterexample checks that word is a valid and minimal
// distinguishing word between a and b:
//  1. the product-state path of word is consistent with both automata;
//  2. word is accepted by exactly one side;
//  3. no shorter or lexicographically smaller word distinguishes them
//     (checked against a full product-graph traversal).
func VerifyCounterexample(a, b *DFA, word []string) (bool, string) {
	// 1+2: simulate both sides along the product path.
	if a.Simulate(word) == b.Simulate(word) {
		return false, "word is not accepted by exactly one automaton"
	}
	// 3: minimality via a fresh full comparison.
	res := Compare(a, b)
	if res.Kind != Counterexample {
		return false, "full product graph found no counterexample (kind=" + res.Kind.String() + ")"
	}
	if !equalWord(res.Word, word) {
		return false, "word is not the shortest lexicographically smallest counterexample; want " + res.WordString()
	}
	return true, ""
}

// ValidateResult re-runs a full product-graph traversal on the current
// automata and checks that a (possibly cached) result still agrees:
// same kind, same counterexample word (hence minimality), same
// blocking set. Equivalence is only confirmed by the fresh traversal.
func ValidateResult(a, b *DFA, cached Result) bool {
	fresh := Compare(a, b)
	if fresh.Kind != cached.Kind {
		return false
	}
	if fresh.Kind == Counterexample && !equalWord(fresh.Word, cached.Word) {
		return false
	}
	if fresh.Kind == Unknown && !equalSet(fresh.Blocking, cached.Blocking) {
		return false
	}
	return true
}

func equalWord(x, y []string) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func equalSet(x, y []string) bool {
	if len(x) != len(y) {
		return false
	}
	m := map[string]bool{}
	for _, s := range x {
		m[s] = true
	}
	for _, s := range y {
		if !m[s] {
			return false
		}
	}
	return true
}
