// Package incjoin implements an incremental equi-join over two tables
// (bags) that allow duplicate rows. Multiplicity is tracked per join key;
// inserts and deletes are counted by multiplicity. A batch may modify both
// tables at once, and the cross delta (dA x dB) is accounted for exactly
// once so the maintained result always equals the full join of the
// current tables.
package incjoin

import "errors"

// Side identifies which table an operation targets.
type Side int

const (
	A Side = iota
	B
)

// Op is a single insert or delete of N copies of Key.
// N must be positive; zero or negative N is rejected.
type Op struct {
	Side   Side
	Key    string
	N      int
	Delete bool
}

// Batch is a set of operations applied atomically to both tables.
// Operations are validated and applied in order.
type Batch []Op

// Delta is the change of the join result: key -> signed multiplicity
// change. Only non-zero entries are present. Positive means the key
// gained result rows, negative means it lost result rows.
type Delta map[string]int

var (
	// ErrNonPositiveCount is returned when an Op has N <= 0.
	ErrNonPositiveCount = errors.New("incjoin: operation count must be positive")
	// ErrNegativeMultiplicity is returned when a delete would drive a
	// key's multiplicity below zero.
	ErrNegativeMultiplicity = errors.New("incjoin: delete would cause negative multiplicity")
)

// Engine maintains tables A and B and their join result incrementally.
// The zero value is ready to use.
type Engine struct {
	a      map[string]int
	b      map[string]int
	result map[string]int
}

// New returns an empty Engine.
func New() *Engine {
	return &Engine{
		a:      make(map[string]int),
		b:      make(map[string]int),
		result: make(map[string]int),
	}
}

// Multiplicity reports how many copies of key the given table holds.
func (e *Engine) Multiplicity(s Side, key string) int {
	if s == A {
		return e.a[key]
	}
	return e.b[key]
}

// Result returns a copy of the current join result: key -> multiplicity.
func (e *Engine) Result() map[string]int {
	out := make(map[string]int, len(e.result))
	for k, v := range e.result {
		out[k] = v
	}
	return out
}

// Apply validates and applies the batch atomically, returning the
// resulting join delta. If any operation is invalid, nothing is
// modified and an error is returned.
//
// The delta is computed as
//
//	dR = dA x B_old + A_old x dB + dA x dB
//
// where the last term is the cross delta between the two tables'
// changes within the same batch.
func (e *Engine) Apply(batch Batch) (Delta, error) {
	newA := make(map[string]int, len(e.a))
	for k, v := range e.a {
		newA[k] = v
	}
	newB := make(map[string]int, len(e.b))
	for k, v := range e.b {
		newB[k] = v
	}
	dA := make(map[string]int)
	dB := make(map[string]int)

	for _, op := range batch {
		if op.N <= 0 {
			return nil, ErrNonPositiveCount
		}
		counts, d := newA, dA
		if op.Side == B {
			counts, d = newB, dB
		}
		if op.Delete {
			if counts[op.Key] < op.N {
				return nil, ErrNegativeMultiplicity
			}
			counts[op.Key] -= op.N
			d[op.Key] -= op.N
		} else {
			counts[op.Key] += op.N
			d[op.Key] += op.N
		}
		if counts[op.Key] == 0 {
			delete(counts, op.Key)
		}
	}

	delta := make(Delta)
	keys := make(map[string]struct{}, len(dA)+len(dB))
	for k := range dA {
		keys[k] = struct{}{}
	}
	for k := range dB {
		keys[k] = struct{}{}
	}
	for k := range keys {
		// dA x B_old + A_old x dB + dA x dB
		dr := dA[k]*e.b[k] + e.a[k]*dB[k] + dA[k]*dB[k]
		if dr == 0 {
			continue
		}
		delta[k] = dr
		e.result[k] += dr
		if e.result[k] == 0 {
			delete(e.result, k)
		}
	}

	e.a, e.b = newA, newB
	return delta, nil
}

// FullJoin computes the join of two tables from scratch:
// key -> multA * multB. It serves as the reference implementation
// for verifying incremental results.
func FullJoin(a, b map[string]int) map[string]int {
	out := make(map[string]int)
	for k, na := range a {
		if nb, ok := b[k]; ok {
			out[k] = na * nb
		}
	}
	return out
}
