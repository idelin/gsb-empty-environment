package exact

import (
	"fmt"
	"math/big"
	"strings"
)

// RoundDirection describes how a half-even rounding step resolved.
type RoundDirection int

const (
	RoundDown           RoundDirection = iota // remainder below half: kept floor
	RoundUp                                   // remainder above half: incremented
	RoundTieToEven                            // exact midpoint: moved to even neighbour
	RoundTieAlreadyEven                       // exact midpoint: floor already even
	RoundExact                                // no fractional remainder at all
)

func (d RoundDirection) String() string {
	switch d {
	case RoundDown:
		return "down"
	case RoundUp:
		return "up"
	case RoundTieToEven:
		return "tie-to-even"
	case RoundTieAlreadyEven:
		return "tie-already-even"
	case RoundExact:
		return "exact"
	}
	return "unknown"
}

// RoundEvidence records the exact inputs and decision of one rounding step,
// so callers can audit why a particular digit string was produced.
type RoundEvidence struct {
	Input     *big.Rat       // exact value before rounding
	Places    int            // decimal places requested
	Scaled    *big.Int       // floor(Input * 10^Places)
	Remainder *big.Rat       // Input*10^Places - Scaled, in [0,1)
	Tie       bool           // remainder exactly 1/2
	Direction RoundDirection // how the tie/remainder resolved
	Result    *big.Int       // final integer coefficient (value = Result / 10^Places)
}

// roundHalfEvenInt rounds the exact rational value to `places` decimal digits
// using half-even (banker's) rounding, purely with big-integer Euclidean
// division. It returns the integer coefficient c such that the rounded value
// is c / 10^places, plus evidence. Negative zero is normalized: a zero
// coefficient is always +0.
func roundHalfEvenInt(value *big.Rat, places int) (*big.Int, *RoundEvidence) {
	if places < 0 {
		places = 0
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)

	// scaled = value * 10^places = num*scale / den
	num := new(big.Int).Mul(value.Num(), scale)
	den := value.Denom()

	// Euclidean division: quo = floor(num/den), rem in [0, den).
	quo, rem := new(big.Int), new(big.Int)
	quo.QuoRem(num, den, rem)
	if rem.Sign() < 0 {
		quo.Sub(quo, big.NewInt(1))
		rem.Add(rem, den)
	}

	ev := &RoundEvidence{
		Input:  new(big.Rat).Set(value),
		Places: places,
		Scaled: new(big.Int).Set(quo),
		Remainder: new(big.Rat).SetFrac(
			new(big.Int).Set(rem), new(big.Int).Set(den)),
		Result: new(big.Int).Set(quo),
	}

	switch rem.Sign() {
	case 0:
		ev.Direction = RoundExact
	default:
		twice := new(big.Int).Lsh(rem, 1) // 2*rem
		cmp := twice.Cmp(den)
		switch {
		case cmp > 0:
			quo.Add(quo, big.NewInt(1))
			ev.Direction = RoundUp
		case cmp < 0:
			ev.Direction = RoundDown
		default:
			ev.Tie = true
			if quo.Bit(0) == 1 { // odd: move to even neighbour
				quo.Add(quo, big.NewInt(1))
				ev.Direction = RoundTieToEven
			} else {
				ev.Direction = RoundTieAlreadyEven
			}
		}
	}
	ev.Result.Set(quo)
	if ev.Result.Sign() == 0 {
		ev.Result.SetInt64(0) // normalize negative zero
	}
	return ev.Result, ev
}

// RoundHalfEven rounds an exact rational to `places` decimal digits with
// half-even rounding and returns the rounded value as an exact rational.
func RoundHalfEven(value *big.Rat, places int) (*big.Rat, *RoundEvidence) {
	coef, ev := roundHalfEvenInt(value, places)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	return new(big.Rat).SetFrac(coef, scale), ev
}

// FormatFixed renders an exact rational with exactly `places` digits after
// the decimal point, applying half-even rounding. Negative zero renders as
// "0"/"0.00..." without a sign.
func FormatFixed(value *big.Rat, places int) string {
	coef, _ := roundHalfEvenInt(value, places)
	return FormatCoef(coef, places)
}

// FormatCoef renders a coefficient produced by roundHalfEvenInt.
func FormatCoef(coef *big.Int, places int) string {
	if coef.Sign() == 0 {
		if places == 0 {
			return "0"
		}
		return "0." + strings.Repeat("0", places)
	}
	neg := coef.Sign() < 0
	digits := new(big.Int).Abs(coef).String()
	if places > 0 {
		if len(digits) <= places {
			digits = strings.Repeat("0", places-len(digits)+1) + digits
		}
		i := len(digits) - places
		digits = digits[:i] + "." + digits[i:]
	}
	if neg {
		return "-" + digits
	}
	return digits
}

// String renders the evidence as a single auditable line.
func (ev *RoundEvidence) String() string {
	return fmt.Sprintf("round(input=%s, places=%d, floor=%s, rem=%s, tie=%v, dir=%s, result=%s)",
		ev.Input.RatString(), ev.Places, ev.Scaled.String(),
		ev.Remainder.RatString(), ev.Tie, ev.Direction,
		FormatCoef(ev.Result, ev.Places))
}
