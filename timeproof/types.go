// Package timeproof computes the set of possible real ("true") times at a
// query instant from signed server-time receipts, while tolerating up to a
// bounded number of faulty trust domains.
//
// Every quantity in the problem is a rational number. The package uses
// math/big.Rat throughout and never uses floating point arithmetic to reach a
// definite conclusion. The public result types carry self-contained,
// independently re-checkable evidence (feasible time assignments, tight
// difference-constraint paths, negative cycles and gap exclusions).
package timeproof

import "math/big"

// Rat is an exact rational number.
type Rat = big.Rat

// NewRat builds an exact rational numerator/denominator value. It panics if
// denominator is zero; callers should instead surface validation errors.
func NewRat(num, den int64) *Rat {
	if den == 0 {
		panic("timeproof: zero denominator")
	}
	return big.NewRat(num, den)
}

// RatInt builds a rational from an integer value.
func RatInt(v int64) *Rat { return big.NewRat(v, 1) }

// ratCopy returns an independent copy of r, treating nil as nil.
func ratCopy(r *Rat) *Rat {
	if r == nil {
		return nil
	}
	return new(Rat).Set(r)
}

// ratCmp compares a and b, treating nil as positive infinity (a nil bound
// means "no bound", never an actual value).
func ratCmp(a, b *Rat) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	default:
		return a.Cmp(b)
	}
}

// Receipt is one signed server-time response. All times are expressed in the
// host's local monotonic clock, except SignedLo/SignedHi which are server
// (true) times asserted by the signed payload. Every field is rational.
type Receipt struct {
	// Nonce binds the receipt to a request; it must equal Query.Nonce.
	Nonce string
	// ServerID identifies the signing server (its public identity).
	ServerID string
	// Domain is the trust domain; all receipts of one domain are atomic.
	Domain string
	// Upstream groups servers that share the same upstream clock. It is
	// informational metadata used for cross-validation; no extra
	// consistency constraints are implied beyond the shared graph nodes.
	Upstream string

	SendMono    *Rat // local monotonic time the request was sent
	RecvMono    *Rat // local monotonic time the response was received
	ProcessLo   *Rat // server processing-time lower bound
	ProcessHi   *Rat // server processing-time upper bound
	OneWayDelay *Rat // network one-way delay bound (both dirs); nil => unbounded
	SignedLo    *Rat // signed server-time interval, lower endpoint
	SignedHi    *Rat // signed server-time interval, upper endpoint
}

// DriftSeg is one segment of the piecewise local monotonic clock drift model.
// On [From, To) of the local monotonic axis, an elapsed monotonic duration dm
// corresponds to a true elapsed duration dt with RateLo <= dt/dm <= RateHi.
type DriftSeg struct {
	From   *Rat
	To     *Rat
	RateLo *Rat
	RateHi *Rat
}

// Clock is the host piecewise monotonic drift model.
type Clock struct {
	Segments []DriftSeg
}

// Query is a single time-proof query.
type Query struct {
	Nonce     string
	Clock     *Clock
	Receipts  []*Receipt
	QueryMono *Rat // local monotonic instant whose true time is requested
	// MaxFaulty is f: the maximum number of trust domains allowed to emit
	// arbitrary (Byzantine) evidence.
	MaxFaulty int
	// StateCap bounds the number of enumerated domain masks. Zero uses the
	// package default. When the cap is hit the result is marked Truncated
	// and no definite "must hold" verdict is claimed.
	StateCap int
}
