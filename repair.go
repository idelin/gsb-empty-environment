// Package repair finds the minimum-cost edit script that transforms an
// input string into a string accepted by a deterministic finite automaton.
//
// Allowed edit operations are insertion, deletion and substitution, each
// with a positive integer cost. Among all minimum-cost repairs, the one
// producing the shortest output string is chosen; remaining ties are broken
// by lexicographic order of the output string.
package repair

import (
	"container/heap"
	"errors"
	"fmt"
)

// DFA is a deterministic finite automaton over a byte alphabet.
type DFA struct {
	NumStates int
	Alphabet  []byte  // distinct symbols; iteration order defines nothing, output lexicographic order is by byte value
	Delta     [][]int // Delta[state][symbolIndex] = next state
	Start     int
	Accept    []bool // len == NumStates
}

// Validate checks the automaton for structural consistency.
func (d *DFA) Validate() error {
	if d.NumStates <= 0 {
		return errors.New("repair: DFA must have at least one state")
	}
	if d.Start < 0 || d.Start >= d.NumStates {
		return fmt.Errorf("repair: start state %d out of range", d.Start)
	}
	if len(d.Accept) != d.NumStates {
		return fmt.Errorf("repair: Accept has length %d, want %d", len(d.Accept), d.NumStates)
	}
	if len(d.Alphabet) == 0 {
		return errors.New("repair: alphabet must not be empty")
	}
	seen := make(map[byte]bool, len(d.Alphabet))
	for _, s := range d.Alphabet {
		if seen[s] {
			return fmt.Errorf("repair: duplicate alphabet symbol %q", s)
		}
		seen[s] = true
	}
	if len(d.Delta) != d.NumStates {
		return fmt.Errorf("repair: Delta has %d rows, want %d", len(d.Delta), d.NumStates)
	}
	for q, row := range d.Delta {
		if len(row) != len(d.Alphabet) {
			return fmt.Errorf("repair: Delta[%d] has %d entries, want %d", q, len(row), len(d.Alphabet))
		}
		for _, next := range row {
			if next < 0 || next >= d.NumStates {
				return fmt.Errorf("repair: Delta[%d] leads to state %d out of range", q, next)
			}
		}
	}
	return nil
}

// Accepts reports whether the automaton accepts s.
func (d *DFA) Accepts(s string) bool {
	idx := make(map[byte]int, len(d.Alphabet))
	for i, a := range d.Alphabet {
		idx[a] = i
	}
	q := d.Start
	for i := 0; i < len(s); i++ {
		j, ok := idx[s[i]]
		if !ok {
			return false
		}
		q = d.Delta[q][j]
	}
	return d.Accept[q]
}

// Costs holds the positive integer costs of the three edit operations.
type Costs struct {
	Insert     int
	Delete     int
	Substitute int
}

func (c Costs) valid() bool {
	return c.Insert > 0 && c.Delete > 0 && c.Substitute > 0
}

// OpKind identifies the kind of an edit operation.
type OpKind int

const (
	// OpMatch consumes an input byte and emits it unchanged (cost 0).
	OpMatch OpKind = iota
	// OpInsert emits Out without consuming input.
	OpInsert
	// OpDelete consumes In without emitting anything.
	OpDelete
	// OpSubstitute consumes In and emits Out.
	OpSubstitute
)

// Op is a single step of an edit script.
type Op struct {
	Kind OpKind
	In   byte // consumed input byte (OpMatch, OpDelete, OpSubstitute)
	Out  byte // emitted byte (OpMatch, OpInsert, OpSubstitute)
}

func (o Op) String() string {
	switch o.Kind {
	case OpMatch:
		return fmt.Sprintf("match(%q)", o.In)
	case OpInsert:
		return fmt.Sprintf("insert(%q)", o.Out)
	case OpDelete:
		return fmt.Sprintf("delete(%q)", o.In)
	case OpSubstitute:
		return fmt.Sprintf("substitute(%q,%q)", o.In, o.Out)
	}
	return "unknown"
}

// ApplyScript applies an edit script to input and returns the output string.
// It fails if a consuming operation does not match the remaining input.
func ApplyScript(input string, script []Op) (string, error) {
	out := make([]byte, 0, len(input))
	pos := 0
	for k, op := range script {
		switch op.Kind {
		case OpMatch, OpDelete, OpSubstitute:
			if pos >= len(input) || input[pos] != op.In {
				return "", fmt.Errorf("repair: script op %d (%v) does not match input at position %d", k, op, pos)
			}
			pos++
			if op.Kind != OpDelete {
				out = append(out, op.Out)
			}
		case OpInsert:
			out = append(out, op.Out)
		default:
			return "", fmt.Errorf("repair: script op %d has unknown kind %d", k, op.Kind)
		}
	}
	if pos != len(input) {
		return "", fmt.Errorf("repair: script consumed %d of %d input bytes", pos, len(input))
	}
	return string(out), nil
}

// ScriptCost returns the total cost of a script under the given costs.
func ScriptCost(script []Op, c Costs) int {
	total := 0
	for _, op := range script {
		switch op.Kind {
		case OpInsert:
			total += c.Insert
		case OpDelete:
			total += c.Delete
		case OpSubstitute:
			total += c.Substitute
		}
	}
	return total
}

