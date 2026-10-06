package batsim

import "math/big"

// Reading is a sensor measurement of stored energy, tagged with the
// state index it refers to (state k is the stored energy after k
// executed steps).
type Reading struct {
	Step   int
	Stored *big.Rat
}

// Estimator maintains an estimated state trajectory that is strictly
// separated from the executed energy accounts. It anchors on sensor
// readings and forward-simulates the executed records with the battery
// model. Late sensor revisions only recompute the estimate and report
// the resulting delta; executed history is never modified.
//
// Every revision creates a new version. Versions are replayable:
// EstimateAtVersion recomputes a past version deterministically from
// the revision prefix that produced it.
type Estimator struct {
	spec      Spec
	src       RecordSource
	revisions []Reading
	versions  []map[int]*big.Rat
}

// NewEstimator creates an Estimator anchored on an initial reading
// (version 0).
func NewEstimator(spec Spec, src RecordSource, initial Reading) *Estimator {
	e := &Estimator{spec: spec, src: src}
	e.revisions = append(e.revisions, Reading{Step: initial.Step, Stored: clone(initial.Stored)})
	e.versions = append(e.versions, e.compute(e.revisions))
	return e
}

// estimateNext advances the estimated state across one executed record
// using the battery model: self-discharge, then the executed grid-side
// energies converted through the efficiencies.
func estimateNext(spec Spec, stored *big.Rat, rec StepRecord) *big.Rat {
	one := Rat(1, 1)
	keep := new(big.Rat).Sub(one, spec.SelfDischarge)
	cur := new(big.Rat).Mul(stored, keep)
	cur.Add(cur, new(big.Rat).Mul(rec.GridIn, spec.ChargeEff))
	drawn := new(big.Rat).Quo(rec.GridOut, spec.DischargeEff)
	cur.Sub(cur, drawn)
	return cur
}

// compute builds the estimated trajectory from a revision list. Later
// revisions for the same step override earlier ones. Estimates exist
// for state indices 0..len(records) that have an anchor at or before
// them.
func (e *Estimator) compute(revs []Reading) map[int]*big.Rat {
	readings := make(map[int]*big.Rat)
	for _, r := range revs {
		readings[r.Step] = clone(r.Stored)
	}
	recs := e.src.Records()
	est := make(map[int]*big.Rat)
	for t := 0; t <= len(recs); t++ {
		anchor := -1
		for s := range readings {
			if s <= t && s > anchor {
				anchor = s
			}
		}
		if anchor < 0 {
			continue
		}
		cur := clone(readings[anchor])
		for k := anchor; k < t; k++ {
			cur = estimateNext(e.spec, cur, recs[k])
		}
		est[t] = cur
	}
	return est
}

// Revise applies a late sensor revision. It recomputes the estimated
// trajectory from the revised step onward, stores a new version, and
// returns the delta (new minus previous estimate) at the latest
// executed state. The executed energy history is untouched.
func (e *Estimator) Revise(r Reading) (delta *big.Rat, ok bool) {
	e.revisions = append(e.revisions, Reading{Step: r.Step, Stored: clone(r.Stored)})
	next := e.compute(e.revisions)
	latest := len(e.src.Records())
	prev := e.versions[len(e.versions)-1]
	e.versions = append(e.versions, next)
	p, okp := prev[latest]
	n, okn := next[latest]
	if okp && okn {
		return new(big.Rat).Sub(n, p), true
	}
	return nil, false
}

// Estimate returns the current estimated stored energy at a state index.
func (e *Estimator) Estimate(step int) (*big.Rat, bool) {
	v, ok := e.versions[len(e.versions)-1][step]
	if !ok {
		return nil, false
	}
	return clone(v), true
}

// EstimateAtVersion deterministically replays the estimate of a past
// version from the revision prefix that produced it.
func (e *Estimator) EstimateAtVersion(version, step int) (*big.Rat, bool) {
	if version < 0 || version >= len(e.versions) {
		return nil, false
	}
	est := e.compute(e.revisions[:version+1])
	v, ok := est[step]
	if !ok {
		return nil, false
	}
	return clone(v), true
}

// VersionCount returns the number of estimate versions (initial plus
// one per revision).
func (e *Estimator) VersionCount() int { return len(e.versions) }
