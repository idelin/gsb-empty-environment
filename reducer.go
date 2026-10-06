// Package opreducer reduces a sequence of integer operations to a
// 1-minimal failing subsequence under a caller-supplied oracle.
//
// Operation encoding: a positive value v creates resource v, a negative
// value -v references resource v, and 0 is a neutral no-op. A sequence is
// valid only if every reference is preceded by a creation of the same
// resource ("creation before reference"). The reducer never emits a
// candidate that violates this constraint.
package opreducer

import "errors"

// Verdict is the outcome of evaluating a candidate sequence.
type Verdict int

const (
	// Pass means the candidate does not reproduce the failure.
	Pass Verdict = iota
	// Fail means the candidate reproduces the failure.
	Fail
	// Unknown means the oracle could not decide (e.g. flaky or timed out).
	// Unknown is never treated as Fail: such candidates are rejected as
	// reductions and recorded in Stats.
	Unknown
)

// Oracle evaluates a candidate operation sequence.
type Oracle func(ops []int) Verdict

// Stats records oracle usage during a reduction.
type Stats struct {
	Calls        int     // total oracle invocations
	Fails        int     // invocations that returned Fail
	Passes       int     // invocations that returned Pass
	Unknowns     int     // invocations that returned Unknown
	UnknownCases [][]int // recorded candidates that returned Unknown
}

// ErrInvalidInput is returned when the initial sequence violates the
// creation-before-reference constraint.
var ErrInvalidInput = errors.New("opreducer: initial sequence violates creation-before-reference constraint")

// Valid reports whether every reference (-v) is preceded by a creation
// (+v) of the same resource.
func Valid(ops []int) bool {
	created := make(map[int]bool, len(ops))
	for _, op := range ops {
		switch {
		case op > 0:
			created[op] = true
		case op < 0:
			if !created[-op] {
				return false
			}
		}
	}
	return true
}

// Reduce shrinks ops to a 1-minimal failing subsequence: every single
// element whose removal keeps the sequence valid is required for the
// failure, i.e. deleting any one of them makes the oracle stop returning
// Fail (Pass or Unknown).
//
// Only candidates the oracle judges Fail are accepted. Unknown verdicts
// are recorded in Stats and the candidate is discarded. If the initial
// sequence does not fail, it is returned unchanged.
func Reduce(ops []int, oracle Oracle) ([]int, Stats, error) {
	var st Stats
	if !Valid(ops) {
		return nil, st, ErrInvalidInput
	}
	current := append([]int(nil), ops...)
	if st.judge(oracle, current) != Fail {
		return current, st, nil
	}

	// Greedy chunk removal: try deleting contiguous blocks from large to
	// small. Ending with an unproductive size-1 sweep guarantees
	// 1-minimality.
	for size := len(current) / 2; size >= 1; {
		progressed := false
		for start := 0; start+size <= len(current); start++ {
			cand := make([]int, 0, len(current)-size)
			cand = append(cand, current[:start]...)
			cand = append(cand, current[start+size:]...)
			if !Valid(cand) {
				continue
			}
			switch st.judge(oracle, cand) {
			case Fail:
				current = cand
				progressed = true
			}
			if progressed {
				break
			}
		}
		if !progressed {
			size--
		}
	}
	return current, st, nil
}

func (st *Stats) judge(oracle Oracle, ops []int) Verdict {
	st.Calls++
	switch v := oracle(ops); v {
	case Fail:
		st.Fails++
		return Fail
	case Unknown:
		st.Unknowns++
		st.UnknownCases = append(st.UnknownCases, append([]int(nil), ops...))
		return Unknown
	default:
		st.Passes++
		return Pass
	}
}