// Result is the outcome of a Repair call.
type Result struct {
	Cost   int    // total edit cost
	Output string // the accepted string produced by the script
	Script []Op   // edit script transforming the input into Output
}

// key is the ordering used during the search: cost, then output length,
// then output string lexicographically.
type key struct {
	cost int
	out  string
}

func lessKey(a, b key) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if len(a.out) != len(b.out) {
		return len(a.out) < len(b.out)
	}
	return a.out < b.out
}

// prioItem is a priority-queue entry.
type prioItem struct {
	node int
	k    key
}

type prioQueue []prioItem

func (p prioQueue) Len() int { return len(p) }
func (p prioQueue) Less(i, j int) bool {
	return lessKey(p[i].k, p[j].k)
}
func (p prioQueue) Swap(i, j int)       { p[i], p[j] = p[j], p[i] }
func (p *prioQueue) Push(x interface{}) { *p = append(*p, x.(prioItem)) }
func (p *prioQueue) Pop() interface{} {
	old := *p
	n := len(old)
	it := old[n-1]
	*p = old[:n-1]
	return it
}

// Repair computes the minimum-cost edit script transforming input into a
// string accepted by d. Ties are broken by shortest output, then by
// lexicographic order of the output.
//
// The search runs Dijkstra's algorithm over the product graph
// (automaton state x input position) with the monotone key
// (cost, output length, output string): every edge strictly increases the
// key (costs are positive and a zero-cost match still extends the output),
// and appending the same operation preserves the key order, so the first
// accepting node popped is optimal.
func Repair(d *DFA, input string, c Costs) (Result, error) {
	if err := d.Validate(); err != nil {
		return Result{}, err
	}
	if !c.valid() {
		return Result{}, fmt.Errorf("repair: costs must be positive integers, got %+v", c)
	}

	symIdx := make(map[byte]int, len(d.Alphabet))
	for i, a := range d.Alphabet {
		symIdx[a] = i
	}

	n := len(input)
	nodeID := func(q, i int) int { return q*(n+1) + i }

	dist := map[int]key{}
	prevNode := map[int]int{}
	prevOp := map[int]Op{}

	start := nodeID(d.Start, 0)
	dist[start] = key{cost: 0, out: ""}
	pq := &prioQueue{{node: start, k: dist[start]}}

	goal := -1
	var goalKey key

	for pq.Len() > 0 {
		it := heap.Pop(pq).(prioItem)
		cur := it.node
		if dist[cur] != it.k {
			continue // stale entry
		}
		q := cur / (n + 1)
		i := cur % (n + 1)
		if i == n && d.Accept[q] {
			goal, goalKey = cur, it.k
			break
		}

		type edge struct {
			to   int
			op   Op
			cost int
			emit byte
			has  bool // whether emit is emitted
		}
		var edges []edge

		// Insertions are always available.
		for si, a := range d.Alphabet {
			edges = append(edges, edge{
				to:   nodeID(d.Delta[q][si], i),
				op:   Op{Kind: OpInsert, Out: a},
				cost: c.Insert,
				emit: a,
				has:  true,
			})
		}
		if i < n {
			ch := input[i]
			// Deletion of the current input byte.
			edges = append(edges, edge{
				to:   nodeID(q, i+1),
				op:   Op{Kind: OpDelete, In: ch},
				cost: c.Delete,
			})
			if si, ok := symIdx[ch]; ok {
				// Zero-cost match.
				edges = append(edges, edge{
					to:   nodeID(d.Delta[q][si], i+1),
					op:   Op{Kind: OpMatch, In: ch, Out: ch},
					cost: 0,
					emit: ch,
					has:  true,
				})
			}
			// Substitutions by any other alphabet symbol.
			for si, a := range d.Alphabet {
				if a == ch {
					continue
				}
				edges = append(edges, edge{
					to:   nodeID(d.Delta[q][si], i+1),
					op:   Op{Kind: OpSubstitute, In: ch, Out: a},
					cost: c.Substitute,
					emit: a,
					has:  true,
				})
			}
		}

		for _, e := range edges {
			nk := key{cost: it.k.cost + e.cost, out: it.k.out}
			if e.has {
				nk.out += string(e.emit)
			}
			if old, ok := dist[e.to]; !ok || lessKey(nk, old) {
				dist[e.to] = nk
				prevNode[e.to] = cur
				prevOp[e.to] = e.op
				heap.Push(pq, prioItem{node: e.to, k: nk})
			}
		}
	}

	if goal < 0 {
		// Unreachable in practice: insertions make every DFA state reachable.
		return Result{}, errors.New("repair: no accepting string reachable")
	}

	// Reconstruct the script.
	var rev []Op
	for cur := goal; cur != start; cur = prevNode[cur] {
		rev = append(rev, prevOp[cur])
	}
	script := make([]Op, len(rev))
	for i, op := range rev {
		script[len(rev)-1-i] = op
	}

	return Result{Cost: goalKey.cost, Output: goalKey.out, Script: script}, nil
}
