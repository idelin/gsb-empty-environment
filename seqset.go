package takeover

import (
	"encoding/json"
	"math"
	"sort"
)

// SeqRange is an inclusive one-shot sequence interval.
type SeqRange struct {
	Start uint64 `json:"start"`
	End   uint64 `json:"end"`
}

// NewSeqRange constructs an inclusive interval.
func NewSeqRange(start, end uint64) (SeqRange, error) {
	interval := SeqRange{Start: start, End: end}
	if !interval.Valid() {
		return SeqRange{}, ErrInvalidRange
	}
	return interval, nil
}

// Range constructs an inclusive interval and panics if it is invalid.
func Range(start, end uint64) SeqRange {
	interval, err := NewSeqRange(start, end)
	if err != nil {
		panic(err)
	}
	return interval
}

// Valid reports whether Start <= End.
func (r SeqRange) Valid() bool {
	return r.Start <= r.End
}

// Contains reports whether seq is in the interval.
func (r SeqRange) Contains(seq uint64) bool {
	return r.Valid() && seq >= r.Start && seq <= r.End
}

// CountExact returns the number of values in the interval. The second result
// is false for the full uint64 domain, whose cardinality does not fit in a
// uint64.
func (r SeqRange) CountExact() (uint64, bool) {
	if !r.Valid() {
		return 0, true
	}
	if r.Start == 0 && r.End == math.MaxUint64 {
		return 0, false
	}
	return r.End - r.Start + 1, true
}

// Count returns the number of values, saturating at MaxUint64.
func (r SeqRange) Count() uint64 {
	count, ok := r.CountExact()
	if !ok {
		return math.MaxUint64
	}
	return count
}

func rangesTouch(a, b SeqRange) bool {
	if !a.Valid() || !b.Valid() {
		return false
	}
	if a.End >= b.Start && b.End >= a.Start {
		return true
	}
	if a.End < b.Start {
		return a.End != math.MaxUint64 && a.End+1 >= b.Start
	}
	return b.End != math.MaxUint64 && b.End+1 >= a.Start
}

// SeqSet is a normalized set of sequence intervals. Ranges are sorted,
// disjoint, and adjacent/touching ranges are coalesced. The representation is
// range based; operations never enumerate individual sequence values.
type SeqSet struct {
	ranges []SeqRange
}

// NewSeqSet normalizes intervals into a sequence set.
func NewSeqSet(intervals ...SeqRange) (SeqSet, error) {
	for _, interval := range intervals {
		if !interval.Valid() {
			return SeqSet{}, ErrInvalidRange
		}
	}
	if len(intervals) == 0 {
		return SeqSet{}, nil
	}
	sorted := append([]SeqRange(nil), intervals...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].End < sorted[j].End
	})
	out := make([]SeqRange, 0, len(sorted))
	for _, interval := range sorted {
		if len(out) == 0 || !rangesTouch(out[len(out)-1], interval) {
			out = append(out, interval)
			continue
		}
		last := &out[len(out)-1]
		if interval.End > last.End {
			last.End = interval.End
		}
	}
	return SeqSet{ranges: out}, nil
}

// Set constructs a sequence set and panics if any range is invalid.
func Set(intervals ...SeqRange) SeqSet {
	set, err := NewSeqSet(intervals...)
	if err != nil {
		panic(err)
	}
	return set
}

// SeqRangeSet constructs a set containing one inclusive range.
func SeqRangeSet(start, end uint64) SeqSet {
	return Set(Range(start, end))
}

// MustSeqSet is an alias for Set.
func MustSeqSet(intervals ...SeqRange) SeqSet {
	return Set(intervals...)
}

// Clone returns an independent copy.
func (s SeqSet) Clone() SeqSet {
	if len(s.ranges) == 0 {
		return SeqSet{}
	}
	return SeqSet{ranges: append([]SeqRange(nil), s.ranges...)}
}

// Ranges returns a copy of the normalized intervals.
func (s SeqSet) Ranges() []SeqRange {
	return append([]SeqRange(nil), s.ranges...)
}

// Intervals is an alias for Ranges.
func (s SeqSet) Intervals() []SeqRange {
	return s.Ranges()
}

// IsEmpty reports whether the set contains no sequence.
func (s SeqSet) IsEmpty() bool {
	return len(s.ranges) == 0
}

// Contains reports whether seq is in the set.
func (s SeqSet) Contains(seq uint64) bool {
	index := sort.Search(len(s.ranges), func(i int) bool {
		return s.ranges[i].End >= seq
	})
	return index < len(s.ranges) && s.ranges[index].Start <= seq
}

// ContainsRange reports whether every value in interval is present.
func (s SeqSet) ContainsRange(interval SeqRange) bool {
	if !interval.Valid() || s.IsEmpty() {
		return false
	}
	index := sort.Search(len(s.ranges), func(i int) bool {
		return s.ranges[i].End >= interval.Start
	})
	return index < len(s.ranges) &&
		s.ranges[index].Start <= interval.Start &&
		s.ranges[index].End >= interval.End
}

