package loopkit

import "math/big"

// TripKind classifies a derived loop trip count.
type TripKind int

const (
	TripFinite   TripKind = iota // exact non-negative iteration count
	TripInfinite                 // provably never exits under wrap arithmetic
	TripUnknown                  // analysis cannot decide safely
)

func (k TripKind) String() string {
	switch k {
	case TripFinite:
		return "finite"
	case TripInfinite:
		return "infinite"
	default:
		return "unknown"
	}
}

// TripResult is the exact trip-count verdict for one loop.
type TripResult struct {
	Kind   TripKind
	Count  uint64 // valid only when Kind == TripFinite
	Bits   int
	Signed bool
	Reason string
}

// TripCount64 returns the finite count, or (0,false) for infinite/unknown.
func (r TripResult) TripCount64() (uint64, bool) {
	if r.Kind != TripFinite {
		return 0, false
	}
	return r.Count, true
}

// TripCount derives the exact trip count of the loop controlling an
// inductive recurrence; wrap-around is never misreported as finite.
func (s *SCEV) TripCount(l *Loop) TripResult {
	ec := s.FindExitCmp(l)
	if ec == nil || ec.Ind.Kind != SCEVAddRec {
		return TripResult{Kind: TripUnknown, Reason: "no inductive exit comparison"}
	}
	bits := ec.Ind.Typ.Bits()
	if bits == 0 || bits > 64 {
		return TripResult{Kind: TripUnknown, Reason: "unsupported bit width"}
	}
	res := solveTrip(ec, bits)
	if res.Kind == TripFinite && ec.PostTest {
		// Do-while runs at least once: trips = failing index + 1.
		res.Count++
	}
	res.Bits = bits
	res.Signed = ec.Pred.Signed()
	return res
}

func solveTrip(ec *ExitCmp, bits int) TripResult {
	start := big.NewInt(ec.Ind.Start.Const)
	step := big.NewInt(ec.Ind.Step.Const)
	bound := big.NewInt(ec.Bound.Const)
	if step.Sign() == 0 {
		if evalPred(ec.Pred, start, bound, bits) {
			return TripResult{Kind: TripInfinite, Reason: "zero step, condition stays true"}
		}
		return TripResult{Kind: TripFinite, Count: 0}
	}
	if ec.Pred.Signed() {
		return solveSigned(ec, bits, start, step, bound)
	}
	return solveUnsigned(ec, bits, start, step, bound)
}

// solveUnsigned handles modular arithmetic over [0, 2^bits).
func solveUnsigned(ec *ExitCmp, bits int, start, step, bound *big.Int) TripResult {
	mod := new(big.Int).Lsh(big.NewInt(1), uint(bits))
	mask := new(big.Int).Sub(mod, big.NewInt(1))
	start.And(start, mask)
	step.And(step, mask)
	bound.And(bound, mask)

	// A negative modular step is mirrored to a positive-step problem.
	if step.Cmp(new(big.Int).Rsh(mod, 1)) >= 0 {
		negStep := new(big.Int).Sub(mod, step)
		// Mirror value v -> M-1-v keeps the ring ordering but reverses
		// step sign and flips strict/non-strict inequalities.
		maxv := new(big.Int).Sub(mod, big.NewInt(1))
		start2 := new(big.Int).Sub(maxv, start)
		bound2 := new(big.Int).Sub(maxv, bound)
		mp := mirrorUnsignedPred(ec.Pred)
		if mp < 0 {
			return TripResult{Kind: TripUnknown, Reason: "cannot mirror unsigned predicate"}
		}
		ec2 := &ExitCmp{Pred: mp}
		return solveUnsigned(ec2, bits, start2, negStep, bound2)
	}

	switch ec.Pred {
	case PredULT:
		return countLess(bits, start, step, bound, mod, false)
	case PredULE:
		b := new(big.Int).Add(bound, big.NewInt(1))
		if b.Cmp(mod) >= 0 {
			return countLess(bits, start, step, mod, mod, true)
		}
		return countLess(bits, start, step, b, mod, false)
	case PredUGT:
		maxv := new(big.Int).Sub(mod, big.NewInt(1))
		start2 := new(big.Int).Sub(maxv, start)
		bound2 := new(big.Int).Sub(maxv, bound)
		return solveUnsigned(&ExitCmp{Pred: PredULT}, bits, start2, step, bound2)
	case PredUGE:
		maxv := new(big.Int).Sub(mod, big.NewInt(1))
		start2 := new(big.Int).Sub(maxv, start)
		bound2 := new(big.Int).Sub(mod, bound)
		return solveUnsigned(&ExitCmp{Pred: PredULT}, bits, start2, step, bound2)
	case PredEQ:
		return countEqual(start, step, bound, mod, false)
	case PredNE:
		return countEqual(start, step, bound, mod, true)
	default:
		return TripResult{Kind: TripUnknown, Reason: "predicate not unsigned"}
	}
}

