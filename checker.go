package tdeq

import (
	"fmt"
	"sort"
)

// Status is the outcome of a bounded equivalence check.
type Status int

const (
	// Unknown means the exploration budget was exhausted before the
	// product graph was fully explored; the frontier can be resumed.
	Unknown Status = iota
	// Equivalent means both transducers produce identical concatenated
	// outputs for every input string of the bounded length.
	Equivalent
	// Inequivalent means a verified counterexample input was found.
	Inequivalent
)

func (s Status) String() string {
	switch s {
	case Equivalent:
		return "equivalent"
	case Inequivalent:
		return "inequivalent"
	default:
		return "unknown"
	}
}

// Result is the outcome of a check. For Inequivalent, Witness is an input
// string of length exactly Bound whose complete execution was verified to
// produce different outputs: OutA/OutB hold the two outputs and ErrA/ErrB
// mark which side produced an error output (missing transition).
type Result struct {
	Status  Status
	Witness string
	OutA    string
	OutB    string
	ErrA    bool
	ErrB    bool
}

// FrontierNode is an exported, serializable view of one product-graph
// frontier entry. Rem/Ahead encode the pending output difference: the
// outputs produced so far are equal except that the side indicated by
// Ahead (+1 for A, -1 for B) has additionally produced Rem. Diverged
// marks entries whose outputs are already known to differ (or where
// exactly one side errored); such entries only track whether both sides
// eventually error, which would make the outputs equal again.
type FrontierNode struct {
	QA, QB   int
	ErrA     bool
	ErrB     bool
	Diverged bool
	Rem      string
	Ahead    int8
	Depth    int
	Input    string
}

// key normalizes a frontier node for deduplication. The depth is part
// of the identity: equivalence is checked at the exact bounded length,
// so the remaining horizon changes the meaning of a product state.
// Behaviour from an errored side no longer depends on its frozen state,
// and a diverged entry does not depend on the pending difference.
func (n FrontierNode) key() FrontierNode {
	k := n
	k.Input = ""
	if k.ErrA {
		k.QA = -1
	}
	if k.ErrB {
		k.QB = -1
	}
	if k.Diverged {
		k.Rem = ""
		k.Ahead = 0
	}
	return k
}

// Snapshot captures the complete search state of a Checker so that an
// interrupted exploration can be recovered later, possibly in another
// process, with Resume.
type Snapshot struct {
	Bound   int
	Queue   []FrontierNode
	Visited []FrontierNode
}

// Checker explores the product graph of two transducers up to the public
// input length bound Bound. It is incremental: Run consumes a budget of
// node expansions and can be called repeatedly; Snapshot/Resume allow the
// frontier to be exported and restored.
type Checker struct {
	A     *Transducer
	B     *Transducer
	Bound int

	alpha   []rune
	queue   []FrontierNode
	visited map[FrontierNode]struct{}
	done    bool
	result  Result
}

// NewChecker prepares a bounded equivalence check between a and b for all
// input strings of length exactly bound.
func NewChecker(a, b *Transducer, bound int) (*Checker, error) {
	if a == nil || b == nil {
		return nil, fmt.Errorf("tdeq: nil transducer")
	}
	if bound < 0 {
		return nil, fmt.Errorf("tdeq: negative input length bound %d", bound)
	}
	c := &Checker{
		A:       a,
		B:       b,
		Bound:   bound,
		alpha:   unionAlphabet(a, b),
		visited: make(map[FrontierNode]struct{}),
	}
	initial := FrontierNode{QA: a.Initial, QB: b.Initial}
	c.visited[initial.key()] = struct{}{}
	c.queue = []FrontierNode{initial}
	return c, nil
}

// FrontierSize reports how many product-graph entries are currently
// waiting to be expanded.
func (c *Checker) FrontierSize() int { return len(c.queue) }

// Done reports whether the check has reached a definitive conclusion
// (Equivalent or Inequivalent).
func (c *Checker) Done() bool { return c.done }

// Run expands at most budget frontier entries and returns the current
// result. A negative budget means unlimited. If the frontier is not empty
// when the budget is exhausted the status is Unknown and the exploration
// can be continued by calling Run again or by exporting the frontier with
// Snapshot.
func (c *Checker) Run(budget int) Result {
	if c.done {
		return c.result
	}
	expanded := 0
	for len(c.queue) > 0 {
		if budget >= 0 && expanded >= budget {
			return Result{Status: Unknown}
		}
		n := c.queue[0]
		c.queue = c.queue[1:]
		expanded++
		if res, found := c.expand(n); found {
			c.done = true
			c.result = res
			return res
		}
	}
	c.done = true
	c.result = Result{Status: Equivalent}
	return c.result
}

