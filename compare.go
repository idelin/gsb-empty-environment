package dfacmp

import (
	"container/heap"
	"fmt"
	"sort"
	"strings"
)

// Kind is the outcome of a comparison.
type Kind int

const (
	// Equivalent means the full product graph was explored, every fragment
	// is expanded, and no distinguishing word exists.
	Equivalent Kind = iota
	// Counterexample means a distinguishing word was found.
	Counterexample
	// Unknown means unexpanded fragments block the decision; Blocking
	// lists the fragment ids involved.
	Unknown
)

func (k Kind) String() string {
	switch k {
	case Equivalent:
		return "equivalent"
	case Counterexample:
		return "counterexample"
	default:
		return "unknown"
	}
}

// Side identifies which automaton accepts a counterexample.
type Side int

const (
	Neither Side = iota
	SideA
	SideB
)

func (s Side) String() string {
	switch s {
	case SideA:
		return "A"
	case SideB:
		return "B"
	default:
		return "neither"
	}
}

// ProductState is a pair of states, one from each automaton.
type ProductState struct {
	A, B StateID
}

// PathStep is one node of the product-state path of a distinguishing word.
// Via is the symbol used to arrive at At; it is empty for the root.
type PathStep struct {
	At  ProductState
	Via Symbol
}

// Result is the outcome of a comparison.
type Result struct {
	Kind Kind
	// Word is the shortest, lexicographically smallest distinguishing
	// word when Kind is Counterexample.
	Word []Symbol
	// Path is the product-state path of Word (len(Path) == len(Word)+1).
	Path []PathStep
	// AcceptedBy tells which side accepts Word.
	AcceptedBy Side
	// Blocking lists the unexpanded fragment ids when Kind is Unknown.
	Blocking []FragmentID
	// VersionA and VersionB are the automaton versions the result is
	// bound to.
	VersionA, VersionB uint64
}

func (r Result) String() string {
	switch r.Kind {
	case Equivalent:
		return fmt.Sprintf("equivalent (A@v%d, B@v%d)", r.VersionA, r.VersionB)
	case Unknown:
		return fmt.Sprintf("unknown, blocked by fragments [%s] (A@v%d, B@v%d)",
			strings.Join(r.Blocking, ", "), r.VersionA, r.VersionB)
	default:
		return fmt.Sprintf("counterexample %s accepted by %s (A@v%d, B@v%d)",
			quoteWord(r.Word), r.AcceptedBy, r.VersionA, r.VersionB)
	}
}

func quoteWord(w []Symbol) string {
	if len(w) == 0 {
		return "ε"
	}
	quoted := make([]string, len(w))
	for i, s := range w {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, " ")
}

// node is a frontier entry of the product search.
type node struct {
	ps   ProductState
	word []Symbol
}

// nodeHeap orders frontier entries by (word length, lexicographic order),
// so the first distinguishing word popped is the shortest and, among the
// shortest, the lexicographically smallest.
type nodeHeap []node

