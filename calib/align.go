package calib

import "math/big"

// Window is a reference-time interval [Lo, Hi] for an aligned event.
type Window struct {
	Lo *big.Rat
	Hi *big.Rat
}

// EventWindow maps a channel reading r to reference time t = (r - beta)/rho
// over the feasible parameter range. Because t is monotonic in beta and
// (for a fixed sign of r-beta) in rho, its extrema over the bounding box
// [OffsetLo, OffsetHi] x [DriftLo, DriftHi] occur at the corners; using the
// box rather than the exact feasible polygon is conservative, which is
// safe: it can only widen windows and therefore only drop order relations,
// never invent one.
//
// ok is false when the channel is infeasible, any parameter bound is
// unbounded, or the drift range is not strictly positive (clock not
// guaranteed monotone).
func EventWindow(reading *big.Rat, c Calib) (Window, bool) {
	if !c.Feasible || c.OffsetLo == nil || c.OffsetHi == nil ||
		c.DriftLo == nil || c.DriftHi == nil {
		return Window{}, false
	}
	if c.DriftLo.Sign() <= 0 {
		return Window{}, false
	}
	betas := []*big.Int{c.OffsetLo, c.OffsetHi}
	rhos := []*big.Rat{c.DriftLo, c.DriftHi}
	var lo, hi *big.Rat
	for _, beta := range betas {
		num := new(big.Rat).Sub(reading, new(big.Rat).SetInt(beta))
		for _, rho := range rhos {
			t := new(big.Rat).Quo(num, rho)
			if lo == nil || t.Cmp(lo) < 0 {
				lo = new(big.Rat).Set(t)
			}
			if hi == nil || t.Cmp(hi) > 0 {
				hi = new(big.Rat).Set(t)
			}
		}
	}
	return Window{Lo: lo, Hi: hi}, true
}

// Rel is the relation between two events.
type Rel int

const (
	Before       Rel = iota // first definitely precedes second
	After                   // first definitely follows second
	Incomparable            // order undetermined: kept in the partial order
)

// Compare decides the order of two events without ever falling back to
// midpoint estimates:
//
//   - same channel: the map reading -> time is strictly increasing
//     (positive drift), so readings order events exactly; equal readings
//     are incomparable;
//   - different channels: e precedes f only if every feasible time of e is
//     strictly before every feasible time of f (window separation);
//   - anything else, including unalignable events, stays incomparable.
func Compare(e, f Event, we, wf Window, oke, okf bool) Rel {
	if e.Channel == f.Channel {
		if !oke || !okf {
			return Incomparable
		}
		switch e.Reading.Cmp(f.Reading) {
		case -1:
			return Before
		case 1:
			return After
		default:
			return Incomparable
		}
	}
	if !oke || !okf {
		return Incomparable
	}
	if we.Hi.Cmp(wf.Lo) < 0 {
		return Before
	}
	if wf.Hi.Cmp(we.Lo) < 0 {
		return After
	}
	return Incomparable
}

// Pair records one ordered or incomparable event pair.
type Pair struct {
	First  string
	Second string
}

// PartialOrder compares every event pair and returns the definite
// precedence pairs together with the pairs that remain unordered.
func PartialOrder(events []Event, calibs map[string]Calib) (prec []Pair, incomparable []Pair) {
	wins := make([]Window, len(events))
	ok := make([]bool, len(events))
	for i, e := range events {
		c, found := calibs[e.Channel]
		if !found {
			continue
		}
		wins[i], ok[i] = EventWindow(e.Reading, c)
	}
	for i := 0; i < len(events); i++ {
		for j := i + 1; j < len(events); j++ {
			switch Compare(events[i], events[j], wins[i], wins[j], ok[i], ok[j]) {
			case Before:
				prec = append(prec, Pair{events[i].ID, events[j].ID})
			case After:
				prec = append(prec, Pair{events[j].ID, events[i].ID})
			default:
				incomparable = append(incomparable, Pair{events[i].ID, events[j].ID})
			}
		}
	}
	return prec, incomparable
}
