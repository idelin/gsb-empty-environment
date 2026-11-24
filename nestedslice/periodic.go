package nestedslice

import (
	"errors"
	"sort"
)

// MaxExpand bounds any materialization performed by the planner. Regular
// selections stay symbolic regardless of their size.
const MaxExpand = 1 << 20

// ErrIrregularTooLarge is returned when an exact selection can only be
// produced by expanding more than MaxExpand irregular values.
var ErrIrregularTooLarge = errors.New("nestedslice: irregular selection exceeds expansion limit")

// AP is a finite arithmetic progression First, First+Step, ... of Count
// values. Step must be non-zero and Count positive. A negative Step models
// reversed dictionary selections without reordering buffers.
type AP struct {
	First int64
	Step  int64
	Count int64
}

func (a AP) validate() {
	if a.Step == 0 || a.Count < 1 {
		panic("nestedslice: invalid arithmetic progression")
	}
}

// Last returns the final value of the progression.
func (a AP) Last() int64 {
	a.validate()
	if a.Count == 1 {
		return a.First
	}
	return addChecked(a.First, mulChecked(a.Step, a.Count-1))
}

// At returns the i-th value (0-based).
func (a AP) At(i int64) int64 {
	if i < 0 || i >= a.Count {
		panic("nestedslice: AP index out of range")
	}
	return addChecked(a.First, mulChecked(a.Step, i))
}

// Contains reports whether x is one of the progression values.
func (a AP) Contains(x int64) bool {
	if a.Step > 0 && (x < a.First || x > a.Last()) {
		return false
	}
	if a.Step < 0 && (x > a.First || x < a.Last()) {
		return false
	}
	d := x - a.First
	if d%a.Step != 0 {
		return false
	}
	return d/a.Step >= 0 && d/a.Step < a.Count
}

// Hull returns the smallest element interval [lo, hi) covering the
// progression, independent of its direction.
func (a AP) Hull() (lo, hi int64) {
	last := a.Last()
	if a.First <= last {
		return a.First, last + 1
	}
	return last, a.First + 1
}

// Shift translates every value by delta.
func (a AP) Shift(delta int64) AP {
	return AP{First: addChecked(a.First, delta), Step: a.Step, Count: a.Count}
}

// Scale multiplies every value by factor; a negative factor reverses the
// progression while preserving its value sequence.
func (a AP) Scale(factor int64) AP {
	if factor == 0 {
		panic("nestedslice: AP scale factor must be non-zero")
	}
	return AP{
		First: mulChecked(a.First, factor),
		Step:  mulChecked(a.Step, factor),
		Count: a.Count,
	}
}

func (a AP) merge(b AP) (AP, bool) {
	// Negative-step progressions encode iteration order and must never be
	// rewritten into positive-step interval descriptions.
	if a.Step <= 0 || b.Step <= 0 || a.Step != b.Step {
		return AP{}, false
	}
	if addChecked(a.Last(), a.Step) == b.First {
		return AP{First: a.First, Step: a.Step, Count: addChecked(a.Count, b.Count)}, true
	}
	if addChecked(b.Last(), b.Step) == a.First {
		return AP{First: b.First, Step: b.Step, Count: addChecked(a.Count, b.Count)}, true
	}
	return AP{}, false
}

// PSet is a finite set of int64 values represented as disjoint arithmetic
// progressions within the universe [Lo, Hi). The representation is a set:
// order between APs is irrelevant, while each AP keeps its own direction.
type PSet struct {
	Lo  int64
	Hi  int64
	APs []AP
}

// EmptySet returns an empty set bounded by [lo, hi).
func EmptySet(lo, hi int64) PSet {
	if lo > hi {
		panic("nestedslice: empty set bounds inverted")
	}
	return PSet{Lo: lo, Hi: hi}
}

// Interval returns the contiguous set [lo, hi).
func Interval(lo, hi int64) PSet {
	if lo > hi {
		panic("nestedslice: interval bounds inverted")
	}
	if lo == hi {
		return EmptySet(lo, hi)
	}
	return PSet{Lo: lo, Hi: hi, APs: []AP{{First: lo, Step: 1, Count: hi - lo}}}
}

