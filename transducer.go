// Package tdeq checks bounded equivalence of finite transducers.
//
// A transducer is a Mealy-like finite state machine whose transitions may
// produce arbitrary strings, including the empty string. Equivalence of two
// transducers is therefore defined over the concatenation of outputs along
// a complete input string, not over individual transition outputs: two
// machines may emit different pieces per edge and still produce identical
// overall output. A missing transition is treated as an error output,
// distinct from every regular output.
//
// Because outputs can be empty, per-edge output differences must not be
// judged immediately; only the cumulative outputs of complete input strings
// are compared. The input length bound is public and finite: Check compares
// exactly the input strings of that length. Counterexamples are verified by
// complete execution of both machines before they are reported. Exploration
// runs on a budget; when the budget is exhausted the result is Unknown and
// the search frontier can be resumed later or snapshotted for recovery.
package tdeq

import (
	"fmt"
	"sort"
)

// Edge is a single transition: on reading an input symbol the machine moves
// to state Next and appends Output (possibly empty) to its output tape.
type Edge struct {
	Next   int
	Output string
}

// Transducer is a finite state transducer. Edges[from][sym] describes the
// transition taken on input symbol sym in state from; a missing map entry
// means the transition is undefined and yields an error output.
type Transducer struct {
	NumStates int
	Initial   int
	Edges     []map[rune]Edge
}

// New creates a transducer with numStates states and the given initial
// state and no transitions.
func New(numStates, initial int) (*Transducer, error) {
	if numStates <= 0 {
		return nil, fmt.Errorf("tdeq: numStates must be positive, got %d", numStates)
	}
	if initial < 0 || initial >= numStates {
		return nil, fmt.Errorf("tdeq: initial state %d out of range [0, %d)", initial, numStates)
	}
	return &Transducer{
		NumStates: numStates,
		Initial:   initial,
		Edges:     make([]map[rune]Edge, numStates),
	}, nil
}

// AddEdge installs a transition from state `from` on symbol sym to state
// `to` producing output. An existing transition on (from, sym) is replaced.
func (t *Transducer) AddEdge(from int, sym rune, to int, output string) error {
	if from < 0 || from >= t.NumStates {
		return fmt.Errorf("tdeq: source state %d out of range [0, %d)", from, t.NumStates)
	}
	if to < 0 || to >= t.NumStates {
		return fmt.Errorf("tdeq: target state %d out of range [0, %d)", to, t.NumStates)
	}
	if t.Edges[from] == nil {
		t.Edges[from] = make(map[rune]Edge)
	}
	t.Edges[from][sym] = Edge{Next: to, Output: output}
	return nil
}

// Alphabet returns the sorted set of input symbols on which the transducer
// has at least one defined transition.
func (t *Transducer) Alphabet() []rune {
	seen := make(map[rune]bool)
	for _, edges := range t.Edges {
		for sym := range edges {
			seen[sym] = true
		}
	}
	alpha := make([]rune, 0, len(seen))
	for sym := range seen {
		alpha = append(alpha, sym)
	}
	sort.Slice(alpha, func(i, j int) bool { return alpha[i] < alpha[j] })
	return alpha
}

// Run executes the transducer on the complete input string and returns the
// concatenated output. ok is false when a transition is missing along the
// way; the output is then an error output and the returned string is
// meaningless.
func (t *Transducer) Run(input string) (output string, ok bool) {
	state := t.Initial
	var buf []byte
	for _, sym := range input {
		edge, defined := t.Edges[state][sym]
		if !defined {
			return "", false
		}
		buf = append(buf, edge.Output...)
		state = edge.Next
	}
	return string(buf), true
}
