package netsec

import (
	"fmt"
	"math/big"
)

// NextMultiplier returns a valid multiplier s greater than last for which at
// least one current candidate can produce a yes answer.  It uses exact
// quotient ranges; it never enumerates plaintext integers.
//
// The scan is over attainable quotients q, not over candidates.  For a fixed
// interval [a,b] and quotient q, admissible multipliers satisfy
//
//	ceil((qN+2B)/b) <= s <= floor((qN+3B-1)/a).
//
// The first exact integer intersection in a quotient window is returned; the
// minimum across intervals is used.
func NextMultiplier(intervals []Interval, last, n, b *big.Int) (*big.Int, error) {
	if n == nil || b == nil || n.Sign() <= 0 || b.Sign() <= 0 {
		return nil, fmt.Errorf("%w: modulus and B must be positive", ErrInvalidInterval)
	}
	current, err := MergeIntervals(intervals)
	if err != nil {
		return nil, err
	}
	if len(current) == 0 {
		return nil, fmt.Errorf("%w: empty candidate set", ErrNoMultiplier)
	}
	if last == nil || last.Sign() < 0 {
		last = new(big.Int)
	}
	nextAfterLast := new(big.Int).Add(last, bigOne)

	var best *big.Int
	for _, iv := range current {
		candidate, err := nextForInterval(iv, nextAfterLast, n, b)
		if err != nil {
			continue
		}
		if candidate == nil {
			continue
		}
		if best == nil || candidate.Cmp(best) < 0 {
			best = candidate
		}
	}
	if best == nil {
		return nil, ErrNoMultiplier
	}
	return best, nil
}

func nextForInterval(iv Interval, minS, n, b *big.Int) (*big.Int, error) {
	threeBMinusOne := new(big.Int).Sub(new(big.Int).Mul(new(big.Int).Set(b), bigThree), bigOne)

	// qStart is the first quotient whose 3B window can extend past minS-1.
	numerator := new(big.Int).Sub(new(big.Int).Mul(iv.Lo, minS), threeBMinusOne)
	qStart := new(big.Int)
	if numerator.Sign() > 0 {
		qStart = ceilDiv(numerator, n)
	}

	twoB := new(big.Int).Lsh(new(big.Int).Set(b), 1)
	q := new(big.Int).Set(qStart)
	for {
		qn := new(big.Int).Mul(q, n)
		lower := ceilDiv(new(big.Int).Add(qn, twoB), iv.Hi)
		upper := new(big.Int).Quo(new(big.Int).Add(qn, threeBMinusOne), iv.Lo)

		candidate := new(big.Int).Set(lower)
		if candidate.Cmp(minS) < 0 {
			candidate = new(big.Int).Set(minS)
		}
		if candidate.Cmp(upper) <= 0 && candidate.Cmp(n) < 0 {
			// The quotient overlap can be narrower than one integer.  Check the
			// exact integer intersection before accepting s.
			pieces, err := yesPiecesForInterval(iv, candidate, n, b)
			if err != nil {
				return nil, err
			}
			if len(pieces) > 0 {
				return candidate, nil
			}
		}

		if lower.Cmp(n) >= 0 {
			return nil, nil
		}
		q = new(big.Int).Add(q, bigOne)
	}
}