// countLess solves "x < limit" with positive step s on a ring of size
// mod. fullRing means the condition covers every representable value.
func countLess(bits int, start, step, limit, mod *big.Int, fullRing bool) TripResult {
	if !fullRing && start.Cmp(limit) >= 0 {
		return TripResult{Kind: TripFinite, Count: 0}
	}
	if fullRing {
		return TripResult{Kind: TripInfinite, Reason: "wrap-around keeps condition true"}
	}
	diff := new(big.Int).Sub(limit, start) // positive
	n := ceilDiv(diff, step)
	// The exit happens when the recurrence first reaches a value >= limit.
	// If that failing value cannot be represented without wrapping, the
	// loop never exits: report infinite rather than a bogus finite count.
	failVal := new(big.Int).Add(start, new(big.Int).Mul(n, step))
	if failVal.Cmp(new(big.Int).Sub(mod, big.NewInt(1))) > 0 {
		return TripResult{Kind: TripInfinite, Reason: "step wraps past MAX before exit"}
	}
	return uintResult(n)
}

// countEqual handles EQ (continue while x == bound) and NE forms.
func countEqual(start, step, bound, mod *big.Int, ne bool) TripResult {
	if !ne {
		// Continue only while equal: at most one iteration when it matches.
		if start.Cmp(bound) == 0 {
			return TripResult{Kind: TripFinite, Count: 1}
		}
		return TripResult{Kind: TripFinite, Count: 0}
	}
	if start.Cmp(bound) == 0 {
		return TripResult{Kind: TripFinite, Count: 0}
	}
	diff := new(big.Int).Sub(bound, start)
	diff.Mod(diff, mod)
	g := new(big.Int).GCD(nil, nil, step, mod)
	if new(big.Int).Mod(diff, g).Sign() != 0 {
		return TripResult{Kind: TripInfinite, Reason: "bound unreachable under modular step"}
	}
	m := new(big.Int).Quo(mod, g)
	s := new(big.Int).Quo(step, g)
	d := new(big.Int).Quo(diff, g)
	inv := new(big.Int).ModInverse(s, m)
	if inv == nil {
		return TripResult{Kind: TripUnknown, Reason: "non-invertible step"}
	}
	k := new(big.Int).Mul(d, inv)
	k.Mod(k, m)
	if k.Sign() == 0 {
		k.Set(m) // smallest positive solution
	}
	return uintResult(k)
}