// Equal reports exact set equality.
func (s SeqSet) Equal(other SeqSet) bool {
	if len(s.ranges) != len(other.ranges) {
		return false
	}
	for i := range s.ranges {
		if s.ranges[i] != other.ranges[i] {
			return false
		}
	}
	return true
}

// SubsetOf reports whether every sequence in s is in parent.
func (s SeqSet) SubsetOf(parent SeqSet) bool {
	for _, interval := range s.ranges {
		if !parent.ContainsRange(interval) {
			return false
		}
	}
	return true
}

// IsDisjoint reports whether the sets share no sequence.
func (s SeqSet) IsDisjoint(other SeqSet) bool {
	i, j := 0, 0
	for i < len(s.ranges) && j < len(other.ranges) {
		a, b := s.ranges[i], other.ranges[j]
		if a.End < b.Start {
			i++
			continue
		}
		if b.End < a.Start {
			j++
			continue
		}
		return false
	}
	return true
}

// Union returns the union of s and other.
func (s SeqSet) Union(other SeqSet) SeqSet {
	if len(other.ranges) == 0 {
		return s.Clone()
	}
	if len(s.ranges) == 0 {
		return other.Clone()
	}
	combined := make([]SeqRange, 0, len(s.ranges)+len(other.ranges))
	combined = append(combined, s.ranges...)
	combined = append(combined, other.ranges...)
	out, _ := NewSeqSet(combined...)
	return out
}

// Intersect returns the intersection of s and other.
func (s SeqSet) Intersect(other SeqSet) SeqSet {
	i, j := 0, 0
	out := make([]SeqRange, 0)
	for i < len(s.ranges) && j < len(other.ranges) {
		a, b := s.ranges[i], other.ranges[j]
		start := a.Start
		if b.Start > start {
			start = b.Start
		}
		end := a.End
		if b.End < end {
			end = b.End
		}
		if start <= end {
			out = append(out, SeqRange{Start: start, End: end})
		}
		if a.End < b.End {
			i++
		} else {
			j++
		}
	}
	normalized, _ := NewSeqSet(out...)
	return normalized
}

// Subtract returns the sequences in s that are not in other.
func (s SeqSet) Subtract(other SeqSet) SeqSet {
	if s.IsEmpty() || other.IsEmpty() {
		return s.Clone()
	}
	out := make([]SeqRange, 0)
	j := 0
	for _, interval := range s.ranges {
		cursor := interval.Start
		exhausted := false
		for j < len(other.ranges) && other.ranges[j].End < cursor {
			j++
		}
		k := j
		for k < len(other.ranges) && other.ranges[k].Start <= interval.End {
			cut := other.ranges[k]
			if cut.Start > cursor {
				out = append(out, SeqRange{Start: cursor, End: cut.Start - 1})
			}
			if cut.End >= interval.End {
				exhausted = true
				break
			}
			cursor = cut.End + 1
			k++
		}
		if !exhausted && cursor <= interval.End {
			out = append(out, SeqRange{Start: cursor, End: interval.End})
		}
	}
	normalized, _ := NewSeqSet(out...)
	return normalized
}

// Complement returns universe minus s. The complement is computed over the
// potentially enormous uint64 sequence domain only when universe is supplied;
// no values are enumerated.
func (s SeqSet) Complement(universe SeqSet) SeqSet {
	return universe.Subtract(s)
}

// Count returns the number of sequences, saturating at MaxUint64.
func (s SeqSet) Count() uint64 {
	count, ok := s.CountExact()
	if !ok {
		return math.MaxUint64
	}
	return count
}

// CountExact returns the number of sequences and whether it fits in uint64.
func (s SeqSet) CountExact() (uint64, bool) {
	var total uint64
	for _, interval := range s.ranges {
		count, ok := interval.CountExact()
		if !ok || math.MaxUint64-total < count {
			return 0, false
		}
		total += count
	}
	return total, true
}

// Min returns the smallest sequence in the set.
func (s SeqSet) Min() (uint64, bool) {
	if s.IsEmpty() {
		return 0, false
	}
	return s.ranges[0].Start, true
}

// Max returns the largest sequence in the set.
func (s SeqSet) Max() (uint64, bool) {
	if s.IsEmpty() {
		return 0, false
	}
	return s.ranges[len(s.ranges)-1].End, true
}

// MarshalJSON encodes the normalized ranges as an array.
func (s SeqSet) MarshalJSON() ([]byte, error) {
	if len(s.ranges) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(s.ranges)
}

// UnmarshalJSON decodes and normalizes an array of ranges.
func (s *SeqSet) UnmarshalJSON(data []byte) error {
	var intervals []SeqRange
	if err := json.Unmarshal(data, &intervals); err != nil {
		return err
	}
	normalized, err := NewSeqSet(intervals...)
	if err != nil {
		return err
	}
	*s = normalized
	return nil
}
