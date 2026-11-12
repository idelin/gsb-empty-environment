package netsec

import "fmt"

// Mismatch describes one concrete input on which two graphs disagree, as
// observed by the independent interpreter.
type Mismatch struct {
	Inputs []byte
	Old    Outcome
	New    Outcome
}

// EnumerateAll runs the independent interpreter over every input combination
// for small graphs. It is intended for 8-bit exhaustive validation where the
// full input space (at most a few 8-bit input bytes) is tractable.
func EnumerateAll(oldGraph, newGraph *Graph, limit int) ([]Mismatch, error) {
	if err := oldGraph.Validate(); err != nil {
		return nil, fmt.Errorf("netsec: old graph invalid: %w", err)
	}
	if err := newGraph.Validate(); err != nil {
		return nil, fmt.Errorf("netsec: new graph invalid: %w", err)
	}
	if oldGraph.Inputs != newGraph.Inputs {
		return nil, fmt.Errorf("netsec: input count differs: %d vs %d",
			oldGraph.Inputs, newGraph.Inputs)
	}
	n := oldGraph.Inputs
	total := 1
	for i := 0; i < n; i++ {
		total *= 256
	}

	var mismatches []Mismatch
	inputs := make([]byte, n)
	for count := 0; count < total; count++ {
		v := count
		for i := 0; i < n; i++ {
			inputs[i] = byte(v)
			v >>= 8
		}
		oo, err := Interpret(oldGraph, inputs)
		if err != nil {
			return nil, err
		}
		no, err := Interpret(newGraph, inputs)
		if err != nil {
			return nil, err
		}
		if !outcomesEqual(oo, no) {
			mismatches = append(mismatches, Mismatch{
				Inputs: append([]byte(nil), inputs...),
				Old:    oo,
				New:    no,
			})
			if limit > 0 && len(mismatches) >= limit {
				return mismatches, nil
			}
		}
	}
	return mismatches, nil
}

func outcomesEqual(a, b Outcome) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case OutcomeError:
		return a.ErrNode == b.ErrNode
	case OutcomeReturn:
		return a.Width == b.Width && a.Value == b.Value
	default:
		return false
	}
}
