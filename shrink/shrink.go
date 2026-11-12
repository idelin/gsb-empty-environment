package shrink

import "errors"

// Reproduce replays a candidate against the library and reports whether it
// still triggers the original failing assertion.
type Reproduce func(*Candidate) bool

// Request configures a structured minimization run.
type Request struct {
	Schema       *Schema
	Precondition Precondition
	Reproduce    Reproduce
	// Budget bounds the number of precondition-satisfying candidates replayed
	// through Reproduce. It must be positive.
	Budget int
}

// Result reports the outcome of a minimization run.
type Result struct {
	// Best is the smallest admissible candidate that still fails, or nil.
	Best *Candidate
	// Trials counts candidates passed to Reproduce.
	Trials int
	// Rejected counts candidates discarded by the precondition.
	Rejected int
	// Exhausted reports that the whole domain was enumerated.
	Exhausted bool
	// Incomplete reports that the budget expired before the domain was
	// exhausted. When true and Best is nil, the global minimum is unproven.
	Incomplete bool
}

// Shrink drives ordered enumeration until a failing candidate is found, the
// domain is exhausted, or the budget expires. Because candidates arrive in
// global minimization order, the first failing candidate is the global
// minimum whenever enumeration remains ordered up to that point.
func Shrink(req Request) (*Result, error) {
	if req.Reproduce == nil {
		return nil, errors.New("shrink: reproduce callback is nil")
	}
	if req.Budget <= 0 {
		return nil, errors.New("shrink: budget must be positive")
	}

	enum, err := NewEnumerator(req.Schema, req.Precondition)
	if err != nil {
		return nil, err
	}

	result := &Result{}
	for result.Trials < req.Budget {
		cand, ok := enum.Next()
		if !ok {
			result.Exhausted = true
			break
		}
		result.Trials++
		if req.Reproduce(cand) {
			result.Best = cand
			// The domain is fully proven only if nothing remains after the
			// global-minimum candidate; otherwise the remaining budget was
			// simply not needed.
			result.Exhausted = enum.Exhausted()
			break
		}
	}
	result.Rejected = enum.Rejected
	result.Incomplete = result.Best == nil && !result.Exhausted
	return result, nil
}
