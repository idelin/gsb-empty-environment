package vpa

import "fmt"

// This file is the independent checker. Its simulator (naiveOutcome) is a
// deliberately naive recursive interpreter that linearly scans the transition
// tables and shares no code with VPA.Run or the BFS in diff.go, so agreement
// between the two is meaningful evidence rather than the same bug twice.

// naiveOutcome re-simulates a single word from scratch. OutOfRange means the
// word needed a deeper stack than maxDepth allows; it is not a rejection.
func naiveOutcome(v *VPA, word []string, maxDepth int) Outcome {
	var rec func(q, pos, depth int, stack []string) Outcome
	rec = func(q, pos, depth int, stack []string) Outcome {
		if pos == len(word) {
			if v.Finals[q] {
				return Accept
			}
			return Reject
		}
		sym := word[pos]
		kind, ok := v.Alphabet[sym]
		if !ok {
			return Reject
		}
		switch kind {
		case Call:
			if depth+1 > maxDepth {
				return OutOfRange
			}
			for key, t := range v.CallTrans {
				if key.state == q && key.sym == sym {
					ns := make([]string, len(stack)+1)
					copy(ns, stack)
					ns[len(stack)] = t.push
					return rec(t.next, pos+1, depth+1, ns)
				}
			}
			return Reject
		case Return:
			if len(stack) == 0 {
				for key, next := range v.ReturnEmpty {
					if key.state == q && key.sym == sym {
						return rec(next, pos+1, depth, stack)
					}
				}
				return Reject
			}
			top := stack[len(stack)-1]
			for key, next := range v.ReturnTrans {
				if key.state == q && key.sym == sym && key.top == top {
					return rec(next, pos+1, depth-1, stack[:len(stack)-1])
				}
			}
			return Reject
		default: // Internal
			for key, next := range v.InternalTrans {
				if key.state == q && key.sym == sym {
					return rec(next, pos+1, depth, stack)
				}
			}
			return Reject
		}
	}
	return rec(v.Initial, 0, 0, nil)
}

// VerifyReport summarises an exhaustive word-by-word re-check.
type VerifyReport struct {
	MaxLenChecked   int
	InRangeWords    int         // words fully simulated (depth stayed <= maxDepth)
	OutOfRangeWords int         // words pruned at the first depth-limit hit
	FirstDiffLen    int         // length of a shortest distinguishing word, -1 if none
	DiffCountByLen  map[int]int // distinguishing words per length
	Example         []string    // a shortest distinguishing word found by brute force
}

// Exhaustive enumerates every word over the alphabet with length <= maxLen,
// word by word, and compares both automata using the naive simulator. Words
// whose stack depth would exceed maxDepth are pruned and counted in
// OutOfRangeWords; they are never used as evidence that the automata agree.
func Exhaustive(a, b *VPA, maxLen, maxDepth int) VerifyReport {
	rep := VerifyReport{
		MaxLenChecked:  maxLen,
		FirstDiffLen:   -1,
		DiffCountByLen: map[int]int{},
	}
	forEachWord(a, maxLen, maxDepth, func(word []string) {
		oa := naiveOutcome(a, word, maxDepth)
		ob := naiveOutcome(b, word, maxDepth)
		if oa == OutOfRange || ob == OutOfRange {
			// Unreachable: enumeration already prunes deep words.
			rep.OutOfRangeWords++
			return
		}
		rep.InRangeWords++
		if (oa == Accept) != (ob == Accept) {
			rep.DiffCountByLen[len(word)]++
			// Enumeration is depth-first, not level-ordered: keep the minimum.
			if rep.FirstDiffLen == -1 || len(word) < rep.FirstDiffLen {
				rep.FirstDiffLen = len(word)
				rep.Example = append([]string(nil), word...)
			}
		}
	}, &rep.OutOfRangeWords)
	return rep
}

// ConfirmMinimal independently verifies that word is a shortest
// distinguishing word within the depth bound: it re-simulates the word
// itself and exhaustively checks every strictly shorter word. It returns a
// human-readable explanation either way.
func ConfirmMinimal(a, b *VPA, word []string, maxDepth int) (bool, string) {
	oa := naiveOutcome(a, word, maxDepth)
	ob := naiveOutcome(b, word, maxDepth)
	if oa == OutOfRange || ob == OutOfRange {
		return false, fmt.Sprintf("word %v is out of range at depth %d; cannot witness anything", word, maxDepth)
	}
	if (oa == Accept) == (ob == Accept) {
		return false, fmt.Sprintf("word %v does not distinguish: %s=%s, %s=%s",
			word, a.Name, oa, b.Name, ob)
	}
	shorter := -1
	var counter []string
	var pruned int
	forEachWord(a, len(word)-1, maxDepth, func(w []string) {
		if shorter != -1 {
			return
		}
		ra := naiveOutcome(a, w, maxDepth)
		rb := naiveOutcome(b, w, maxDepth)
		if ra != OutOfRange && rb != OutOfRange && (ra == Accept) != (rb == Accept) {
			shorter = len(w)
			counter = append([]string(nil), w...)
		}
	}, &pruned)
	if shorter != -1 {
		return false, fmt.Sprintf("word %v is not minimal: shorter distinguishing word %v of length %d exists",
			word, counter, shorter)
	}
	return true, fmt.Sprintf("word %v distinguishes (%s=%s, %s=%s) and no shorter word of length<%d does; length %d is minimal",
		word, a.Name, oa, b.Name, ob, len(word), len(word))
}

// forEachWord depth-first enumerates every word of length <= maxLen whose
// stack depth never exceeds maxDepth, invoking fn for each. Pruned
// extensions (first symbol that would exceed the depth bound) are counted
// into pruned; their longer extensions are excluded as well.
func forEachWord(a *VPA, maxLen, maxDepth int, fn func(word []string), pruned *int) {
	symbols := a.Symbols()
	var word []string
	var rec func(depth int)
	rec = func(depth int) {
		fn(word)
		if len(word) == maxLen {
			return
		}
		for _, sym := range symbols {
			nd := depth
			switch a.Alphabet[sym] {
			case Call:
				nd = depth + 1
			case Return:
				if depth > 0 {
					nd = depth - 1
				}
			}
			if nd > maxDepth {
				*pruned++
				continue
			}
			word = append(word, sym)
			rec(nd)
			word = word[:len(word)-1]
		}
	}
	rec(0)
}
