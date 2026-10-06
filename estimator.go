package batsim

import "math/big"

// SensorReading is one versioned observation of a battery's stored
// energy. Late-arriving revisions carry a higher Version and supersede
// earlier readings for the same battery during replay.
type SensorReading struct {
	Version int
	Battery int
	Stored  *big.Rat
}

// Estimator maintains an estimated state derived solely from the
// versioned sensor log. It is strictly separated from execution state:
// revising a reading recomputes the estimate and reports the delta, but
// never touches batteries, energy books, or the fleet's ActionLog.
type Estimator struct {
	log     []SensorReading
	current map[int]*big.Rat
}

// NewEstimator returns an empty Estimator.
func NewEstimator() *Estimator {
	return &Estimator{current: make(map[int]*big.Rat)}
}

// Revise appends a reading (a new version) to the log, replays the full
// log to recompute the estimate, and returns the per-battery delta
// against the previous estimate. Batteries absent from the delta were
// unaffected.
func (e *Estimator) Revise(r SensorReading) map[int]*big.Rat {
	e.log = append(e.log, r)
	next := e.replay()
	delta := make(map[int]*big.Rat)
	keys := make(map[int]bool)
	for k := range next {
		keys[k] = true
	}
	for k := range e.current {
		keys[k] = true
	}
	for k := range keys {
		d := new(big.Rat).Sub(ratOrZero(next[k]), ratOrZero(e.current[k]))
		if d.Sign() != 0 {
			delta[k] = d
		}
	}
	e.current = next
	return delta
}

// replay recomputes the estimate from scratch by folding the version
// log in append order; the latest version per battery wins.
func (e *Estimator) replay() map[int]*big.Rat {
	est := make(map[int]*big.Rat)
	for _, r := range e.log {
		est[r.Battery] = copyRat(r.Stored)
	}
	return est
}

// Estimate returns a copy of the current estimated state.
func (e *Estimator) Estimate() map[int]*big.Rat {
	out := make(map[int]*big.Rat, len(e.current))
	for k, v := range e.current {
		out[k] = copyRat(v)
	}
	return out
}

// LogLen returns the number of recorded sensor versions.
func (e *Estimator) LogLen() int { return len(e.log) }
