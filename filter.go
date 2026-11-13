package netsec

import (
	"fmt"
	"math/big"
)

var (
	bigOne   = big.NewInt(1)
	bigThree = big.NewInt(3)
)

// ceilDiv returns ceil(a/b) for b > 0.  a may be negative.
func ceilDiv(a, b *big.Int) *big.Int {
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(a, b, r)
	if r.Sign() != 0 && a.Sign() >= 0 {
		q.Add(q, bigOne)
	}
	return q
}

// FilterIntervals restricts a candidate set using one oracle answer for the
// multiplier s.  The returned intervals are sorted, disjoint, and adjacent
// intervals have been merged.
//
// For a yes answer, the result is the union over every attainable integer
// quotient q of
//
//	[ceil((qN+2B)/s), ceil((qN+3B)/s)-1]
//
// intersected with the current candidate intervals.  For a no answer the same
// union is removed.  FeedbackUnavailable returns the candidate set unchanged.
func FilterIntervals(intervals []Interval, s, n, b *big.Int, feedback Feedback) ([]Interval, error) {
	if s == nil || n == nil || b == nil || s.Sign() <= 0 || n.Sign() <= 0 || b.Sign() <= 0 {
		return nil, fmt.Errorf("%w: multiplier, modulus and B must be positive", ErrInvalidInterval)
	}
	if !feedback.valid() {
		return nil, ErrInvalidFeedback
	}
	current, err := MergeIntervals(intervals)
	if err != nil {
		return nil, err
	}
	if feedback == FeedbackUnavailable || len(current) == 0 {
		return current, nil
	}

	pieces := make([]Interval, 0)
	for _, iv := range current {
		yesPieces, err := yesPiecesForInterval(iv, s, n, b)
		if err != nil {
			return nil, err
		}
		pieces = append(pieces, yesPieces...)
	}
	pieces, err = MergeIntervals(pieces)
	if err != nil {
		return nil, err
	}
	if feedback == FeedbackYes {
		return pieces, nil
	}

	// FeedbackNo: remove every qualifying piece from the current candidate set.
	out := make([]Interval, 0, len(current))
	for _, iv := range current {
		out = append(out, subtractPieces(iv, pieces)...)
	}
	return MergeIntervals(out)
}

// UpdateIntervals and ApplyFeedback are aliases for FilterIntervals.
func UpdateIntervals(intervals []Interval, s, n, b *big.Int, feedback Feedback) ([]Interval, error) {
	return FilterIntervals(intervals, s, n, b, feedback)
}

func ApplyFeedback(intervals []Interval, s, n, b *big.Int, feedback Feedback) ([]Interval, error) {
	return FilterIntervals(intervals, s, n, b, feedback)
}

// FilterByFeedback is an alias for FilterIntervals.
func FilterByFeedback(intervals []Interval, s, n, b *big.Int, feedback Feedback) ([]Interval, error) {
	return FilterIntervals(intervals, s, n, b, feedback)
}

// IntersectFeedback is like FilterIntervals but reports ErrInconsistent when
// the resulting intersection is empty.  This is the form used by a session.
func IntersectFeedback(intervals []Interval, s, n, b *big.Int, feedback Feedback) ([]Interval, error) {
	out, err := FilterIntervals(intervals, s, n, b, feedback)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrInconsistent
	}
	return out, nil
}

// yesPiecesForInterval computes every subinterval of iv whose multiplied
// residue lies in [2B, 3B).  There is one candidate quotient range per integer
// q; no plaintext integer is enumerated.
func yesPiecesForInterval(iv Interval, s, n, b *big.Int) ([]Interval, error) {
	twoB := new(big.Int).Lsh(new(big.Int).Set(b), 1)
	threeB := new(big.Int).Mul(new(big.Int).Set(b), bigThree)

	loTimesS := new(big.Int).Mul(iv.Lo, s)
	hiTimesS := new(big.Int).Mul(iv.Hi, s)
	qLo := new(big.Int).Quo(loTimesS, n)
	qHi := new(big.Int).Quo(hiTimesS, n)

	out := make([]Interval, 0, 1)
	q := new(big.Int).Set(qLo)
	for q.Cmp(qHi) <= 0 {
		qn := new(big.Int).Mul(q, n)
		lowerNumerator := new(big.Int).Add(qn, twoB)
		upperNumerator := new(big.Int).Add(qn, threeB)
		lower := ceilDiv(lowerNumerator, s)
		upper := new(big.Int).Sub(ceilDiv(upperNumerator, s), bigOne)

		if lower.Cmp(iv.Lo) < 0 {
			lower = new(big.Int).Set(iv.Lo)
		}
		if upper.Cmp(iv.Hi) > 0 {
			upper = new(big.Int).Set(iv.Hi)
		}
		if lower.Cmp(upper) <= 0 {
			out = append(out, Interval{Lo: lower, Hi: upper})
		}
		q = new(big.Int).Add(q, bigOne)
	}
	return MergeIntervals(out)
}

// subtractPieces removes a sorted, merged set of disjoint pieces from iv.
func subtractPieces(iv Interval, pieces []Interval) []Interval {
	out := make([]Interval, 0)
	pos := new(big.Int).Set(iv.Lo)
	for _, cut := range pieces {
		if cut.Hi.Cmp(pos) < 0 {
			continue
		}
		if cut.Lo.Cmp(iv.Hi) > 0 {
			break
		}
		if cut.Lo.Cmp(pos) > 0 {
			hi := new(big.Int).Sub(cut.Lo, bigOne)
			if hi.Cmp(iv.Hi) > 0 {
				hi = new(big.Int).Set(iv.Hi)
			}
			if pos.Cmp(hi) <= 0 {
				out = append(out, Interval{Lo: new(big.Int).Set(pos), Hi: hi})
			}
		}
		if cut.Hi.Cmp(pos) >= 0 {
			pos = new(big.Int).Add(cut.Hi, bigOne)
		}
		if pos.Cmp(iv.Hi) > 0 {
			return out
		}
	}
	if pos.Cmp(iv.Hi) <= 0 {
		out = append(out, Interval{Lo: new(big.Int).Set(pos), Hi: new(big.Int).Set(iv.Hi)})
	}
	return out
}
