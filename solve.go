package pathpat

import (
	"sort"
	"strconv"
	"strings"
)

// Verdict is the outcome of Solve.
type Verdict int

const (
	// Witness means an allowed and not denied path exists; Result.Witness
	// holds one with the fewest segments.
	Witness Verdict = iota
	// None means no allowed path escapes the deny set (proved by exhausting
	// the finite search space).
	None
	// Unknown means the budget was insufficient to decide.
	Unknown
)

func (v Verdict) String() string {
	switch v {
	case Witness:
		return "witness"
	case None:
		return "none"
	default:
		return "unknown"
	}
}

// Budget bounds the search. A non-positive field means "no limit".
type Budget struct {
	// MaxExpansions caps how many search states may be expanded.
	MaxExpansions int
	// MaxDepth caps the segment count of candidate paths.
	MaxDepth int
}

// Result is the outcome of Solve. Witness holds the path segments of a
// shortest allowed-and-not-denied path: an empty slice is the empty path,
// []string{""} is the root path.
type Result struct {
	Verdict Verdict
	Witness []string
}

// Solve decides whether some path matches at least one allow pattern and no
// deny pattern, returning a shortest such path when one exists.
//
// The decision enumerates candidate paths over a finite alphabet — every
// literal segment occurring in the patterns plus one fresh symbol standing
// for any other segment — in increasing segment count. Enumeration states
// that agree on the set of NFA states reachable in both the allow and the
// deny automata are merged, which preserves the enumeration semantics while
// making the search space finite: if every reachable state is exhausted
// without finding a witness, no witness exists (None). If the budget runs
// out first, the result is Unknown.
func Solve(allow, deny []Pattern, b Budget) Result {
	lits := collectLiterals(allow, deny)
	an := buildNFA(allow)
	dn := buildNFA(deny)
	nsym := len(lits) + 1 // literals plus the fresh symbol
	fresh := freshName(lits)

	startA := closure(an, an.starts)
	startD := closure(dn, dn.starts)
	startKey := keyOf(startA, startD)

	if accepting(an, dn, startA, startD) {
		return Result{Verdict: Witness, Witness: []string{}}
	}

	type node struct {
		a, d  []int
		key   string
		depth int
	}

	visited := map[string]bool{startKey: true}
	parents := map[string]trail{}
	queue := []node{{a: startA, d: startD, key: startKey}}
	truncated := false
	expansions := 0

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if b.MaxExpansions > 0 && expansions >= b.MaxExpansions {
			truncated = true
			break
		}
		expansions++
		if b.MaxDepth > 0 && cur.depth >= b.MaxDepth {
			truncated = true
			continue
		}
		for sym := 0; sym < nsym; sym++ {
			na := step(an, cur.a, sym, lits)
			nd := step(dn, cur.d, sym, lits)
			k := keyOf(na, nd)
			if visited[k] {
				continue
			}
			visited[k] = true
			parents[k] = trail{prev: cur.key, sym: sym}
			if accepting(an, dn, na, nd) {
				return Result{Verdict: Witness, Witness: reconstruct(parents, k, startKey, lits, fresh)}
			}
			queue = append(queue, node{a: na, d: nd, key: k, depth: cur.depth + 1})
		}
	}
	if truncated {
		return Result{Verdict: Unknown}
	}
	return Result{Verdict: None}
}

// BruteForce is a reference checker that literally enumerates every path
// over alphabet with at most maxDepth segments, shortest first, and returns
// the first path matching some allow pattern and no deny pattern. It is
// exponential in maxDepth and intended for validating Solve on small cases.
func BruteForce(allow, deny []Pattern, alphabet []string, maxDepth int) (bool, []string) {
	queue := [][]string{nil}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if AllowedAndNotDenied(allow, deny, p) {
			return true, p
		}
		if len(p) < maxDepth {
			for _, s := range alphabet {
				q := make([]string, len(p)+1)
				copy(q, p)
				q[len(p)] = s
				queue = append(queue, q)
			}
		}
	}
	return false, nil
}

