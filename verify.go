package dfacmp

import "fmt"

// VerifyCounterexample checks that r is a valid counterexample for a and b:
// the word is accepted by exactly one side and the recorded product-state
// path is consistent with both automata.
func VerifyCounterexample(a, b *Automaton, r Result) error {
	if r.Kind != Counterexample {
		return fmt.Errorf("dfacmp: result is %v, not a counterexample", r.Kind)
	}
	accA, okA := a.Run(r.Word)
	accB, okB := b.Run(r.Word)
	if !okA || !okB {
		return fmt.Errorf("dfacmp: word %s traverses unexpanded fragments", quoteWord(r.Word))
	}
	if accA == accB {
		return fmt.Errorf("dfacmp: word %s is not distinguishing (accepted by both: %v)", quoteWord(r.Word), accA)
	}
	want := SideB
	if accA {
		want = SideA
	}
	if r.AcceptedBy != want {
		return fmt.Errorf("dfacmp: AcceptedBy is %v, want %v", r.AcceptedBy, want)
	}
	if len(r.Path) != len(r.Word)+1 {
		return fmt.Errorf("dfacmp: path has %d steps, want %d", len(r.Path), len(r.Word)+1)
	}
	cur := ProductState{a.Start, b.Start}
	if r.Path[0].At != cur || r.Path[0].Via != "" {
		return fmt.Errorf("dfacmp: path root is %+v, want %+v with empty Via", r.Path[0], cur)
	}
	for i, sym := range r.Word {
		if r.Path[i+1].Via != sym {
			return fmt.Errorf("dfacmp: path step %d has Via %q, want %q", i+1, r.Path[i+1].Via, sym)
		}
		cur = ProductState{a.Step(cur.A, sym), b.Step(cur.B, sym)}
		if r.Path[i+1].At != cur {
			return fmt.Errorf("dfacmp: path step %d is %+v, want %+v", i+1, r.Path[i+1].At, cur)
		}
	}
	return nil
}

// EnumerateCheck cross-checks r against a brute-force enumeration of all
// words up to maxLen symbols over the union alphabet. For a counterexample
// it confirms that no shorter or lexicographically smaller distinguishing
// word exists within the bound; for an equivalence claim it confirms that
// no word within the bound distinguishes the automata. It fails if any
// simulated word traverses an unexpanded fragment.
func EnumerateCheck(a, b *Automaton, maxLen int, r Result) error {
	alphabet := unionAlphabet(a.Alphabet, b.Alphabet)
	var firstDiff []Symbol
	queue := [][]Symbol{{}}
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		accA, okA := a.Run(w)
		accB, okB := b.Run(w)
		if !okA || !okB {
			return fmt.Errorf("dfacmp: word %s traverses unexpanded fragments", quoteWord(w))
		}
		if accA != accB {
			firstDiff = w
			break
		}
		if len(w) < maxLen {
			for _, sym := range alphabet {
				queue = append(queue, appendWord(w, sym))
			}
		}
	}
	switch r.Kind {
	case Counterexample:
		if firstDiff == nil {
			if len(r.Word) <= maxLen {
				return fmt.Errorf("dfacmp: claimed counterexample %s, but no word up to length %d distinguishes the automata", quoteWord(r.Word), maxLen)
			}
			return nil // the counterexample is longer than the enumeration bound
		}
		if !equalWord(firstDiff, r.Word) {
			return fmt.Errorf("dfacmp: counterexample %s is not minimal; enumeration found %s", quoteWord(r.Word), quoteWord(firstDiff))
		}
	case Equivalent:
		if firstDiff != nil {
			return fmt.Errorf("dfacmp: claimed equivalence, but %s distinguishes the automata", quoteWord(firstDiff))
		}
	}
	return nil
}

// FullProductCheck validates r against a complete search of the product
// graph. Both automata must be fully expanded; otherwise it fails, since
// neither equivalence nor counterexample minimality may be claimed before
// full expansion. It re-runs the search from scratch, so it confirms cached
// results as well as the minimality of counterexamples.
func FullProductCheck(a, b *Automaton, r Result) error {
	if ids := unexpandedFragments(a, b); len(ids) > 0 {
		return fmt.Errorf("dfacmp: automata not fully expanded (blocked by %v)", ids)
	}
	fresh := Compare(a, b)
	if fresh.Kind == Unknown {
		return fmt.Errorf("dfacmp: internal error: search blocked on fully expanded automata")
	}
	if fresh.Kind != r.Kind {
		return fmt.Errorf("dfacmp: full product check yields %v, result claims %v", fresh.Kind, r.Kind)
	}
	if r.Kind == Counterexample {
		if !equalWord(fresh.Word, r.Word) {
			return fmt.Errorf("dfacmp: counterexample %s is not the shortest lexicographically smallest; want %s", quoteWord(r.Word), quoteWord(fresh.Word))
		}
		return VerifyCounterexample(a, b, r)
	}
	return nil
}