// expand processes one frontier entry. Entries at the bounded depth are
// terminal: their accumulated output difference, if any, is verified by
// complete execution. Shallower entries generate one child per alphabet
// symbol.
func (c *Checker) expand(n FrontierNode) (Result, bool) {
	if n.Depth >= c.Bound {
		if n.Diverged || n.Rem != "" {
			return c.verify(n.Input)
		}
		return Result{}, false
	}
	for _, sym := range c.alpha {
		child, pruned := c.step(n, sym)
		if pruned {
			continue
		}
		child.Depth = n.Depth + 1
		child.Input = n.Input + string(sym)
		k := child.key()
		if _, seen := c.visited[k]; seen {
			continue
		}
		c.visited[k] = struct{}{}
		c.queue = append(c.queue, child)
	}
	return Result{}, false
}

// step advances both machines over one input symbol and combines the
// pending output difference with the freshly produced outputs.
func (c *Checker) step(n FrontierNode, sym rune) (FrontierNode, bool) {
	qa, errA := n.QA, n.ErrA
	var outA string
	if !errA {
		edge, defined := c.A.Edges[qa][sym]
		if !defined {
			errA = true
		} else {
			qa = edge.Next
			outA = edge.Output
		}
	}
	qb, errB := n.QB, n.ErrB
	var outB string
	if !errB {
		edge, defined := c.B.Edges[qb][sym]
		if !defined {
			errB = true
		} else {
			qb = edge.Next
			outB = edge.Output
		}
	}

	// Both sides errored: both outputs are error outputs, hence equal,
	// and this stays true for every extension. Prune the branch.
	if errA && errB {
		return FrontierNode{}, true
	}

	// Once the outputs are known to differ (including exactly one side
	// erroring), only the error flags matter: the branch dies if the
	// surviving side also errors, otherwise it stays diverged.
	if n.Diverged || errA || errB {
		return FrontierNode{QA: qa, QB: qb, ErrA: errA, ErrB: errB, Diverged: true}, false
	}

	// Neither side errored. Append the new outputs to the pending
	// difference and cancel the longest common prefix.
	remA, remB := "", ""
	switch n.Ahead {
	case 1:
		remA = n.Rem
	case -1:
		remB = n.Rem
	}
	remA += outA
	remB += outB
	i := 0
	for i < len(remA) && i < len(remB) && remA[i] == remB[i] {
		i++
	}
	remA, remB = remA[i:], remB[i:]
	switch {
	case remA == "" && remB == "":
		return FrontierNode{QA: qa, QB: qb}, false
	case remB == "":
		return FrontierNode{QA: qa, QB: qb, Rem: remA, Ahead: 1}, false
	case remA == "":
		return FrontierNode{QA: qa, QB: qb, Rem: remB, Ahead: -1}, false
	default:
		// Both sides have unconsumed output starting at the same
		// position with different bytes: the cumulative outputs differ.
		return FrontierNode{QA: qa, QB: qb, Diverged: true}, false
	}
}

// verify runs both machines completely on the witness candidate and only
// confirms a counterexample when the full executions really differ.
func (c *Checker) verify(witness string) (Result, bool) {
	outA, okA := c.A.Run(witness)
	outB, okB := c.B.Run(witness)
	if okA == okB && (!okA || outA == outB) {
		return Result{}, false
	}
	return Result{
		Status:  Inequivalent,
		Witness: witness,
		OutA:    outA,
		OutB:    outB,
		ErrA:    !okA,
		ErrB:    !okB,
	}, true
}

// Snapshot exports the current frontier and visited set so the
// exploration can be recovered later with Resume.
func (c *Checker) Snapshot() Snapshot {
	snap := Snapshot{Bound: c.Bound}
	snap.Queue = append(snap.Queue, c.queue...)
	for k := range c.visited {
		snap.Visited = append(snap.Visited, k)
	}
	return snap
}

// Resume restores a checker from a snapshot previously taken with
// Snapshot. The transducers must be the same ones (or structurally
// identical copies) used for the original check.
func Resume(a, b *Transducer, snap Snapshot) (*Checker, error) {
	c, err := NewChecker(a, b, snap.Bound)
	if err != nil {
		return nil, err
	}
	c.queue = append([]FrontierNode(nil), snap.Queue...)
	c.visited = make(map[FrontierNode]struct{}, len(snap.Visited))
	for _, k := range snap.Visited {
		c.visited[k.key()] = struct{}{}
	}
	return c, nil
}

// unionAlphabet merges both machines' alphabets. Symbols outside the
// union make both machines error immediately, so strings containing them
// can never distinguish the machines and are not explored.
func unionAlphabet(a, b *Transducer) []rune {
	seen := make(map[rune]bool)
	for _, sym := range a.Alphabet() {
		seen[sym] = true
	}
	for _, sym := range b.Alphabet() {
		seen[sym] = true
	}
	alpha := make([]rune, 0, len(seen))
	for sym := range seen {
		alpha = append(alpha, sym)
	}
	sort.Slice(alpha, func(i, j int) bool { return alpha[i] < alpha[j] })
	return alpha
}
