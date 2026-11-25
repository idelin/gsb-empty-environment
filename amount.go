package displaycommit

import (
	"fmt"
	"math/big"
	"strings"
)

// Interval is a closed integer interval [Lo, Hi]. A nil Lo/Hi denotes the
// empty interval; unbounded intervals are intentionally not represented
// because rule verification must return Unknown rather than assume a bound.
type Interval struct {
	Lo    *big.Int `json:"lo,omitempty"`
	Hi    *big.Int `json:"hi,omitempty"`
	Empty bool     `json:"empty,omitempty"`
}

func PointInterval(v *big.Int) Interval {
	if v == nil {
		return Interval{Empty: true}
	}
	return Interval{Lo: new(big.Int).Set(v), Hi: new(big.Int).Set(v)}
}

func ClosedInterval(lo, hi *big.Int) Interval {
	if lo == nil || hi == nil || lo.Cmp(hi) > 0 {
		return Interval{Empty: true}
	}
	return Interval{Lo: new(big.Int).Set(lo), Hi: new(big.Int).Set(hi)}
}

func EmptyInterval() Interval { return Interval{Empty: true} }

func (iv Interval) IsEmpty() bool {
	return iv.Empty || iv.Lo == nil || iv.Hi == nil || iv.Lo.Cmp(iv.Hi) > 0
}

func (iv Interval) IsPoint() bool {
	return !iv.IsEmpty() && iv.Lo.Cmp(iv.Hi) == 0
}

func (iv Interval) Contains(v *big.Int) bool {
	if iv.IsEmpty() || v == nil {
		return false
	}
	return v.Cmp(iv.Lo) >= 0 && v.Cmp(iv.Hi) <= 0
}

func (iv Interval) Len() *big.Int {
	if iv.IsEmpty() {
		return big.NewInt(0)
	}
	return new(big.Int).Sub(iv.Hi, iv.Lo)
}

func (iv Interval) String() string {
	if iv.IsEmpty() {
		return "∅"
	}
	return "[" + iv.Lo.String() + "," + iv.Hi.String() + "]"
}

func UnionInterval(intervals ...Interval) Interval {
	var out Interval
	for _, iv := range intervals {
		if iv.IsEmpty() {
			continue
		}
		if out.Lo == nil {
			out.Lo = new(big.Int).Set(iv.Lo)
			out.Hi = new(big.Int).Set(iv.Hi)
			continue
		}
		if iv.Lo.Cmp(out.Lo) < 0 {
			out.Lo.Set(iv.Lo)
		}
		if iv.Hi.Cmp(out.Hi) > 0 {
			out.Hi.Set(iv.Hi)
		}
	}
	if out.Lo == nil {
		return EmptyInterval()
	}
	return out
}

func IntervalOfValues(values ...*big.Int) Interval {
	if len(values) == 0 {
		return EmptyInterval()
	}
	iv := PointInterval(values[0])
	for _, v := range values[1:] {
		iv = UnionInterval(iv, PointInterval(v))
	}
	return iv
}

// RoundingMode enumerates deterministic integer rounding modes.
type RoundingMode string

const (
	RoundHalfUp   RoundingMode = "half-up"
	RoundHalfEven RoundingMode = "half-even"
	RoundDown     RoundingMode = "down"
	RoundFloor    RoundingMode = "floor"
	RoundCeil     RoundingMode = "ceil"
)

type RoundingRule struct {
	// Unit is the integer rounding granularity in raw units. For decimal
	// rounding it is usually 10^(sourceDecimals-displayDecimals).
	Unit *big.Int `json:"unit,omitempty"`
	// SourceDecimals and DisplayDecimals provide an alternative to Unit.
	SourceDecimals  int          `json:"source_decimals,omitempty"`
	DisplayDecimals int          `json:"display_decimals,omitempty"`
	Mode            RoundingMode `json:"mode,omitempty"`
}

func (r RoundingRule) unit() (*big.Int, error) {
	if r.Unit != nil && r.Unit.Sign() > 0 {
		return new(big.Int).Set(r.Unit), nil
	}
	if r.SourceDecimals >= r.DisplayDecimals && r.DisplayDecimals >= 0 {
		u := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(r.SourceDecimals-r.DisplayDecimals)), nil)
		if u.Sign() > 0 {
			return u, nil
		}
	}
	if r.Unit != nil && r.Unit.Cmp(big.NewInt(1)) == 0 {
		return big.NewInt(1), nil
	}
	return nil, fmt.Errorf("displaycommit: rounding rule needs positive unit or decimals")
}

