package editfa

import (
	"container/heap"
	"errors"
	"fmt"
)

// ErrNoSolution is returned when no accepted string is reachable from
// the DFA's start state, so no edit script can make the input accepted.
var ErrNoSolution = errors.New("editfa: no accepted string is reachable")

// Costs holds the positive integer costs of the edit operations.
type Costs struct {
	Insert  int
	Delete  int
	Replace int
}

// Op identifies the kind of an edit operation.
type Op int

const (
	// Keep copies the input byte at Pos to the output (cost 0).
	Keep Op = iota
	// Insert inserts New before input position Pos.
	Insert
	// Delete removes the input byte at Pos.
	Delete
	// Replace substitutes the input byte Old at Pos with New.
	Replace
)

func (o Op) String() string {
	switch o {
	case Keep:
		return "keep"
	case Insert:
		return "insert"
	case Delete:
		return "delete"
	case Replace:
		return "replace"
	}
	return "unknown"
}

// Edit is a single operation of an edit script. Pos always refers to an
// index in the original input string; for Insert it is the index before
// which New is inserted. Old is the consumed input byte for Keep,
// Delete and Replace.
type Edit struct {
	Op       Op
	Pos      int
	Old, New byte
}

func (e Edit) String() string {
	switch e.Op {
	case Keep:
		return fmt.Sprintf("keep %q @%d", e.Old, e.Pos)
	case Insert:
		return fmt.Sprintf("insert %q @%d", e.New, e.Pos)
	case Delete:
		return fmt.Sprintf("delete %q @%d", e.Old, e.Pos)
	case Replace:
		return fmt.Sprintf("replace %q -> %q @%d", e.Old, e.New, e.Pos)
	}
	return "unknown"
}

// Result is the outcome of MinEditScript.
type Result struct {
	Cost   int    // total cost of Script
	Output string // the accepted string produced by Script
	Script []Edit // operations transforming the input into Output
}

// label is the priority of a partial solution: first by cost, then by
// output length, then lexicographically by output.
type label struct {
	cost int
	str  string
}

func lessLabel(a, b label) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if len(a.str) != len(b.str) {
		return len(a.str) < len(b.str)
	}
	return a.str < b.str
}

type pqItem struct {
	idx int
	lbl label
}

type priorityQueue []pqItem

func (p priorityQueue) Len() int { return len(p) }
func (p priorityQueue) Less(i, j int) bool {
	return lessLabel(p[i].lbl, p[j].lbl)
}
func (p priorityQueue) Swap(i, j int)       { p[i], p[j] = p[j], p[i] }
func (p *priorityQueue) Push(x interface{}) { *p = append(*p, x.(pqItem)) }
func (p *priorityQueue) Pop() interface{} {
	old := *p
	n := len(old)
	it := old[n-1]
	*p = old[:n-1]
	return it
}

