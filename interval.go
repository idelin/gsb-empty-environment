package replayhandoff

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Interval is a half-open interval [Lo, Hi). Hi is allowed to be 2^40.
type Interval struct {
	Lo Seq
	Hi Seq
}

// NewInterval validates and constructs a half-open interval.
func NewInterval(lo, hi Seq) (Interval, error) {
	iv := Interval{Lo: lo, Hi: hi}
	if err := iv.Validate(); err != nil {
		return Interval{}, err
	}
	return iv, nil
}

// Validate checks the 40-bit and half-open interval invariants.
func (iv Interval) Validate() error {
	if iv.Lo > SequenceLimit || iv.Hi > SequenceLimit {
		return ErrOutsideSequenceSpace
	}
	if iv.Lo > iv.Hi {
		return ErrInvertedInterval
	}
	return nil
}

// Empty reports whether the interval contains no sequence number.
func (iv Interval) Empty() bool {
	return iv.Lo == iv.Hi
}

// Len returns the cardinality of the half-open interval. It never enumerates
// members of the interval.
func (iv Interval) Len() uint64 {
	if iv.Lo >= iv.Hi {
		return 0
	}
	return uint64(iv.Hi - iv.Lo)
}

// Contains reports whether s belongs to iv.
func (iv Interval) Contains(s Seq) bool {
	return iv.Lo <= s && s < iv.Hi
}

// Intersects reports whether two half-open intervals overlap.
func (iv Interval) Intersects(other Interval) bool {
	return iv.Lo < other.Hi && other.Lo < iv.Hi
}

// Intersection returns the overlap of iv and other.
func (iv Interval) Intersection(other Interval) Interval {
	lo := iv.Lo
	if other.Lo > lo {
		lo = other.Lo
	}
	hi := iv.Hi
	if other.Hi < hi {
		hi = other.Hi
	}
	if lo > hi {
		return Interval{Lo: lo, Hi: lo}
	}
	return Interval{Lo: lo, Hi: hi}
}

// Clip returns iv restricted to within bounds.
func (iv Interval) Clip(bounds Interval) Interval {
	return iv.Intersection(bounds)
}

// String renders a canonical half-open interval.
func (iv Interval) String() string {
	return fmt.Sprintf("[%d,%d)", iv.Lo, iv.Hi)
}

// ParseInterval parses the strict form [lo,hi). Both endpoints are required.
func ParseInterval(text string) (Interval, error) {
	text = strings.TrimSpace(text)
	if len(text) < 3 || text[0] != '[' || text[len(text)-1] != ')' {
		return Interval{}, fmt.Errorf("replayhandoff: interval must use [lo,hi) syntax: %q", text)
	}
	body := text[1 : len(text)-1]
	parts := strings.Split(body, ",")
	if len(parts) != 2 {
		return Interval{}, fmt.Errorf("replayhandoff: interval must contain exactly one comma: %q", text)
	}
	if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return Interval{}, ErrMissingEndpoint
	}
	lo, err := parseSeqEndpoint(parts[0])
	if err != nil {
		return Interval{}, err
	}
	hi, err := parseSeqEndpoint(parts[1])
	if err != nil {
		return Interval{}, err
	}
	return NewInterval(lo, hi)
}

// IntervalSet is an immutable, normalized set of disjoint half-open intervals.
//
// Updating an IntervalSet never mutates the receiver. A snapshot can therefore
// be shared cheaply by many node views, and every write produces a new value
// instead of modifying the old backing slice. This is the copy-on-write
// representation used by NodeLog.
type IntervalSet struct {
	spans []Interval
}

// NewIntervalSet constructs a normalized set. Inputs may overlap, be adjacent
// or arrive in any order.
func NewIntervalSet(intervals ...Interval) (IntervalSet, error) {
	spans := make([]Interval, 0, len(intervals))
	for _, iv := range intervals {
		if err := iv.Validate(); err != nil {
			return IntervalSet{}, err
		}
		if !iv.Empty() {
			spans = append(spans, iv)
		}
	}
	return IntervalSet{spans: normalizeSpans(spans)}, nil
}

// MustIntervalSet is a convenience constructor for statically valid inputs.
// It panics on invalid intervals.
func MustIntervalSet(intervals ...Interval) IntervalSet {
	set, err := NewIntervalSet(intervals...)
	if err != nil {
		panic(err)
	}
	return set
}