// SetOf builds a set from explicit progressions, deriving the universe from
// their hulls. All values must be non-negative.
func SetOf(aps ...AP) PSet {
	if len(aps) == 0 {
		panic("nestedslice: SetOf needs bounds for an empty set")
	}
	lo, hi := aps[0].Hull()
	for _, a := range aps[1:] {
		alo, ahi := a.Hull()
		if alo < lo {
			lo = alo
		}
		if ahi > hi {
			hi = ahi
		}
	}
	for _, a := range aps {
		a.validate()
		alo, ahi := a.Hull()
		if alo < 0 {
			panic("nestedslice: periodic sets use non-negative indices")
		}
		_ = lo
		_ = hi
		_ = ahi
	}
	return PSet{Lo: lo, Hi: hi, APs: append([]AP(nil), aps...)}
}

// IsEmpty reports whether the set contains no values.
func (s PSet) IsEmpty() bool { return len(s.APs) == 0 }

// Count returns the number of distinct values.
func (s PSet) Count() int64 {
	var n int64
	for _, a := range s.APs {
		n = addChecked(n, a.Count)
	}
	return n
}

// Hull returns the smallest interval covering every value.
func (s PSet) Hull() (lo, hi int64, ok bool) {
	if s.IsEmpty() {
		return 0, 0, false
	}
	lo, hi = s.APs[0].Hull()
	for _, a := range s.APs[1:] {
		alo, ahi := a.Hull()
		if alo < lo {
			lo = alo
		}
		if ahi > hi {
			hi = ahi
		}
	}
	return lo, hi, true
}

// Contains reports membership.
func (s PSet) Contains(x int64) bool {
	for _, a := range s.APs {
		if a.Contains(x) {
			return true
		}
	}
	return false
}

// Shift translates every value and the universe bounds by delta.
func (s PSet) Shift(delta int64) PSet {
	out := PSet{Lo: addChecked(s.Lo, delta), Hi: addChecked(s.Hi, delta)}
	out.APs = make([]AP, len(s.APs))
	for i, a := range s.APs {
		out.APs[i] = a.Shift(delta)
	}
	return out
}

// Affine maps every value x to mul*x+add. mul must be non-zero. The universe
// is recomputed from the mapped hulls.
func (s PSet) Affine(mul, add int64) PSet {
	if mul == 0 {
		panic("nestedslice: affine multiplier must be non-zero")
	}
	if s.IsEmpty() {
		lo := addChecked(mulChecked(s.Lo, mul), add)
		hi := addChecked(mulChecked(s.Hi, mul), add)
		if lo > hi {
			lo, hi = hi, lo
		}
		return EmptySet(lo, hi)
	}
	mapped := make([]AP, len(s.APs))
	lo, hi := int64(0), int64(0)
	for i, a := range s.APs {
		// Preserve AP direction: a negative multiplier negates the step but
		// keeps First as the first value of the mapped progression.
		m := AP{
			First: addChecked(mulChecked(a.First, mul), add),
			Step:  mulChecked(a.Step, mul),
			Count: a.Count,
		}
		mapped[i] = m
		if i == 0 {
			lo, hi = m.Hull()
		} else {
			mlo, mhi := m.Hull()
			if mlo < lo {
				lo = mlo
			}
			if mhi > hi {
				hi = mhi
			}
		}
	}
	return PSet{Lo: lo, Hi: hi, APs: mapped}
}

// Union returns the union of the given sets, which must share universe
// semantics. Duplicate and adjacent same-step progressions are coalesced.
func Union(sets ...PSet) PSet {
	if len(sets) == 0 {
		panic("nestedslice: Union needs at least one set")
	}
	lo, hi := sets[0].Lo, sets[0].Hi
	var aps []AP
	for _, s := range sets {
		if s.Lo < lo {
			lo = s.Lo
		}
		if s.Hi > hi {
			hi = s.Hi
		}
		aps = append(aps, s.APs...)
	}
	return PSet{Lo: lo, Hi: hi, APs: normalizeAPs(aps)}
}