// MinEditScript finds the minimum-cost edit script that transforms
// input into a string accepted by d. Ties are broken by preferring the
// shortest output string, then the lexicographically smallest one.
//
// It returns ErrNoSolution if no accepted string is reachable from the
// start state, and an error if the arguments are invalid.
func MinEditScript(d *DFA, input string, c Costs) (*Result, error) {
	if d == nil || d.NumStates() == 0 {
		return nil, errors.New("editfa: DFA has no states")
	}
	if d.Start < 0 || d.Start >= d.NumStates() {
		return nil, errors.New("editfa: start state out of range")
	}
	if c.Insert <= 0 || c.Delete <= 0 || c.Replace <= 0 {
		return nil, errors.New("editfa: costs must be positive integers")
	}

	n := len(input)
	ns := d.NumStates()
	alphabet := d.Alphabet()
	index := func(i, q int) int { return i*ns + q }

	type cell struct {
		lbl label
		set bool
	}
	dist := make([]cell, (n+1)*ns)
	settled := make([]bool, (n+1)*ns)
	parent := make([]int, (n+1)*ns)
	parentEdit := make([]Edit, (n+1)*ns)

	start := index(0, d.Start)
	dist[start] = cell{set: true}
	parent[start] = -1
	pq := &priorityQueue{{idx: start}}
	heap.Init(pq)

	target := -1
	for pq.Len() > 0 {
		it := heap.Pop(pq).(pqItem)
		u := it.idx
		if settled[u] || it.lbl != dist[u].lbl {
			continue
		}
		settled[u] = true
		i, q := u/ns, u%ns
		if i == n && d.acceptsState(q) {
			target = u
			break
		}
		relax := func(v int, nl label, e Edit) {
			if settled[v] {
				return
			}
			if !dist[v].set || lessLabel(nl, dist[v].lbl) {
				dist[v] = cell{lbl: nl, set: true}
				parent[v] = u
				parentEdit[v] = e
				heap.Push(pq, pqItem{idx: v, lbl: nl})
			}
		}
		if i < n {
			cur := input[i]
			if nq, ok := d.step(q, cur); ok {
				relax(index(i+1, nq),
					label{it.lbl.cost, it.lbl.str + string(cur)},
					Edit{Op: Keep, Pos: i, Old: cur, New: cur})
			}
			relax(index(i+1, q),
				label{it.lbl.cost + c.Delete, it.lbl.str},
				Edit{Op: Delete, Pos: i, Old: cur})
			for _, a := range alphabet {
				if a == cur {
					continue
				}
				if nq, ok := d.step(q, a); ok {
					relax(index(i+1, nq),
						label{it.lbl.cost + c.Replace, it.lbl.str + string(a)},
						Edit{Op: Replace, Pos: i, Old: cur, New: a})
				}
			}
		}
		for _, a := range alphabet {
			if nq, ok := d.step(q, a); ok {
				relax(index(i, nq),
					label{it.lbl.cost + c.Insert, it.lbl.str + string(a)},
					Edit{Op: Insert, Pos: i, New: a})
			}
		}
	}

	if target < 0 {
		return nil, ErrNoSolution
	}
	var rev []Edit
	for u := target; parent[u] >= 0; u = parent[u] {
		rev = append(rev, parentEdit[u])
	}
	script := make([]Edit, len(rev))
	for k := range rev {
		script[len(rev)-1-k] = rev[k]
	}
	return &Result{
		Cost:   dist[target].lbl.cost,
		Output: dist[target].lbl.str,
		Script: script,
	}, nil
}

// Apply replays an edit script on input and returns the produced
// string. It reports an error if the script is inconsistent with the
// input or does not consume it entirely.
func Apply(input string, script []Edit) (string, error) {
	var out []byte
	src := 0
	for k, e := range script {
		switch e.Op {
		case Keep, Delete, Replace:
			if e.Pos != src || src >= len(input) || input[src] != e.Old {
				return "", fmt.Errorf("editfa: edit %d (%v) does not match input at position %d", k, e.Op, src)
			}
			if e.Op == Keep {
				out = append(out, e.Old)
			} else if e.Op == Replace {
				out = append(out, e.New)
			}
			src++
		case Insert:
			if e.Pos != src {
				return "", fmt.Errorf("editfa: edit %d (insert) at wrong position %d, expected %d", k, e.Pos, src)
			}
			out = append(out, e.New)
		default:
			return "", fmt.Errorf("editfa: edit %d has unknown op %d", k, e.Op)
		}
	}
	if src != len(input) {
		return "", fmt.Errorf("editfa: script consumed %d of %d input bytes", src, len(input))
	}
	return string(out), nil
}

// ScriptCost returns the total cost of an edit script.
func ScriptCost(script []Edit, c Costs) int {
	total := 0
	for _, e := range script {
		switch e.Op {
		case Insert:
			total += c.Insert
		case Delete:
			total += c.Delete
		case Replace:
			total += c.Replace
		}
	}
	return total
}
