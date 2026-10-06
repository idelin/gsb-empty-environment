// Package expsim implements a sequential experiment simulator.
//
// Observations are explicit integers with pre-given upper/lower
// boundaries. Statistics accumulate step by step and the experiment
// stops at the first boundary touch; data arriving afterwards must
// not change the conclusion. Observations sharing the same time are
// processed in sequence-number order.
package expsim

import "fmt"

// Decision is the frozen conclusion of a stopped experiment.
type Decision int

const (
	DecisionNone  Decision = iota // still running
	DecisionUpper                 // cumulative sum touched the upper boundary
	DecisionLower                 // cumulative sum touched the lower boundary
)

func (d Decision) String() string {
	switch d {
	case DecisionUpper:
		return "upper"
	case DecisionLower:
		return "lower"
	default:
		return "none"
	}
}

// Observation is one explicit integer measurement. ID uniquely
// identifies the fact; duplicates of the same ID are repeated events
// and must not be counted twice. Observations with equal Time are
// ordered by Seq.
type Observation struct {
	ID    string
	Time  int64
	Seq   int
	Value int64
}

// Boundaries holds the pre-given upper/lower limits, indexed by step
// (step n uses index n-1, 1-based steps). Steps beyond the slice
// length have no boundary and cannot stop the experiment.
type Boundaries struct {
	Upper []int64
	Lower []int64
}

func (b Boundaries) steps() int {
	if len(b.Upper) < len(b.Lower) {
		return len(b.Upper)
	}
	return len(b.Lower)
}

// touches reports whether the cumulative sum at the given 1-based
// step meets either boundary, and which decision it implies. The
// upper boundary is checked first.
func (b Boundaries) touches(step int, sum int64) (Decision, bool) {
	if step < 1 || step > b.steps() {
		return DecisionNone, false
	}
	if sum >= b.Upper[step-1] {
		return DecisionUpper, true
	}
	if sum <= b.Lower[step-1] {
		return DecisionLower, true
	}
	return DecisionNone, false
}

// StopEvidence records the exact proof of the first boundary touch.
type StopEvidence struct {
	Step     int      // 1-based step at which the boundary was touched
	Sum      int64    // cumulative sum at that step
	Upper    int64    // upper boundary in force at that step
	Lower    int64    // lower boundary in force at that step
	Decision Decision // which boundary was touched
	ObsID    string   // observation that caused the touch
	ObsTime  int64
	ObsSeq   int
	ObsValue int64
}

func (e StopEvidence) String() string {
	return fmt.Sprintf("step=%d sum=%d bounds=[%d,%d] decision=%s obs=%s(t=%d,seq=%d,v=%d)",
		e.Step, e.Sum, e.Lower, e.Upper, e.Decision, e.ObsID, e.ObsTime, e.ObsSeq, e.ObsValue)
}

// StepRecord is the per-step audit entry produced while processing.
type StepRecord struct {
	Step    int
	Sum     int64
	Obs     Observation
	Stopped bool // true if this record caused the stop
}
