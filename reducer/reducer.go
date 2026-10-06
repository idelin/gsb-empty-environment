// Package reducer implements a constraint-aware delta debugger (ddmin)
// over sequences of integer operations.
//
// Operation encoding:
//   op > 0  creates id op
//   op < 0  references id -op
//   op == 0 is neutral
//
// A sequence is legal iff every reference is preceded by a creation of
// the same id ("creation before reference"). The reducer never accepts
// or even evaluates an illegal candidate.
//
// The failure predicate returns a Verdict. Only Fail candidates are
// accepted. Unknown verdicts are recorded (with a snapshot of the
// candidate) and are never treated as Fail, i.e. never accepted.
//
// The reduction terminates with a 1-minimal result: deleting any single
// operation whose deletion keeps the sequence legal no longer fails.
package reducer

// Verdict is the outcome of evaluating a candidate sequence.
type Verdict int

const (
	// Pass means the candidate does not reproduce the failure.
	Pass Verdict = iota
	// Fail means the candidate still reproduces the failure.
	Fail
	// Unknown means the predicate could not decide. Recorded, never
	// treated as Fail.
	Unknown
)

func (v Verdict) String() string {
	switch v {
	case Pass:
		return "pass"
	case Fail:
		return "fail"
	default:
		return "unknown"
	}
}

// Predicate evaluates a candidate sequence.
type Predicate func(seq []int) Verdict

// Stats records every predicate evaluation performed during a reduction.
type Stats struct {
	Calls          int     // total predicate evaluations
	Fails          int     // evaluations that returned Fail
	Passes         int     // evaluations that returned Pass
	Unknowns       int     // evaluations that returned Unknown
	SkippedIllegal int     // candidates rejected without evaluation (illegal)
	UnknownSeqs    [][]int // snapshots of candidates that returned Unknown
}

// Legal reports whether every reference in seq is preceded by a creation
// of the same id.
func Legal(seq []int) bool {
	created := make(map[int]bool)
	for _, op := range seq {
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

type reducer struct {
	pred  Predicate
	stats Stats
}

// test evaluates a candidate, recording the verdict. Unknown verdicts are
// recorded with a snapshot and are never reported as Fail to the caller.
func (r *reducer) test(seq []int) Verdict {
	r.stats.Calls++
	v := r.pred(seq)
	switch v {
	case Fail:
		r.stats.Fails++
	case Unknown:
		r.stats.Unknowns++
		r.stats.UnknownSeqs = append(r.stats.UnknownSeqs, append([]int(nil), seq...))
	default:
		v = Pass
		r.stats.Passes++
	}
	return v
}

// Reduce shrinks seq to a 1-minimal failing subsequence under pred.
//
// Only candidates that still fail are accepted. Candidates whose
// deletion of operations would violate the creation-before-reference
// constraint are skipped without evaluation. If seq itself does not
// fail, it is returned unchanged.
func Reduce(seq []int, pred Predicate) ([]int, Stats) {
	r := &reducer{pred: pred}
	cur := append([]int(nil), seq...)
	if r.test(cur) != Fail {
		return cur, r.stats
	}

	// Phase 1: ddmin-style chunk removal with legality filtering.
	n := 2
	for len(cur) >= 2 {
		chunk := (len(cur) + n - 1) / n
		reduced := false
		for i := 0; i < len(cur); i += chunk {
			end := i + chunk
			if end > len(cur) {
				end = len(cur)
			}
			cand := make([]int, 0, len(cur)-(end-i))
			cand = append(cand, cur[:i]...)
			cand = append(cand, cur[end:]...)
			if !Legal(cand) {
				r.stats.SkippedIllegal++
				continue
			}
			if r.test(cand) == Fail {
				cur = cand
				if n > 2 {
					n--
				}
				reduced = true
				break
			}
		}
		if reduced {
			continue
		}
		if n >= len(cur) {
			break
		}
		n *= 2
		if n > len(cur) {
			n = len(cur)
		}
	}

	// Phase 2: 1-minimality fixpoint. Guarantees that deleting any
	// single operation whose deletion is legal no longer fails.
	for {
		reduced := false
		for i := 0; i < len(cur); i++ {
			cand := make([]int, 0, len(cur)-1)
			cand = append(cand, cur[:i]...)
			cand = append(cand, cur[i+1:]...)
			if !Legal(cand) {
				r.stats.SkippedIllegal++
				continue
			}
			if r.test(cand) == Fail {
				cur = cand
				reduced = true
				break
			}
		}
		if !reduced {
			break
		}
	}
	return cur, r.stats
}
