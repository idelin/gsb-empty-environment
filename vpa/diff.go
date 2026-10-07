package vpa

import (
	"fmt"
	"strings"
)

// DiffResult describes the shortest distinguishing word found within the
// bounded range, or records that none exists within it.
type DiffResult struct {
	Found      bool
	Word       []int
	Accept1    bool
	Accept2    bool
	Trace1     []Step
	Trace2     []Step
	Reason     string
	Explored   int // product configurations dequeued
	PrunedDeep int // successor words dropped for exceeding the depth bound
	MaxLen     int
	MaxDepth   int
}

type pnode struct {
	q1, q2 int
	s1, s2 []int
	word   []int
}

func keyOf(q1, q2 int, s1, s2 []int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d,%d|", q1, q2)
	for _, x := range s1 {
		fmt.Fprintf(&b, "%d.", x)
	}
	b.WriteByte('|')
	for _, x := range s2 {
		fmt.Fprintf(&b, "%d.", x)
	}
	return b.String()
}

// FindDistinguishing searches the product of a and b in BFS
// (shortest-first) order for a word that exactly one of them accepts.
// Words whose stack depth would exceed maxDepth are out of range: they
// are pruned, never treated as rejections, and never used as
// counterexamples. If no word is found, equivalence is established only
// within length <= maxLen and depth <= maxDepth.
func FindDistinguishing(a, b *Automaton, maxLen, maxDepth int) (*DiffResult, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	if len(a.Kinds) != len(b.Kinds) {
		return nil, fmt.Errorf("alphabet size mismatch: %d vs %d", len(a.Kinds), len(b.Kinds))
	}
	for s := range a.Kinds {
		if a.Kinds[s] != b.Kinds[s] {
			return nil, fmt.Errorf("symbol %s classified differently: %s vs %s", a.SymName(s), a.Kinds[s], b.Kinds[s])
		}
	}
	res := &DiffResult{MaxLen: maxLen, MaxDepth: maxDepth}
	check := func(q1, q2 int, word []int) bool {
		f1, f2 := a.Finals[q1], b.Finals[q2]
		if f1 == f2 {
			return false
		}
		res.Found = true
		res.Word = append([]int{}, word...)
		res.Accept1, res.Accept2 = f1, f2
		r1 := a.Run(word, maxDepth)
		r2 := b.Run(word, maxDepth)
		res.Trace1, res.Trace2 = r1.Steps, r2.Steps
		res.Reason = explain(a, b, word, r1, r2)
		return true
	}
	root := pnode{q1: a.Initial, q2: b.Initial}
	seen := map[string]bool{keyOf(root.q1, root.q2, nil, nil): true}
	queue := []pnode{root}
	if check(root.q1, root.q2, nil) {
		return res, nil
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		res.Explored++
		if len(cur.word) >= maxLen {
			continue
		}
		for sym := range a.Kinds {
			nq1, ns1, _, oor1 := a.step(cur.q1, cur.s1, sym, maxDepth)
			nq2, ns2, _, oor2 := b.step(cur.q2, cur.s2, sym, maxDepth)
			if oor1 || oor2 {
				// Depth is a word-level property: both automata always
				// have equal stack height, so oor1 == oor2.
				res.PrunedDeep++
				continue
			}
			nw := append(append([]int{}, cur.word...), sym)
			k := keyOf(nq1, nq2, ns1, ns2)
			if seen[k] {
				continue
			}
			seen[k] = true
			if check(nq1, nq2, nw) {
				return res, nil
			}
			queue = append(queue, pnode{nq1, nq2, ns1, ns2, nw})
		}
	}
	return res, nil
}

func eqStack(x, y []int) bool {
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

// explain pinpoints why the word distinguishes the two automata: the
// final verdicts, and the first step where the two configurations
// (state + full stack contents) diverge.
func explain(a, b *Automaton, word []int, r1, r2 RunResult) string {
	var sb strings.Builder
	verdict := func(r RunResult) string {
		if r.Accepted {
			return "accepts"
		}
		return "rejects"
	}
	fmt.Fprintf(&sb, "%s %s (ends in state q%d), %s %s (ends in state q%d)",
		a.Name, verdict(r1), r1.FinalState, b.Name, verdict(r2), r2.FinalState)
	q1, q2 := a.Initial, b.Initial
	var s1, s2 []int
	diverged := false
	for i := range word {
		nq1, ns1, _, _ := a.step(q1, s1, word[i], 1<<30)
		nq2, ns2, _, _ := b.step(q2, s2, word[i], 1<<30)
		if nq1 != nq2 || !eqStack(ns1, ns2) {
			fmt.Fprintf(&sb, "; configurations first diverge at step %d on %q (%s): %s -> (q%d, stack %s) but %s -> (q%d, stack %s)",
				i+1, a.SymName(word[i]), a.Kinds[word[i]],
				a.Name, nq1, a.FormatStack(ns1),
				b.Name, nq2, b.FormatStack(ns2))
			diverged = true
			break
		}
		q1, q2, s1, s2 = nq1, nq2, ns1, ns2
	}
	if !diverged {
		sb.WriteString("; configurations never diverge, so the difference lies in the final-state sets themselves")
	}
	return sb.String()
}

// BruteForce independently enumerates every word of length 0..maxLen
// over the shared alphabet, skipping only words that leave the depth
// range, and simulates both automata on each with Simulate (which shares
// no code with the product search). It returns the first distinguishing
// word in length-then-lexicographic order. checked counts the in-range
// words actually compared; out-of-range words are excluded from the
// comparison entirely, never counted as equivalent. Cost grows as
// |alphabet|^maxLen, so use it with small alphabets and lengths.
func BruteForce(a, b *Automaton, maxLen, maxDepth int) (word []int, accept1, accept2 bool, found bool, checked int) {
	n := len(a.Kinds)
	for l := 0; l <= maxLen && !found; l++ {
		w := make([]int, 0, l)
		var rec func(depth int) bool
		rec = func(depth int) bool {
			if len(w) == l {
				checked++
				f1, ok1 := Simulate(a, w, maxDepth)
				f2, ok2 := Simulate(b, w, maxDepth)
				if !ok1 || !ok2 {
					return false // unreachable: enumeration prunes by depth
				}
				if f1 != f2 {
					word = append([]int{}, w...)
					accept1, accept2, found = f1, f2, true
					return true
				}
				return false
			}
			for s := 0; s < n; s++ {
				nd := depth
				switch a.Kinds[s] {
				case Call:
					nd = depth + 1
					if nd > maxDepth {
						continue // out of range: skipped, not rejected
					}
				case Return:
					if depth > 0 {
						nd = depth - 1
					}
				}
				w = append(w, s)
				if rec(nd) {
					return true
				}
				w = w[:len(w)-1]
			}
			return false
		}
		if rec(0) {
			return
		}
	}
	return
}
