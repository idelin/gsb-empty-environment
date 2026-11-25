package parity

import (
	"math/big"
)

// RatInterval is a closed exact rational interval [Lo, Hi].
//
// Endpoints are always non-negative during a recovery and Lo <= Hi.
type RatInterval struct {
	Lo *big.Rat `json:"lo"`
	Hi *big.Rat `json:"hi"`
}

func newRatInterval(lo, hi *big.Rat) RatInterval {
	return RatInterval{Lo: new(big.Rat).Set(lo), Hi: new(big.Rat).Set(hi)}
}

// cloneIntervals returns a deep copy so checkpoints cannot alias mutable state.
func cloneIntervals(in []RatInterval) []RatInterval {
	out := make([]RatInterval, len(in))
	for i, iv := range in {
		out[i] = RatInterval{Lo: new(big.Rat).Set(iv.Lo), Hi: new(big.Rat).Set(iv.Hi)}
	}
	return out
}

// floorRat returns floor(r) as an integer. All recovery rationals are
// non-negative; Quo is therefore exact floor division.
func floorRat(r *big.Rat) *big.Int {
	return new(big.Int).Quo(r.Num(), r.Denom())
}

// ceilRat returns ceil(r).
func ceilRat(r *big.Rat) *big.Int {
	n := r.Num()
	d := r.Denom()
	q, rem := new(big.Int).QuoRem(n, d, new(big.Int))
	if rem.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

var (
	bigZero = big.NewInt(0)
	bigOne  = big.NewInt(1)
)

// integerRange returns the inclusive integer candidate range [lo, hi] inside
// iv, additionally clamped to the plaintext domain 0 <= m <= nMinusOne.
//
// It returns ok=false when there is no integer candidate.
func (iv RatInterval) integerRange(nMinusOne *big.Int) (lo, hi *big.Int, ok bool) {
	lo = ceilRat(iv.Lo)
	hi = floorRat(iv.Hi)
	if lo.Sign() < 0 {
		lo = big.NewInt(0)
	}
	if nMinusOne != nil && lo.Cmp(bigZero) < 0 {
		lo.SetInt64(0)
	}
	if hi.Cmp(nMinusOne) > 0 {
		hi = new(big.Int).Set(nMinusOne)
	}
	if lo.Cmp(hi) > 0 {
		return nil, nil, false
	}
	return lo, hi, true
}

// countCandidates counts integer plaintext candidates across all intervals and
// returns the unique candidate when the total is exactly one.
func countCandidates(intervals []RatInterval, N *big.Int) (count *big.Int, unique *big.Int) {
	nMinusOne := new(big.Int).Sub(N, bigOne)
	total := new(big.Int)
	var found *big.Int
	for _, iv := range intervals {
		lo, hi, ok := iv.integerRange(nMinusOne)
		if !ok {
			continue
		}
		size := new(big.Int).Sub(hi, lo)
		size.Add(size, bigOne)
		total.Add(total, size)
		if size.Cmp(bigOne) == 0 {
			if found == nil {
				found = lo
			} else if found.Cmp(lo) != 0 {
				// Two distinct singleton ranges.
				found = nil
			}
		}
	}
	if total.Cmp(bigOne) == 0 && found != nil {
		return total, new(big.Int).Set(found)
	}
	return total, nil
}

// parityOfK returns big.Int bit 0 as Parity.
func parityOfK(k *big.Int) Parity {
	if k.Bit(0) == 1 {
		return ParityOdd
	}
	return ParityEven
}

// applyParity intersects the feasible intervals with the exact constraint
//
//	Parity((T*m) mod N) == want
//
// for a positive EVEN multiplier T and odd N.
//
// Because T is even and N is odd, parity((T m) mod N) == parity(floor(T m/N)).
// The floor changes value at exact rational strip boundaries k*N/T. Only strips
// whose k has the requested parity are retained.
//
// gcd(T,N) == 1 makes every internal strip boundary non-integer, so closed
// rational endpoints never introduce spurious interior integer candidates.
func applyParity(intervals []RatInterval, N, T *big.Int, want Parity, maxIntervals int) ([]RatInterval, error) {
	nRat := new(big.Rat).SetInt(N)
	tRat := new(big.Rat).SetInt(T)

	var out []RatInterval
	for _, iv := range intervals {
		// Exact integer bounds for k = floor(T*m/N) over this interval.
		timesLo := new(big.Rat).Mul(new(big.Rat).SetInt(T), iv.Lo)
		timesLo.Quo(timesLo, nRat) // T*Lo/N
		kFirst := floorRat(timesLo)

		timesHi := new(big.Rat).Mul(new(big.Rat).SetInt(T), iv.Hi)
		timesHi.Quo(timesHi, nRat) // T*Hi/N
		kLast := ceilRat(timesHi)

		// Iterate the small number of overlapping strips. The number is the
		// exact strip density T*(Hi-Lo)/N + O(1), which is ~2 on the power-of-2
		// path and ~2*r for a small blinding factor r.
		k := new(big.Int).Set(kFirst)
		k.Sub(k, bigOne) // pad by one; zero-length overlaps are discarded
		end := new(big.Int).Set(kLast)
		end.Add(end, bigOne)

		for ; k.Cmp(end) <= 0; k.Add(k, bigOne) {
			if parityOfK(k) != want {
				continue
			}
			// Strip is [k*N/T, (k+1)*N/T].
			kRat := new(big.Rat).SetInt(k)
			stripLo := new(big.Rat).Mul(kRat, nRat)
			stripLo.Quo(stripLo, tRat)

			k1 := new(big.Int).Add(k, bigOne)
			stripHi := new(big.Rat).SetInt(k1)
			stripHi.Mul(stripHi, nRat)
			stripHi.Quo(stripHi, tRat)

			lo := ratMax(iv.Lo, stripLo)
			hi := ratMin(iv.Hi, stripHi)
			// Strictly positive rational width discards boundary-only artifacts
			// such as the m=0 endpoint of a negative-index strip.
			if lo.Cmp(hi) >= 0 {
				continue
			}
			out = append(out, RatInterval{Lo: lo, Hi: hi})
			if maxIntervals > 0 && len(out) > maxIntervals {
				return nil, ErrIntervalBudget
			}
		}
	}
	merged := mergeIntervals(out)
	if maxIntervals > 0 && len(merged) > maxIntervals {
		return nil, ErrIntervalBudget
	}
	return merged, nil
}

func ratMax(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) >= 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}