func normalizeAPs(in []AP) []AP {
	if len(in) == 0 {
		return nil
	}
	aps := append([]AP(nil), in...)
	sort.SliceStable(aps, func(i, j int) bool {
		li, _ := aps[i].Hull()
		lj, _ := aps[j].Hull()
		if li != lj {
			return li < lj
		}
		return aps[i].First < aps[j].First
	})
	out := aps[:0]
	for _, a := range aps {
		if len(out) == 0 {
			out = append(out, a)
			continue
		}
		last := &out[len(out)-1]
		if last.Step != a.Step || last.Step <= 0 {
			out = append(out, a)
			continue
		}
		if merged, ok := last.merge(a); ok {
			*last = merged
			continue
		}
		out = append(out, a)
	}
	// A second pass merges chains created by the first pass.
	changed := true
	for changed && len(out) > 1 {
		changed = false
		next := out[:0]
		for _, a := range out {
			if len(next) == 0 {
				next = append(next, a)
				continue
			}
			if next[len(next)-1].Step != a.Step || a.Step <= 0 {
				next = append(next, a)
				continue
			}
			if merged, ok := next[len(next)-1].merge(a); ok {
				next[len(next)-1] = merged
				changed = true
			} else {
				next = append(next, a)
			}
		}
		out = next
	}
	return out
}

// Expand materializes the set when it has at most limit values.
func (s PSet) Expand(limit int64) ([]int64, bool) {
	if s.Count() > limit {
		return nil, false
	}
	values := make([]int64, 0, s.Count())
	for _, a := range s.APs {
		for i := int64(0); i < a.Count; i++ {
			values = append(values, a.At(i))
		}
	}
	return values, true
}

// Memo applies fn to every value and returns the mapped periodic set when the
// materialized size is within limit. The result preserves the input AP order:
// each input progression becomes one output AP, so a reversed dictionary
// selection remains a negative-step progression. Otherwise it returns
// ErrIrregularTooLarge.
func (s PSet) Memo(limit int64, fn func(int64) int64) (PSet, error) {
	values, ok := s.Expand(limit)
	if !ok {
		return PSet{}, ErrIrregularTooLarge
	}
	if len(values) == 0 {
		return EmptySet(s.Lo, s.Hi), nil
	}
	aps := make([]AP, 0, len(s.APs))
	offset := int64(0)
	for _, in := range s.APs {
		mapped := make([]int64, in.Count)
		for i := int64(0); i < in.Count; i++ {
			mapped[i] = fn(values[offset+i])
		}
		offset += in.Count
		built := buildOrderedAPs(mapped)
		aps = append(aps, built...)
	}
	lo, hi, ok := (PSet{APs: aps}).Hull()
	if !ok {
		return EmptySet(s.Lo, s.Hi), nil
	}
	return PSet{Lo: lo, Hi: hi, APs: aps}, nil
}

// buildOrderedAPs turns an ordered value sequence into minimal APs without
// reordering values. Adjacent equal differences extend an AP; a direction
// change starts a new one.
func buildOrderedAPs(values []int64) []AP {
	if len(values) == 1 {
		return []AP{{First: values[0], Step: 1, Count: 1}}
	}
	var aps []AP
	start := 0
	for start < len(values) {
		if start == len(values)-1 {
			aps = append(aps, AP{First: values[start], Step: 1, Count: 1})
			start++
			continue
		}
		step := values[start+1] - values[start]
		end := start + 1
		for end+1 < len(values) && values[end+1]-values[end] == step {
			end++
		}
		count := int64(end - start + 1)
		if count == 1 {
			step = 1
		}
		aps = append(aps, AP{First: values[start], Step: step, Count: count})
		start = end + 1
	}
	return aps
}

func addChecked(a, b int64) int64 {
	sum := a + b
	if (a >= 0 && b >= 0 && sum < 0) || (a < 0 && b < 0 && sum >= 0) {
		panic("nestedslice: integer overflow")
	}
	return sum
}

func mulChecked(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	prod := a * b
	if a == -1 && b == minInt64 {
		panic("nestedslice: integer overflow")
	}
	if b == -1 && a == minInt64 {
		panic("nestedslice: integer overflow")
	}
	if prod/b != a {
		panic("nestedslice: integer overflow")
	}
	return prod
}

const minInt64 = -1 << 63