// RoundBigInt rounds a non-negative arbitrary-precision integer to Unit.
func RoundBigInt(value *big.Int, rule RoundingRule) (*big.Int, error) {
	if value == nil {
		return nil, fmt.Errorf("displaycommit: nil amount")
	}
	u, err := rule.unit()
	if err != nil {
		return nil, err
	}
	mode := rule.Mode
	if mode == "" {
		mode = RoundHalfUp
	}
	v := new(big.Int).Set(value)
	negative := v.Sign() < 0
	if negative {
		v.Abs(v)
	}
	q, rem := new(big.Int).QuoRem(v, u, new(big.Int))
	twice := new(big.Int).Mul(rem, big.NewInt(2))
	switch mode {
	case RoundDown, RoundFloor:
		// For negative values Floor means away from zero after symmetry is
		// restored by negating the rounded magnitude? Handle Floor below.
	case RoundCeil:
		if rem.Sign() > 0 {
			q.Add(q, big.NewInt(1))
		}
	case RoundHalfUp:
		if twice.Cmp(u) >= 0 {
			q.Add(q, big.NewInt(1))
		}
	case RoundHalfEven:
		cmp := twice.Cmp(u)
		if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
			q.Add(q, big.NewInt(1))
		}
	default:
		return nil, fmt.Errorf("displaycommit: unsupported rounding mode %q", mode)
	}
	rounded := new(big.Int).Mul(q, u)
	if negative {
		rounded.Neg(rounded)
		if mode == RoundFloor && rem.Sign() > 0 {
			// Symmetric truncation produced ceil(abs(v)); negation gives ceil(v),
			// so subtract one unit for floor.
			rounded.Sub(rounded, u)
		}
		if mode == RoundCeil {
			// We rounded abs up; negative result is floor, while ceil(v) is
			// truncation toward zero. Recompute simply via quotient.
			q0, _ := new(big.Int).QuoRem(new(big.Int).Set(value), u, new(big.Int))
			rounded = new(big.Int).Mul(q0, u)
		}
	}
	return rounded, nil
}

// HalfUpInterval returns the closed interval of non-negative raw integers
// that round to rounded under round-half-up.
func RoundingInterval(rounded *big.Int, rule RoundingRule) (Interval, error) {
	u, err := rule.unit()
	if err != nil {
		return EmptyInterval(), err
	}
	if rounded == nil || rounded.Sign() < 0 || new(big.Int).Rem(rounded, u).Sign() != 0 {
		return EmptyInterval(), nil
	}
	mode := rule.Mode
	if mode == "" {
		mode = RoundHalfUp
	}
	if mode != RoundHalfUp {
		// Exact bins for other modes can be added when needed. The verifier
		// treats an unsupported symbolic interval as Unknown rather than
		// overclaiming.
		return EmptyInterval(), nil
	}
	q := new(big.Int).Quo(rounded, u)
	ceilHalf := new(big.Int).Add(u, big.NewInt(1))
	ceilHalf.Div(ceilHalf, big.NewInt(2)) // ceil(u/2)
	if q.Sign() == 0 {
		hi := new(big.Int).Sub(ceilHalf, big.NewInt(1))
		return ClosedInterval(big.NewInt(0), hi), nil
	}
	lo := new(big.Int).Mul(new(big.Int).Sub(q, big.NewInt(1)), u)
	lo.Add(lo, ceilHalf)
	hi := new(big.Int).Mul(q, u)
	hi.Add(hi, ceilHalf)
	hi.Sub(hi, big.NewInt(1))
	return ClosedInterval(lo, hi), nil
}

// FormatDecimal formats an arbitrary precision integer as an exact decimal.
func FormatDecimal(v *big.Int, decimals int) string {
	if v == nil {
		return ""
	}
	neg := v.Sign() < 0
	s := new(big.Int).Abs(v).String()
	if decimals <= 0 {
		if neg {
			return "-" + s
		}
		return s
	}
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}
	dot := len(s) - decimals
	out := s[:dot] + "." + s[dot:]
	out = strings.TrimRight(out, "0")
	out = strings.TrimRight(out, ".")
	if neg && out != "0" {
		out = "-" + out
	}
	return out
}

// Amount is an exact asset-scoped arbitrary-precision quantity.
type Amount struct {
	Asset    string
	Value    *big.Int
	Decimals int
}

// AmountSet collects an asset-scoped group. Verification and display use its
// closed [Min,Max] interval instead of enumerating every possible value.
type AmountSet struct {
	Asset    string
	Decimals int
	Values   []*big.Int
}

func NewAmountSet(asset string, values ...*big.Int) AmountSet {
	return AmountSet{Asset: asset, Values: values}
}

func (s AmountSet) ClosedInterval() Interval {
	return IntervalOfValues(s.Values...)
}

func (s AmountSet) Len() int { return len(s.Values) }

func (a Amount) String() string {
	if a.Value == nil {
		return a.Asset + ":<unknown>"
	}
	return FormatDecimal(a.Value, a.Decimals) + " " + a.Asset
}

// Near256Bit reports whether the amount is around the 256-bit boundary. It is
// used by tests and domain generators; all arithmetic remains big.Int.
func Near256Bit(v *big.Int) bool {
	if v == nil {
		return false
	}
	bits := v.BitLen()
	return bits >= 248 && bits <= 260
}
