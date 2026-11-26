package temporal

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Kind selects the value domain of a Range.
type Kind int

const (
	// Continuous is the dense 64-bit microsecond timestamp domain.
	// Open/closed endpoint flags are preserved. It is the zero value so a
	// zero Range behaves as the empty continuous interval (0, 0).
	Continuous Kind = iota
	// Discrete is the integer domain. Ranges normalize to canonical [lo, hi).
	Discrete
)

// String returns the domain name.
func (k Kind) String() string {
	switch k {
	case Discrete:
		return "discrete"
	case Continuous:
		return "continuous"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Range is a temporal interval over either discrete integers or continuous
// microsecond timestamps.
//
// Unbounded lower/upper bounds are -inf/+inf respectively; when an endpoint
// is unbounded its value and inclusion flag are ignored.
type Range struct {
	// Kind is the domain of the interval.
	Kind Kind

	// Lower is the lower endpoint value; ignored when LowerUnbounded.
	Lower int64
	// Upper is the upper endpoint value; ignored when UpperUnbounded.
	Upper int64

	// LowerInclusive selects '[' when true and '(' when false.
	LowerInclusive bool
	// UpperInclusive selects ']' when true and ')' when false.
	UpperInclusive bool

	// LowerUnbounded means the lower bound is -inf.
	LowerUnbounded bool
	// UpperUnbounded means the upper bound is +inf.
	UpperUnbounded bool
}

// Empty returns the canonical empty range of the given domain.
func Empty(kind Kind) Range {
	if kind == Discrete {
		// [0, 0) contains no integer.
		return Range{Kind: Discrete, Lower: 0, Upper: 0, LowerInclusive: true, UpperInclusive: false}
	}
	// (0, 0) contains no continuous point.
	return Range{Kind: Continuous, Lower: 0, Upper: 0, LowerInclusive: false, UpperInclusive: false}
}

// IsEmpty reports whether the interval contains no values.
//
// Empty ranges never overlap any range. Every operation normalizes its
// operands first, so an un-normalized discrete (3, 4) is correctly empty.
func (r Range) IsEmpty() bool {
	c := r.Normalize()
	return c.isEmptyCanonical()
}

func (r Range) isEmptyCanonical() bool {
	if r.LowerUnbounded || r.UpperUnbounded {
		return false
	}
	if r.Kind == Discrete {
		return r.Lower >= r.Upper
	}
	if r.Lower > r.Upper {
		return true
	}
	if r.Lower == r.Upper && (!r.LowerInclusive || !r.UpperInclusive) {
		return true
	}
	return false
}

// Normalize returns the canonical representation of the range.
//
// Discrete ranges are shifted to the closed-open [lo, hi) form. Continuous
// ranges keep their endpoint flags. Empty ranges collapse to the canonical
// Empty(kind) sentinel. Normalize is idempotent.
func (r Range) Normalize() Range {
	out := r

	if r.Kind == Discrete {
		if r.LowerUnbounded {
			out.LowerInclusive = false
		} else {
			lo := r.Lower
			if !r.LowerInclusive {
				// (a ...) starts at a+1; (MaxInt64, ...) has no integer.
				if lo == math.MaxInt64 {
					return Empty(Discrete)
				}
				lo++
			}
			out.Lower = lo
			out.LowerInclusive = true
		}

		if r.UpperUnbounded {
			out.UpperInclusive = false
		} else {
			hi := r.Upper
			if r.UpperInclusive {
				// ... b] ends after b, i.e. exclusive bound b+1.
				if hi == math.MaxInt64 {
					// Within the int64 domain, [x, MaxInt64] equals [x, +inf).
					out.UpperUnbounded = true
					out.UpperInclusive = false
				} else {
					hi++
					out.Upper = hi
					out.UpperInclusive = false
				}
			} else {
				out.UpperInclusive = false
			}
		}

		if !out.LowerUnbounded && !out.UpperUnbounded && out.Lower >= out.Upper {
			return Empty(Discrete)
		}
		return out
	}

	// Continuous: preserve flags, only canonicalize unbounded inclusion.
	if r.LowerUnbounded {
		out.LowerInclusive = false
	}
	if r.UpperUnbounded {
		out.UpperInclusive = false
	}
	if !out.LowerUnbounded && !out.UpperUnbounded {
		if out.Lower > out.Upper {
			return Empty(Continuous)
		}
		if out.Lower == out.Upper && (!out.LowerInclusive || !out.UpperInclusive) {
			return Empty(Continuous)
		}
	}
	return out
}

// NewRange constructs a bounded range with explicit endpoint flags.
//
// Supported call forms:
//
//	NewRange(kind Kind, lower, upper int64, lowerInclusive, upperInclusive bool)
//	NewRange(lower, upper int64, lowerInclusive, upperInclusive bool)
//
// The four-argument form defaults to the continuous timestamp domain. Numeric
// arguments may be any integer kind.
func NewRange(args ...any) Range {
	var kind Kind = Continuous
	var lowerV, upperV any
	var li, ui bool

	switch len(args) {
	case 5:
		k, ok := args[0].(Kind)
		if !ok {
			if ki, ok2 := asInt64(args[0]); ok2 {
				k = Kind(ki)
			}
		}
		kind = k
		lowerV, upperV = args[1], args[2]
		li = asBool(args[3])
		ui = asBool(args[4])
	case 4:
		lowerV, upperV = args[0], args[1]
		li = asBool(args[2])
		ui = asBool(args[3])
	default:
		panic(fmt.Sprintf("temporal.NewRange: expected 4 or 5 arguments, got %d", len(args)))
	}

	lower, _ := asInt64(lowerV)
	upper, _ := asInt64(upperV)
	return Range{
		Kind:           kind,
		Lower:          lower,
		Upper:          upper,
		LowerInclusive: li,
		UpperInclusive: ui,
	}.Normalize()
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case float64:
		return int64(n), true
	case float32:
		return int64(n), true
	default:
		return 0, false
	}
}

// NewBoundRange constructs a range from optional endpoints: nil means
// unbounded on that side.
func NewBoundRange(kind Kind, lower, upper *int64, lowerInclusive, upperInclusive bool) Range {
	r := Range{Kind: kind, LowerInclusive: lowerInclusive, UpperInclusive: upperInclusive}
	if lower == nil {
		r.LowerUnbounded = true
	} else {
		r.Lower = *lower
	}
	if upper == nil {
		r.UpperUnbounded = true
	} else {
		r.Upper = *upper
	}
	return r.Normalize()
}

// NewUnbounded constructs a range with explicit bounded side and an
// unbounded opposite side. Exactly one of lower/upper must be non-nil.
func NewUnbounded(kind Kind, lower *int64, lowerInclusive bool, upper *int64, upperInclusive bool) Range {
	return NewBoundRange(kind, lower, upper, lowerInclusive, upperInclusive)
}

// AllRange returns the full universe (-inf, +inf) of the given domain.
func AllRange(kind Kind) Range {
	return Range{Kind: kind, LowerUnbounded: true, UpperUnbounded: true}.Normalize()
}

// NewIntRange returns the discrete canonical interval [lo, hi).
func NewIntRange(lo, hi int64) Range {
	return NewRange(Discrete, lo, hi, true, false)
}

// IntInterval is an alias for NewIntRange.
func IntInterval(lo, hi int64) Range { return NewIntRange(lo, hi) }

// NewDiscreteRange constructs a discrete range from arbitrary endpoint
// flags and normalizes it to [lo, hi).
func NewDiscreteRange(lower, upper int64, lowerInclusive, upperInclusive bool) Range {
	return NewRange(Discrete, lower, upper, lowerInclusive, upperInclusive)
}

// NewTimeRange constructs a continuous microsecond timestamp range.
func NewTimeRange(lower, upper int64, lowerInclusive, upperInclusive bool) Range {
	return NewRange(Continuous, lower, upper, lowerInclusive, upperInclusive)
}

// NewTimestampRange is an alias for NewTimeRange.
func NewTimestampRange(lower, upper int64, lowerInclusive, upperInclusive bool) Range {
	return NewTimeRange(lower, upper, lowerInclusive, upperInclusive)
}

// TsRange is a short alias for NewTimeRange.
func TsRange(lower, upper int64, lowerInclusive, upperInclusive bool) Range {
	return NewTimeRange(lower, upper, lowerInclusive, upperInclusive)
}

// Point returns the continuous single-point interval [t, t].
func Point(t int64) Range {
	return NewRange(Continuous, t, t, true, true)
}

// TimePoint is an alias for Point.
func TimePoint(t int64) Range { return Point(t) }

// IntPoint returns the discrete singleton containing exactly v, i.e. [v, v+1).
func IntPoint(v int64) Range {
	if v == math.MaxInt64 {
		// [MaxInt64, +inf) contains exactly MaxInt64 in the int64 domain.
		return Range{Kind: Discrete, Lower: v, LowerInclusive: true, UpperUnbounded: true}.Normalize()
	}
	return NewIntRange(v, v+1)
}

// LowerOpen returns (-inf, upper) style half-line with the given upper flag.
func LowerOpen(kind Kind, upper int64, upperInclusive bool) Range {
	return NewBoundRange(kind, nil, &upper, false, upperInclusive)
}

// UpperOpen returns a half-line from lower to +inf with the given lower flag.
func UpperOpen(kind Kind, lower int64, lowerInclusive bool) Range {
	return NewBoundRange(kind, &lower, nil, lowerInclusive, false)
}

// Equal reports whether two ranges denote the same set of values. All empty
// ranges of the same Kind compare equal.
func Equal(a, b Range) bool {
	a = a.Normalize()
	b = b.Normalize()
	if a.Kind != b.Kind {
		return false
	}
	if a.IsEmpty() || b.IsEmpty() {
		return a.IsEmpty() && b.IsEmpty()
	}
	if a.LowerUnbounded != b.LowerUnbounded || a.UpperUnbounded != b.UpperUnbounded {
		return false
	}
	if !a.LowerUnbounded {
		if a.Lower != b.Lower || a.LowerInclusive != b.LowerInclusive {
			return false
		}
	}
	if !a.UpperUnbounded {
		if a.Upper != b.Upper || a.UpperInclusive != b.UpperInclusive {
			return false
		}
	}
	return true
}

// Equals is the method form of Equal.
func (r Range) Equals(other Range) bool { return Equal(r, other) }

// SameKind reports whether a and b use the same domain.
func SameKind(a, b Range) bool { return a.Kind == b.Kind }

func kindMismatch(a, b Range) error {
	return fmt.Errorf("%w: %s vs %s", ErrKindMismatch, a.Kind, b.Kind)
}

// Contains reports whether the point x belongs to the range.
func (r Range) Contains(x int64) bool {
	c := r.Normalize()
	if c.isEmptyCanonical() {
		return false
	}
	if !c.LowerUnbounded {
		if c.LowerInclusive {
			if x < c.Lower {
				return false
			}
		} else if x <= c.Lower {
			return false
		}
	}
	if !c.UpperUnbounded {
		if c.UpperInclusive {
			if x > c.Upper {
				return false
			}
		} else if x >= c.Upper {
			return false
		}
	}
	return true
}

// ContainsRange reports whether every point of other is contained in r
// (other is a subset of r). An empty other is trivially contained. It
// returns false when the domains differ.
func (r Range) ContainsRange(other Range) bool {
	if r.Kind != other.Kind {
		return false
	}
	return len(Difference(other, r)) == 0
}

// String renders the range using the mixed-bracket notation from the
// specification, e.g. "[3,5)", "(t,t+1]", "(-inf,42)" or "empty".
func (r Range) String() string {
	c := r.Normalize()
	if c.isEmptyCanonical() {
		return "empty"
	}

	var lowerBracket, lowerText string
	if c.LowerUnbounded {
		lowerBracket = "("
		lowerText = "-inf"
	} else {
		lowerText = strconv.FormatInt(c.Lower, 10)
		if c.LowerInclusive {
			lowerBracket = "["
		} else {
			lowerBracket = "("
		}
	}

	var upperBracket, upperText string
	if c.UpperUnbounded {
		upperBracket = ")"
		upperText = "+inf"
	} else {
		upperText = strconv.FormatInt(c.Upper, 10)
		if c.UpperInclusive {
			upperBracket = "]"
		} else {
			upperBracket = ")"
		}
	}

	var b strings.Builder
	b.Grow(len(lowerText) + len(upperText) + 8)
	b.WriteString(lowerBracket)
	b.WriteString(lowerText)
	b.WriteString(",")
	b.WriteString(upperText)
	b.WriteString(upperBracket)
	return b.String()
}

// Format renders the range with the given separator between endpoints.
func (r Range) Format(sep string) string {
	s := r.String()
	if sep == "," || s == "empty" {
		return s
	}
	return strings.Replace(s, ",", sep, 1)
}

// GoString renders a verbose representation, useful in test failure output.
func (r Range) GoString() string {
	if r.IsEmpty() {
		return fmt.Sprintf("temporal.Empty(temporal.%s)", r.Kind)
	}
	return fmt.Sprintf("temporal.Range{Kind: temporal.%s, Lower: %d, Upper: %d, LI: %v, UI: %v, -inf: %v, +inf: %v}",
		r.Kind, r.Lower, r.Upper, r.LowerInclusive, r.UpperInclusive, r.LowerUnbounded, r.UpperUnbounded)
}
