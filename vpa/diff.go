package vpa

import (
	"fmt"
	"strings"
)

// DiffResult is the outcome of a bounded shortest-distinguishing-word search.
type DiffResult struct {
	Found      bool
	Word       []string // a shortest distinguishing word (lexicographically first)
	Reason     string   // why the word distinguishes the two automata
	RunA, RunB Run      // full traces of both automata on Word
	Explored   int      // product configurations visited by the BFS
	PrunedDeep int      // successor words dropped because depth would exceed MaxDepth
	MaxLen     int
	MaxDepth   int
}

// Summary renders the boundedness disclaimer: a negative result is never a
// proof of equivalence, because deep words were excluded from the search.
func (r DiffResult) Summary() string {
	if r.Found {
		return fmt.Sprintf("distinguishing word of length %d found within length<=%d, depth<=%d",
			len(r.Word), r.MaxLen, r.MaxDepth)
	}
	return fmt.Sprintf("no distinguishing word with length<=%d and stack depth<=%d; "+
		"this is NOT an equivalence proof: %d successor words were excluded by the depth bound",
		r.MaxLen, r.MaxDepth, r.PrunedDeep)
}

// productNode is one BFS configuration: both control states plus both stacks.
// Stack depth is a property of the word alone (calls push, matched returns
// pop), so both stacks always have the same depth while both automata are
// alive; a trapped automaton keeps a nil stack as a normal form.
type productNode struct {
	qa, qb int
	stackA []string
	stackB []string
	depth  int
	word   []string
}

// ShortestDistinguishingWord finds a shortest word w with |w| <= maxLen whose
// stack depth never exceeds maxDepth and which exactly one of the automata
// accepts. The search is a breadth-first walk over the product of both
// automata, so the first hit is globally shortest within the bounds. Words
// that would exceed maxDepth are pruned (counted in PrunedDeep) and never
// treated as accepted, rejected, or evidence of equivalence.
func ShortestDistinguishingWord(a, b *VPA, maxLen, maxDepth int) (DiffResult, error) {
	res := DiffResult{MaxLen: maxLen, MaxDepth: maxDepth}
	if !SameAlphabet(a, b) {
		return res, fmt.Errorf("automata %q and %q have different alphabets", a.Name, b.Name)
	}
	symbols := a.Symbols()

	accepts := func(v *VPA, q int) bool { return q != -1 && v.Finals[q] }
	distinguishes := func(qa, qb int) bool { return accepts(a, qa) != accepts(b, qb) }

	key := func(qa, qb int, sa, sb []string) string {
		var sbld strings.Builder
		fmt.Fprintf(&sbld, "%d|%d|", qa, qb)
		if qa != -1 {
			sbld.WriteString(strings.Join(sa, "\x00"))
		}
		sbld.WriteString("|")
		if qb != -1 {
			sbld.WriteString(strings.Join(sb, "\x00"))
		}
		return sbld.String()
	}

	start := productNode{qa: a.Initial, qb: b.Initial, depth: 0}
	queue := []productNode{start}
	seen := map[string]bool{key(start.qa, start.qb, nil, nil): true}

	finish := func(n productNode) (DiffResult, error) {
		res.Found = true
		res.Word = n.word
		res.RunA = a.Run(n.word, maxDepth)
		res.RunB = b.Run(n.word, maxDepth)
		res.Reason = fmt.Sprintf(
			"after %d symbol(s): %s stops in state %s (%s), %s stops in state %s (%s)",
			len(n.word),
			a.Name, stateName(res.RunA.FinalState), finalNote(a, res.RunA.FinalState),
			b.Name, stateName(res.RunB.FinalState), finalNote(b, res.RunB.FinalState))
		return res, nil
	}

	for head := 0; head < len(queue); head++ {
		n := queue[head]
		res.Explored++
		if distinguishes(n.qa, n.qb) {
			return finish(n)
		}
		if len(n.word) >= maxLen {
			continue
		}
		for _, sym := range symbols {
			nd := n.depth
			switch a.Alphabet[sym] {
			case Call:
				nd++
			case Return:
				if nd > 0 {
					nd--
				}
			}
			if nd > maxDepth {
				res.PrunedDeep++
				continue
			}
			nqa, nstackA := stepOne(a, n.qa, n.stackA, sym)
			nqb, nstackB := stepOne(b, n.qb, n.stackB, sym)
			k := key(nqa, nqb, nstackA, nstackB)
			if seen[k] {
				continue
			}
			seen[k] = true
			w := make([]string, len(n.word)+1)
			copy(w, n.word)
			w[len(n.word)] = sym
			queue = append(queue, productNode{
				qa: nqa, qb: nqb,
				stackA: nstackA, stackB: nstackB,
				depth: nd, word: w,
			})
		}
	}
	return res, nil
}

// stepOne advances one automaton by one symbol. State -1 is the implicit
// trap: once trapped, always trapped, and the stack normal form is nil.
func stepOne(v *VPA, q int, stack []string, sym string) (int, []string) {
	if q == -1 {
		return -1, nil
	}
	switch v.Alphabet[sym] {
	case Call:
		t, ok := v.CallTrans[callKey{q, sym}]
		if !ok {
			return -1, nil
		}
		ns := make([]string, len(stack)+1)
		copy(ns, stack)
		ns[len(stack)] = t.push
		return t.next, ns
	case Return:
		if len(stack) == 0 {
			next, ok := v.ReturnEmpty[stateSym{q, sym}]
			if !ok {
				return -1, nil
			}
			return next, stack
		}
		next, ok := v.ReturnTrans[returnKey{q, sym, stack[len(stack)-1]}]
		if !ok {
			return -1, nil
		}
		ns := make([]string, len(stack)-1)
		copy(ns, stack[:len(stack)-1])
		return next, ns
	default: // Internal
		next, ok := v.InternalTrans[stateSym{q, sym}]
		if !ok {
			return -1, nil
		}
		return next, stack
	}
}

func stateName(q int) string {
	if q == -1 {
		return "trap"
	}
	return fmt.Sprintf("%d", q)
}

func finalNote(v *VPA, q int) string {
	switch {
	case q == -1:
		return "implicit trap, not final: reject"
	case v.Finals[q]:
		return "final state: accept"
	default:
		return "not final: reject"
	}
}