func normalizeSpans(src []Interval) []Interval {
	if len(src) == 0 {
		return nil
	}
	sort.Slice(src, func(i, j int) bool {
		if src[i].Lo != src[j].Lo {
			return src[i].Lo < src[j].Lo
		}
		return src[i].Hi < src[j].Hi
	})
	out := make([]Interval, 0, len(src))
	for _, iv := range src {
		if iv.Empty() {
			continue
		}
		if len(out) == 0 {
			out = append(out, iv)
			continue
		}
		last := &out[len(out)-1]
		if iv.Lo <= last.Hi {
			if iv.Hi > last.Hi {
				last.Hi = iv.Hi
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// Empty reports whether the set has no members.
func (set IntervalSet) Empty() bool {
	return len(set.spans) == 0
}

// Count returns the number of disjoint intervals in the set, not the number of
// sequence numbers.
func (set IntervalSet) Count() int {
	return len(set.spans)
}

// Intervals returns a defensive copy of the canonical intervals.
func (set IntervalSet) Intervals() []Interval {
	if len(set.spans) == 0 {
		return nil
	}
	out := make([]Interval, len(set.spans))
	copy(out, set.spans)
	return out
}

// Clone returns another handle to the same persistent snapshot.
func (set IntervalSet) Clone() IntervalSet {
	return set
}

// Contains reports whether s is present.
func (set IntervalSet) Contains(s Seq) bool {
	if !s.Valid() {
		return false
	}
	i := sort.Search(len(set.spans), func(i int) bool {
		return set.spans[i].Hi > s
	})
	return i < len(set.spans) && set.spans[i].Contains(s)
}

// ContainsInterval reports whether every member of iv is present.
func (set IntervalSet) ContainsInterval(iv Interval) bool {
	if iv.Empty() {
		return true
	}
	for _, span := range set.spans {
		if span.Lo <= iv.Lo && span.Hi >= iv.Hi {
			return true
		}
	}
	return false
}

// Add returns a new set containing the receiver and iv.
func (set IntervalSet) Add(iv Interval) (IntervalSet, error) {
	if err := iv.Validate(); err != nil {
		return IntervalSet{}, err
	}
	if iv.Empty() {
		return set, nil
	}
	spans := make([]Interval, 0, len(set.spans)+1)
	spans = append(spans, set.spans...)
	spans = append(spans, iv)
	return IntervalSet{spans: normalizeSpans(spans)}, nil
}

// Union returns the union of two sets.
func (set IntervalSet) Union(other IntervalSet) IntervalSet {
	spans := make([]Interval, 0, len(set.spans)+len(other.spans))
	spans = append(spans, set.spans...)
	spans = append(spans, other.spans...)
	return IntervalSet{spans: normalizeSpans(spans)}
}

// Intersect returns the intersection of two sets.
func (set IntervalSet) Intersect(other IntervalSet) IntervalSet {
	i, j := 0, 0
	out := make([]Interval, 0, len(set.spans)+len(other.spans))
	for i < len(set.spans) && j < len(other.spans) {
		a, b := set.spans[i], other.spans[j]
		lo := a.Lo
		if b.Lo > lo {
			lo = b.Lo
		}
		hi := a.Hi
		if b.Hi < hi {
			hi = b.Hi
		}
		if lo < hi {
			out = append(out, Interval{Lo: lo, Hi: hi})
		}
		if a.Hi < b.Hi {
			i++
		} else if b.Hi < a.Hi {
			j++
		} else {
			i++
			j++
		}
	}
	return IntervalSet{spans: out}
}

// Subtract returns the members of set that are absent from other.
func (set IntervalSet) Subtract(other IntervalSet) IntervalSet {
	out := make([]Interval, 0, len(set.spans)+len(other.spans))
	j := 0
	for _, cur := range set.spans {
		start := cur.Lo
		for j < len(other.spans) && other.spans[j].Hi <= start {
			j++
		}
		k := j
		for k < len(other.spans) && other.spans[k].Lo < cur.Hi {
			cut := other.spans[k]
			if cut.Lo > start {
				out = append(out, Interval{Lo: start, Hi: cut.Lo})
			}
			if cut.Hi > start {
				start = cut.Hi
			}
			if start >= cur.Hi {
				break
			}
			k++
		}
		if start < cur.Hi {
			out = append(out, Interval{Lo: start, Hi: cur.Hi})
		}
	}
	return IntervalSet{spans: out}
}

// Complement returns [0,2^40) minus set.
func (set IntervalSet) Complement() IntervalSet {
	return MustIntervalSet(Interval{Lo: 0, Hi: SequenceLimit}).Subtract(set)
}

// Clip returns the set intersect bounds.
func (set IntervalSet) Clip(bounds Interval) IntervalSet {
	return set.Intersect(MustIntervalSet(bounds))
}

// GapsWithin returns the parts of bounds not covered by set.
func (set IntervalSet) GapsWithin(bounds Interval) []Interval {
	return MustIntervalSet(bounds).Subtract(set).Intervals()
}

// Equal reports set equality.
func (set IntervalSet) Equal(other IntervalSet) bool {
	if len(set.spans) != len(other.spans) {
		return false
	}
	for i := range set.spans {
		if set.spans[i] != other.spans[i] {
			return false
		}
	}
	return true
}

// String renders a compact canonical representation.
func (set IntervalSet) String() string {
	if len(set.spans) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, iv := range set.spans {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(iv.String())
	}
	b.WriteByte('}')
	return b.String()
}

// ValidateSet validates every interval and canonicality invariant.
func ValidateSet(set IntervalSet) error {
	prevHi := Seq(0)
	for i, iv := range set.spans {
		if err := iv.Validate(); err != nil {
			return err
		}
		if iv.Empty() {
			return errors.New("replayhandoff: canonical interval set contains an empty interval")
		}
		if i > 0 && iv.Lo < prevHi {
			return errors.New("replayhandoff: interval set is not normalized")
		}
		prevHi = iv.Hi
	}
	return nil
}
