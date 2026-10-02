package decisiontable

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Value is a concrete condition value: bool, string or float64.
type Value = any

// Cond is a matcher on a single condition dimension.
type Cond interface {
	// Matches reports whether a concrete value satisfies the condition.
	Matches(v Value) bool
	// Overlaps reports whether two conditions can match a common value.
	Overlaps(o Cond) bool
	// Covers reports whether every value matched by o is also matched here.
	Covers(o Cond) bool
	// Union merges two conditions into one, if representable as a single Cond.
	Union(o Cond) (Cond, bool)
	// Intersect returns the condition matching only values matched by both,
	// or nil when the conditions are disjoint.
	Intersect(o Cond) Cond
	Equal(o Cond) bool
	IsWildcard() bool
	String() string
}

// AnyCond is the wildcard condition: it matches every value.
type AnyCond struct{}

func (AnyCond) Matches(Value) bool      { return true }
func (AnyCond) Overlaps(Cond) bool      { return true }
func (AnyCond) Covers(Cond) bool        { return true }
func (AnyCond) Union(Cond) (Cond, bool) { return AnyCond{}, true }
func (AnyCond) Intersect(o Cond) Cond   { return o }
func (AnyCond) Equal(o Cond) bool       { _, ok := o.(AnyCond); return ok }
func (AnyCond) IsWildcard() bool        { return true }
func (AnyCond) String() string          { return "*" }

// SetCond matches any value from a finite set. When Domain is provided and
// the set covers the whole domain, the condition collapses to a wildcard.
type SetCond struct {
	Values map[Value]struct{}
	Domain []Value
}

// NewSetCond builds a SetCond; it returns AnyCond when vals cover domain.
func NewSetCond(domain []Value, vals ...Value) Cond {
	s := SetCond{Values: make(map[Value]struct{}, len(vals)), Domain: domain}
	for _, v := range vals {
		s.Values[v] = struct{}{}
	}
	if domain != nil && len(s.Values) >= len(domain) {
		coversAll := true
		for _, d := range domain {
			if _, ok := s.Values[d]; !ok {
				coversAll = false
				break
			}
		}
		if coversAll {
			return AnyCond{}
		}
	}
	return s
}

func (s SetCond) Matches(v Value) bool {
	_, ok := s.Values[v]
	return ok
}

func (s SetCond) Overlaps(o Cond) bool {
	switch o := o.(type) {
	case AnyCond:
		return true
	case SetCond:
		for v := range s.Values {
			if _, ok := o.Values[v]; ok {
				return true
			}
		}
	}
	return false
}

func (s SetCond) Covers(o Cond) bool {
	oSet, ok := o.(SetCond)
	if !ok {
		return false
	}
	for v := range oSet.Values {
		if _, ok := s.Values[v]; !ok {
			return false
		}
	}
	return true
}

func (s SetCond) Union(o Cond) (Cond, bool) {
	switch o := o.(type) {
	case AnyCond:
		return AnyCond{}, true
	case SetCond:
		vals := make([]Value, 0, len(s.Values)+len(o.Values))
		for v := range s.Values {
			vals = append(vals, v)
		}
		for v := range o.Values {
			if _, ok := s.Values[v]; !ok {
				vals = append(vals, v)
			}
		}
		domain := s.Domain
		if domain == nil {
			domain = o.Domain
		}
		return NewSetCond(domain, vals...), true
	}
	return nil, false
}

