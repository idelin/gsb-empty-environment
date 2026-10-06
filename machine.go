// Package transducer checks bounded equivalence of finite transducers
// (Mealy-style machines whose transitions emit possibly-empty strings).
//
// Two machines are equivalent with respect to a public finite input-length
// bound L iff every complete input string of length exactly L makes both
// machines emit the same concatenated output. Per-edge output differences
// do not imply inequivalence: a machine may defer output (empty edge
// outputs) and catch up on a later edge of the same input string. Only the
// totals at the end of a complete run are compared.
//
// A missing transition is treated as an error output: the machine emits a
// distinguished error marker and halts, so runs that hit missing
// transitions compare unequal to runs that do not (and equal to runs that
// hit a missing transition after producing the same output).
package transducer

import "strings"

// Halted is the implicit state a machine enters after hitting a missing
// transition. Machines must not use negative state ids.
const Halted = -1

// Edge is a single transition: the target state and the output string
// emitted when the edge is taken. The output may be empty.
type Edge struct {
	To     int
	Output string
}

// Machine is a finite transducer. Transitions maps a state and an input
// symbol to an Edge; a missing entry models an undefined transition.
type Machine struct {
	Initial     int
	Transitions map[int]map[rune]Edge
}

// RunResult is the outcome of executing a complete input string.
type RunResult struct {
	// Output is the concatenation of all edge outputs produced before
	// the input was exhausted or a missing transition was hit.
	Output string
	// Error reports whether a missing transition was hit. Once it is
	// hit the machine halts and remaining input produces no output.
	Error bool
}

// Run executes the complete input string and returns the concatenated
// output together with the error flag.
func (m *Machine) Run(input []rune) RunResult {
	state := m.Initial
	var b strings.Builder
	for _, sym := range input {
		edge, ok := m.Transitions[state][sym]
		if !ok {
			return RunResult{Output: b.String(), Error: true}
		}
		b.WriteString(edge.Output)
		state = edge.To
	}
	return RunResult{Output: b.String()}
}

// step advances one symbol. The halted state is absorbing and silent; a
// missing transition halts the machine (the error output).
func step(m *Machine, state int, sym rune) (int, string) {
	if state == Halted {
		return Halted, ""
	}
	edge, ok := m.Transitions[state][sym]
	if !ok {
		return Halted, ""
	}
	return edge.To, edge.Output
}