// AllowedAndNotDenied reports whether path matches at least one allow
// pattern and no deny pattern.
func AllowedAndNotDenied(allow, deny []Pattern, path []string) bool {
	ok := false
	for _, p := range allow {
		if p.Match(path) {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	for _, p := range deny {
		if p.Match(path) {
			return false
		}
	}
	return true
}

// collectLiterals returns the sorted distinct literal segments of all
// patterns; together with one fresh symbol they form the search alphabet.
func collectLiterals(patSets ...[]Pattern) []string {
	seen := map[string]bool{}
	var lits []string
	for _, pats := range patSets {
		for _, p := range pats {
			for _, s := range p.Segs {
				if s.Kind == Lit && !seen[s.Lit] {
					seen[s.Lit] = true
					lits = append(lits, s.Lit)
				}
			}
		}
	}
	sort.Strings(lits)
	return lits
}

// freshName returns a segment name distinct from every literal, used to
// instantiate the fresh symbol in witnesses.
func freshName(lits []string) string {
	used := make(map[string]bool, len(lits))
	for _, l := range lits {
		used[l] = true
	}
	for i := 0; ; i++ {
		name := "seg" + strconv.Itoa(i)
		if !used[name] {
			return name
		}
	}
}

// matcher describes one NFA transition label.
type matcher struct {
	any bool   // matches any single segment ("*" or "**" self-loop)
	lit string // matches exactly this segment
}

type nfaEdge struct {
	m   matcher
	dst int
}

// nfa is an epsilon-NFA over path segments recognizing a union of patterns.
type nfa struct {
	n      int
	starts []int
	final  []bool
	eps    [][]int
	trans  [][]nfaEdge
}

func buildNFA(pats []Pattern) *nfa {
	n := &nfa{}
	addState := func() int {
		n.n++
		n.final = append(n.final, false)
		n.eps = append(n.eps, nil)
		n.trans = append(n.trans, nil)
		return n.n - 1
	}
	for _, p := range pats {
		cur := addState()
		n.starts = append(n.starts, cur)
		for _, seg := range p.Segs {
			if seg.Kind == StarStar {
				next := addState()
				// "**": skip via epsilon, or consume a segment and stay.
				n.eps[cur] = append(n.eps[cur], next)
				n.trans[cur] = append(n.trans[cur], nfaEdge{m: matcher{any: true}, dst: cur})
				cur = next
				continue
			}
			next := addState()
			m := matcher{any: seg.Kind == Star}
			if seg.Kind == Lit {
				m.lit = seg.Lit
			}
			n.trans[cur] = append(n.trans[cur], nfaEdge{m: m, dst: next})
			cur = next
		}
		n.final[cur] = true
	}
	return n
}

// closure returns the sorted epsilon-closure of states.
func closure(n *nfa, states []int) []int {
	seen := make([]bool, n.n)
	stack := append([]int(nil), states...)
	var out []int
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		stack = append(stack, n.eps[s]...)
	}
	sort.Ints(out)
	return out
}

// step moves a state subset on one input symbol and closes under epsilon.
// Symbol indices below len(lits) denote that literal; index len(lits) is
// the fresh symbol (any segment equal to no literal).
func step(n *nfa, states []int, sym int, lits []string) []int {
	var moved []int
	for _, s := range states {
		for _, e := range n.trans[s] {
			if matchSym(e.m, sym, lits) {
				moved = append(moved, e.dst)
			}
		}
	}
	if len(moved) == 0 {
		return nil
	}
	return closure(n, moved)
}

func matchSym(m matcher, sym int, lits []string) bool {
	if m.any {
		return true
	}
	if sym >= len(lits) {
		return false
	}
	return m.lit == lits[sym]
}

// accepting reports whether the allow subset accepts and the deny subset
// does not.
func accepting(an, dn *nfa, a, d []int) bool {
	ok := false
	for _, s := range a {
		if an.final[s] {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	for _, s := range d {
		if dn.final[s] {
			return false
		}
	}
	return true
}

func keyOf(a, d []int) string {
	return subsetKey(a) + "|" + subsetKey(d)
}

// trail records how a search state was reached: the previous state's key
// and the input symbol taken.
type trail struct {
	prev string
	sym  int
}

func subsetKey(s []int) string {
	var sb strings.Builder
	for i, v := range s {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Itoa(v))
	}
	return sb.String()
}

// reconstruct walks parent pointers back to the start state and renders the
// symbol sequence as path segments.
func reconstruct(parents map[string]trail, final, start string, lits []string, fresh string) []string {
	var rev []string
	for k := final; k != start; {
		p := parents[k]
		if p.sym < len(lits) {
			rev = append(rev, lits[p.sym])
		} else {
			rev = append(rev, fresh)
		}
		k = p.prev
	}
	w := make([]string, len(rev))
	for i, s := range rev {
		w[len(rev)-1-i] = s
	}
	return w
}