func (s SetCond) Intersect(o Cond) Cond {
	switch o := o.(type) {
	case AnyCond:
		return s
	case SetCond:
		var vals []Value
		for v := range s.Values {
			if _, ok := o.Values[v]; ok {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			return nil
		}
		domain := s.Domain
		if domain == nil {
			domain = o.Domain
		}
		return NewSetCond(domain, vals...)
	}
	return nil
}

func (s SetCond) Equal(o Cond) bool {
	oSet, ok := o.(SetCond)
	if !ok || len(oSet.Values) != len(s.Values) {
		return false
	}
	for v := range s.Values {
		if _, ok := oSet.Values[v]; !ok {
			return false
		}
	}
	return true
}

func (s SetCond) IsWildcard() bool { return false }

func (s SetCond) String() string {
	parts := make([]string, 0, len(s.Values))
	for v := range s.Values {
		parts = append(parts, fmt.Sprint(v))
	}
	sort.Strings(parts)
	if len(parts) == 1 {
		return parts[0]
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// RangeCond matches a numeric interval [Lo, Hi] with inclusive/exclusive
// bounds. Use math.Inf for open-ended ranges.
type RangeCond struct {
	Lo, Hi       float64
	LoInc, HiInc bool
}

// NewRangeCond builds a RangeCond; it returns AnyCond for (-inf, +inf).
func NewRangeCond(lo, hi float64, loInc, hiInc bool) Cond {
	if math.IsInf(lo, -1) && math.IsInf(hi, 1) {
		return AnyCond{}
	}
	if math.IsInf(lo, -1) {
		loInc = true
	}
	if math.IsInf(hi, 1) {
		hiInc = true
	}
	return RangeCond{Lo: lo, Hi: hi, LoInc: loInc, HiInc: hiInc}
}

func toFloat(v Value) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

func (r RangeCond) Matches(v Value) bool {
	f, ok := toFloat(v)
	if !ok {
		return false
	}
	if f < r.Lo || (f == r.Lo && !r.LoInc) {
		return false
	}
	if f > r.Hi || (f == r.Hi && !r.HiInc) {
		return false
	}
	return true
}

func (r RangeCond) Overlaps(o Cond) bool {
	or, ok := o.(RangeCond)
	if !ok {
		_, isAny := o.(AnyCond)
		return isAny
	}
	if r.Lo > or.Hi || (r.Lo == or.Hi && !(r.LoInc && or.HiInc)) {
		return false
	}
	if or.Lo > r.Hi || (or.Lo == r.Hi && !(or.LoInc && r.HiInc)) {
		return false
	}
	return true
}

func (r RangeCond) Covers(o Cond) bool {
	or, ok := o.(RangeCond)
	if !ok {
		return false
	}
	loOk := r.Lo < or.Lo || (r.Lo == or.Lo && (r.LoInc || !or.LoInc))
	hiOk := r.Hi > or.Hi || (r.Hi == or.Hi && (r.HiInc || !or.HiInc))
	return loOk && hiOk
}

// adjacent reports whether r ends exactly where o starts with no gap.
func (r RangeCond) adjacent(o RangeCond) bool {
	return r.Hi == o.Lo && (r.HiInc || o.LoInc)
}

func (r RangeCond) Union(o Cond) (Cond, bool) {
	if _, ok := o.(AnyCond); ok {
		return AnyCond{}, true
	}
	or, ok := o.(RangeCond)
	if !ok {
		return nil, false
	}
	if !r.Overlaps(or) && !r.adjacent(or) && !or.adjacent(r) {
		return nil, false
	}
	lo, loInc := r.Lo, r.LoInc
	if or.Lo < lo || (or.Lo == lo && or.LoInc) {
		lo, loInc = or.Lo, or.LoInc
	}
	hi, hiInc := r.Hi, r.HiInc
	if or.Hi > hi || (or.Hi == hi && or.HiInc) {
		hi, hiInc = or.Hi, or.HiInc
	}
	return NewRangeCond(lo, hi, loInc, hiInc), true
}

func (r RangeCond) Intersect(o Cond) Cond {
	if _, ok := o.(AnyCond); ok {
		return r
	}
	or, ok := o.(RangeCond)
	if !ok {
		return nil
	}
	lo, loInc := r.Lo, r.LoInc
	if or.Lo > lo || (or.Lo == lo && !or.LoInc) {
		lo, loInc = or.Lo, or.LoInc
	}
	hi, hiInc := r.Hi, r.HiInc
	if or.Hi < hi || (or.Hi == hi && !or.HiInc) {
		hi, hiInc = or.Hi, or.HiInc
	}
	if lo > hi || (lo == hi && !(loInc && hiInc)) {
		return nil
	}
	return NewRangeCond(lo, hi, loInc, hiInc)
}

func (r RangeCond) Equal(o Cond) bool {
	or, ok := o.(RangeCond)
	return ok && r == or
}

func (r RangeCond) IsWildcard() bool { return false }

func (r RangeCond) String() string {
	lo, hi := "[", "]"
	if !r.LoInc || math.IsInf(r.Lo, -1) {
		lo = "("
	}
	if !r.HiInc || math.IsInf(r.Hi, 1) {
		hi = ")"
	}
	return lo + fmtNum(r.Lo) + ", " + fmtNum(r.Hi) + hi
}

func fmtNum(f float64) string {
	switch {
	case math.IsInf(f, -1):
		return "-∞"
	case math.IsInf(f, 1):
		return "+∞"
	default:
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
}