func (h nodeHeap) Len() int { return len(h) }
func (h nodeHeap) Less(i, j int) bool {
	a, b := h[i].word, h[j].word
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for k := range a {
		if a[k] != b[k] {
			return a[k] < b[k]
		}
	}
	return false
}
func (h nodeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x any)   { *h = append(*h, x.(node)) }
func (h *nodeHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Search is an incremental equivalence search over the product graph of two
// automata. Words are explored in (length, lexicographic) order. A Search
// can be resumed with Resume after blocking fragments have been expanded;
// expansions only add previously unknown information, so states already
// visited stay valid.
type Search struct {
	a, b     *Automaton
	alphabet []Symbol
	visited  map[ProductState]bool
	blocked  map[ProductState]node
	pq       nodeHeap
}

// NewSearch starts a search from the pair of start states.
func NewSearch(a, b *Automaton) *Search {
	s := &Search{
		a:        a,
		b:        b,
		alphabet: unionAlphabet(a.Alphabet, b.Alphabet),
		visited:  map[ProductState]bool{},
		blocked:  map[ProductState]node{},
	}
	heap.Push(&s.pq, node{ps: ProductState{a.Start, b.Start}})
	return s
}

// Compare runs a one-shot equivalence search over a and b. It reports
// Equivalent only when no unexpanded fragments remain anywhere; otherwise,
// if no reachable difference exists, it reports Unknown with the blocking
// fragment ids.
func Compare(a, b *Automaton) Result {
	return NewSearch(a, b).Run()
}

// Run explores until a counterexample is found, the frontier is exhausted,
// or only opaque states remain.
func (s *Search) Run() Result {
	for s.pq.Len() > 0 {
		n := heap.Pop(&s.pq).(node)
		if s.visited[n.ps] {
			continue
		}
		sa, sb := s.a.state(n.ps.A), s.b.state(n.ps.B)
		if sa.opaque() || sb.opaque() {
			if _, ok := s.blocked[n.ps]; !ok {
				s.blocked[n.ps] = n
			}
			continue
		}
		s.visited[n.ps] = true
		if sa.accepting() != sb.accepting() {
			return s.counterexample(n)
		}
		for _, sym := range s.alphabet {
			heap.Push(&s.pq, node{
				ps:   ProductState{sa.next(sym), sb.next(sym)},
				word: appendWord(n.word, sym),
			})
		}
	}
	if len(s.blocked) > 0 {
		return Result{Kind: Unknown, Blocking: s.blockingFragments()}
	}
	if rest := unexpandedFragments(s.a, s.b); len(rest) > 0 {
		return Result{Kind: Unknown, Blocking: rest}
	}
	return Result{Kind: Equivalent}
}

// Resume continues the search after some blocking fragments have been
// expanded in the underlying automata. Blocked product states that are now
// fully known are re-queued at their original (length, lexicographic)
// position, preserving the minimality guarantee.
func (s *Search) Resume() Result {
	for ps, n := range s.blocked {
		if !s.a.state(ps.A).opaque() && !s.b.state(ps.B).opaque() {
			delete(s.blocked, ps)
			heap.Push(&s.pq, n)
		}
	}
	return s.Run()
}

func (s *Search) counterexample(n node) Result {
	path := make([]PathStep, 0, len(n.word)+1)
	cur := ProductState{s.a.Start, s.b.Start}
	path = append(path, PathStep{At: cur})
	for _, sym := range n.word {
		cur = ProductState{s.a.Step(cur.A, sym), s.b.Step(cur.B, sym)}
		path = append(path, PathStep{At: cur, Via: sym})
	}
	side := SideB
	if acc, _ := s.a.IsAccepting(cur.A); acc {
		side = SideA
	}
	return Result{
		Kind:       Counterexample,
		Word:       append([]Symbol(nil), n.word...),
		Path:       path,
		AcceptedBy: side,
	}
}

func (s *Search) blockingFragments() []FragmentID {
	set := map[FragmentID]bool{}
	for ps := range s.blocked {
		if id := opaqueFragment(s.a.state(ps.A)); id != "" {
			set[id] = true
		}
		if id := opaqueFragment(s.b.state(ps.B)); id != "" {
			set[id] = true
		}
	}
	return sortedIds(set)
}

func opaqueFragment(st *State) FragmentID {
	if st != nil && st.Fragment != nil && !st.Fragment.Expanded {
		return st.Fragment.ID
	}
	return ""
}

func unexpandedFragments(a, b *Automaton) []FragmentID {
	set := map[FragmentID]bool{}
	for _, id := range a.UnexpandedFragments() {
		set[id] = true
	}
	for _, id := range b.UnexpandedFragments() {
		set[id] = true
	}
	return sortedIds(set)
}

func sortedIds(set map[FragmentID]bool) []FragmentID {
	out := make([]FragmentID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func unionAlphabet(x, y []Symbol) []Symbol {
	set := map[Symbol]bool{}
	for _, s := range x {
		set[s] = true
	}
	for _, s := range y {
		set[s] = true
	}
	out := make([]Symbol, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func appendWord(w []Symbol, sym Symbol) []Symbol {
	out := make([]Symbol, len(w)+1)
	copy(out, w)
	out[len(w)] = sym
	return out
}

func equalWord(x, y []Symbol) bool {
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