func ratMin(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}

// mergeIntervals sorts and merges overlapping/touching exact intervals.
func mergeIntervals(in []RatInterval) []RatInterval {
	if len(in) == 0 {
		return in
	}
	// Insertion-free simple sort via big.Rat comparator.
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j-1].Lo.Cmp(in[j].Lo) > 0; j-- {
			in[j-1], in[j] = in[j], in[j-1]
		}
	}
	out := make([]RatInterval, 0, len(in))
	for _, iv := range in {
		if len(out) == 0 {
			out = append(out, iv)
			continue
		}
		last := &out[len(out)-1]
		if iv.Lo.Cmp(last.Hi) <= 0 {
			if iv.Hi.Cmp(last.Hi) > 0 {
				last.Hi = iv.Hi
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// hull returns the outermost exact rational endpoints.
func hull(intervals []RatInterval) (lo, hi *big.Rat, ok bool) {
	if len(intervals) == 0 {
		return nil, nil, false
	}
	lo = new(big.Rat).Set(intervals[0].Lo)
	hi = new(big.Rat).Set(intervals[0].Hi)
	for _, iv := range intervals[1:] {
		if iv.Lo.Cmp(lo) < 0 {
			lo.Set(iv.Lo)
		}
		if iv.Hi.Cmp(hi) > 0 {
			hi.Set(iv.Hi)
		}
	}
	return lo, hi, true
}

// pruneToIntegerRanges replaces fragmented rational intervals with their exact
// inclusive integer candidate ranges and merges them. Regions containing no
// integer are discarded, which is exact because the secret is an integer. It is
// used only on fragmented/blinded state; the canonical dyadic path keeps its
// fractional endpoints untouched.
func pruneToIntegerRanges(in []RatInterval, N *big.Int) []RatInterval {
	nMinusOne := new(big.Int).Sub(N, bigOne)
	var out []RatInterval
	for _, iv := range in {
		lo, hi, ok := iv.integerRange(nMinusOne)
		if !ok {
			continue
		}
		out = append(out, RatInterval{Lo: new(big.Rat).SetInt(lo), Hi: new(big.Rat).SetInt(hi)})
	}
	return mergeIntervals(out)
}