// solveSigned keeps values in signed range; crossing MIN/MAX is a wrap.
func solveSigned(ec *ExitCmp, bits int, start, step, bound *big.Int) TripResult {
	min := new(big.Int).Lsh(big.NewInt(1), uint(bits-1))
	maxv := new(big.Int).Sub(min, big.NewInt(1))

	if step.Sign() > 0 {
		switch ec.Pred {
		case PredSLT:
			if start.Cmp(bound) >= 0 {
				return TripResult{Kind: TripFinite, Count: 0}
			}
			n := ceilDiv(new(big.Int).Sub(bound, start), step)
			failVal := new(big.Int).Add(start, new(big.Int).Mul(n, step))
			if failVal.Cmp(maxv) > 0 {
				return TripResult{Kind: TripInfinite, Reason: "signed step wraps past MAX"}
			}
			return uintResult(n)
		case PredSLE:
			if start.Cmp(bound) > 0 {
				return TripResult{Kind: TripFinite, Count: 0}
			}
			n := ceilDiv(new(big.Int).Add(
				new(big.Int).Sub(bound, start), big.NewInt(1)), step)
			failVal := new(big.Int).Add(start, new(big.Int).Mul(n, step))
			if failVal.Cmp(maxv) > 0 {
				return TripResult{Kind: TripInfinite, Reason: "signed step wraps past MAX"}
			}
			return uintResult(n)
		case PredSGT:
			// x > b  <=>  -x < -b.
			return solveSigned(&ExitCmp{Pred: PredSLT}, bits,
				new(big.Int).Neg(start), step, new(big.Int).Neg(bound))
		case PredSGE:
			// x >= b  <=>  -x <= -b  <=>  -x < -b + 1.
			return solveSigned(&ExitCmp{Pred: PredSLT}, bits,
				new(big.Int).Neg(start), step,
				new(big.Int).Add(new(big.Int).Neg(bound), big.NewInt(1)))
		}
	}
	if step.Sign() < 0 {
		mp := mirrorSignedPred(ec.Pred)
		if mp < 0 {
			return TripResult{Kind: TripUnknown, Reason: "cannot mirror signed predicate"}
		}
		return solveSigned(&ExitCmp{Pred: mp}, bits,
			new(big.Int).Neg(start), new(big.Int).Neg(step),
			new(big.Int).Neg(bound))
	}
	return TripResult{Kind: TripUnknown, Reason: "unhandled signed predicate"}
}

func uintResult(n *big.Int) TripResult {
	if !n.IsUint64() {
		return TripResult{Kind: TripUnknown, Reason: "trip count exceeds uint64"}
	}
	return TripResult{Kind: TripFinite, Count: n.Uint64()}
}

func ceilDiv(a, b *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(a, b, new(big.Int))
	if r.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

func mirrorUnsignedPred(p Pred) Pred {
	switch p {
	case PredULT:
		return PredUGT
	case PredULE:
		return PredUGE
	case PredUGT:
		return PredULT
	case PredUGE:
		return PredULE
	case PredEQ:
		return PredEQ
	case PredNE:
		return PredNE
	}
	return -1
}

func mirrorSignedPred(p Pred) Pred {
	switch p {
	case PredSLT:
		return PredSGT
	case PredSLE:
		return PredSGE
	case PredSGT:
		return PredSLT
	case PredSGE:
		return PredSLE
	case PredEQ:
		return PredEQ
	case PredNE:
		return PredNE
	}
	return -1
}

// evalPred evaluates a comparison on ring/signed values.
func evalPred(p Pred, a, b *big.Int, bits int) bool {
	if p.Signed() {
		min := new(big.Int).Lsh(big.NewInt(1), uint(bits-1))
		a, b = signedOf(a, min), signedOf(b, min)
	}
	cmp := a.Cmp(b)
	switch p {
	case PredEQ:
		return cmp == 0
	case PredNE:
		return cmp != 0
	case PredULT, PredSLT:
		return cmp < 0
	case PredULE, PredSLE:
		return cmp <= 0
	case PredUGT, PredSGT:
		return cmp > 0
	case PredUGE, PredSGE:
		return cmp >= 0
	}
	return false
}

func signedOf(v, min *big.Int) *big.Int {
	if v.Cmp(min) >= 0 {
		return new(big.Int).Sub(v, new(big.Int).Lsh(big.NewInt(1),
			uint(min.BitLen())))
	}
	return new(big.Int).Set(v)
}
