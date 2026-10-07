package dfeq

import (
	"container/heap"
	"sort"
	"strings"
)

// Kind is the outcome category of a comparison.
type Kind int

const (
	// Equivalent: both automata accept the same language and every
	// reachable state is fully expanded.
	Equivalent Kind = iota
	// Counterexample: the languages differ; Result.Word is the
	// shortest, lexicographically smallest distinguishing word.
	Counterexample
	// Unknown: no distinguishing word was found, but reachable states
	// still carry unexpanded fragments, so equivalence cannot be claimed.
	Unknown
)

func (k Kind) String() string {
	switch k {
	case Equivalent:
		return "Equivalent"
	case Counterexample:
		return "Counterexample"
	default:
		return "Unknown"
	}
}

// Result is the outcome of comparing two automata.
type Result struct {
	Kind Kind
	// Word is the distinguishing word (Counterexample only).
	Word []string
	// Path is the product-state path taken by Word, starting at the
	// pair of start states (Counterexample only).
	Path [][2]string
	// Blocking lists the IDs of the fragments that prevented a
	// definitive answer (Unknown only), sorted.
	Blocking []string
	// Versions records the DFA versions this result is bound to.
	Versions [2]uint64
}

// WordString renders the distinguishing word; the empty word is ε.
func (r Result) WordString() string {
	if len(r.Word) == 0 {
		return "ε"
	}
	return strings.Join(r.Word, " ")
}

// pair is a product-graph node.
type pair struct{ a, b string }

// frontierItem is a BFS queue entry ordered by (depth, word) so the
// first counterexample found is shortest and lexicographically smallest.
type frontierItem struct {
	node  pair
	word  []string
	depth int
}

// frontier is a priority queue in shortlex order.
type frontier []frontierItem

func (f frontier) Len() int { return len(f) }
func (f frontier) Less(i, j int) bool {
	if f[i].depth != f[j].depth {
		return f[i].depth < f[j].depth
	}
	a, b := f[i].word, f[j].word
	for k := 0; k < len(a) && k < len(b); k++ {
		if a[k] != b[k] {
			return a[k] < b[k]
		}
	}
	return len(a) < len(b)
}
func (f frontier) Swap(i, j int)       { f[i], f[j] = f[j], f[i] }
func (f *frontier) Push(x interface{}) { *f = append(*f, x.(frontierItem)) }
func (f *frontier) Pop() interface{} {
	old := *f
	n := len(old)
	it := old[n-1]
	*f = old[:n-1]
	return it
}

// exploration is the incremental state of a product-graph traversal.
type exploration struct {
	visited   map[pair]bool
	parent    map[pair]pair
	parentSym map[pair]string
	queue     frontier
	blocking  map[string]bool
	blocked   []pair // visited nodes skipped because of unexpanded fragments
	found     *pair
}

func newExploration() *exploration {
	return &exploration{
		visited:   map[pair]bool{},
		parent:    map[pair]pair{},
		parentSym: map[pair]string{},
		blocking:  map[string]bool{},
	}
}

// unionAlphabet merges both alphabets, sorted.
func unionAlphabet(a, b *DFA) []string {
	return normalizeAlphabet(append(append([]string(nil), a.Alphabet...), b.Alphabet...))
}

// step advances the exploration until it is exhausted or a
// counterexample is found. Previously blocked nodes are re-queued by
// the caller when fragments get resolved.
func (e *exploration) step(a, b *DFA, alphabet []string) {
	for e.queue.Len() > 0 && e.found == nil {
		it := heap.Pop(&e.queue).(frontierItem)
		cur := it.node
		if e.visited[cur] {
			continue
		}
		e.visited[cur] = true

		// Opaque states: record the blocking fragments and do not
		// expand — their outgoing behaviour may still change.
		if a.opaque(cur.a) || b.opaque(cur.b) {
			if id := a.fragmentID(cur.a); id != "" {
				e.blocking[id] = true
			}
			if id := b.fragmentID(cur.b); id != "" {
				e.blocking[id] = true
			}
			e.blocked = append(e.blocked, cur)
			continue
		}

		if a.Accepting(cur.a) != b.Accepting(cur.b) {
			node := cur
			e.found = &node
			return
		}

		for _, sym := range alphabet {
			next := pair{a.Step(cur.a, sym), b.Step(cur.b, sym)}
			if e.visited[next] {
				continue
			}
			if _, seen := e.parent[next]; !seen && next != cur {
				e.parent[next] = cur
				e.parentSym[next] = sym
			}
			word := append(append([]string(nil), it.word...), sym)
			heap.Push(&e.queue, frontierItem{node: next, word: word, depth: it.depth + 1})
		}
	}
}

// resume re-queues previously blocked nodes whose fragments have been
// resolved, so exploration continues incrementally. Words are rebuilt
// from the parent chain to preserve shortlex order. It returns false
// if nothing could be resumed.
func (e *exploration) resume(a, b *DFA) bool {
	start := pair{a.Start, b.Start}
	progressed := false
	remaining := e.blocked[:0]
	for _, node := range e.blocked {
		if a.opaque(node.a) || b.opaque(node.b) {
			remaining = append(remaining, node)
			continue
		}
		word := e.buildWord(node, start)
		delete(e.visited, node)
		heap.Push(&e.queue, frontierItem{node: node, word: word, depth: len(word)})
		progressed = true
	}
	e.blocked = remaining
	// Rebuild the blocking set from the still-unresolved nodes.
	e.blocking = map[string]bool{}
	for _, node := range e.blocked {
		if id := a.fragmentID(node.a); id != "" {
			e.blocking[id] = true
		}
		if id := b.fragmentID(node.b); id != "" {
			e.blocking[id] = true
		}
	}
	return progressed
}

// buildWord reconstructs the word leading to node.
func (e *exploration) buildWord(node pair, start pair) []string {
	var rev []string
	for node != start {
		rev = append(rev, e.parentSym[node])
		node = e.parent[node]
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// buildPath reconstructs the product-state path for word from start.
func buildPath(a, b *DFA, word []string) [][2]string {
	pa, pb := a.Start, b.Start
	path := [][2]string{{pa, pb}}
	for _, sym := range word {
		pa, pb = a.Step(pa, sym), b.Step(pb, sym)
		path = append(path, [2]string{pa, pb})
	}
	return path
}

// result assembles the final Result from the exploration state.
func (e *exploration) result(a, b *DFA) Result {
	res := Result{Versions: [2]uint64{a.Version, b.Version}}
	if e.found != nil {
		res.Kind = Counterexample
		res.Word = e.buildWord(*e.found, pair{a.Start, b.Start})
		res.Path = buildPath(a, b, res.Word)
		return res
	}
	if len(e.blocking) > 0 {
		res.Kind = Unknown
		for id := range e.blocking {
			res.Blocking = append(res.Blocking, id)
		}
		sort.Strings(res.Blocking)
		return res
	}
	res.Kind = Equivalent
	return res
}

// Compare checks whether a and b accept the same language.
//
// Missing transitions go to a rejecting sink; the empty word is
// checked first. If the languages differ, the shortest and
// lexicographically smallest distinguishing word is returned together
// with the product-state path. If reachable states still carry
// unexpanded fragments, the result is Unknown with the blocking
// fragment IDs — equivalence is never claimed before full expansion.
func Compare(a, b *DFA) Result {
	e := newExploration()
	start := pair{a.Start, b.Start}
	heap.Push(&e.queue, frontierItem{node: start})
	e.step(a, b, unionAlphabet(a, b))
	return e.result(a, b)
}
