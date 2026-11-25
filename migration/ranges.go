package migration

import (
	"math/big"
	"sort"
)

type halfInterval struct {
	start *big.Int
	end   *big.Int
}

func exclusive(step Step) *big.Int {
	end := new(big.Int).SetUint64(uint64(step))
	return end.Add(end, big.NewInt(1))
}

func toHalf(i Interval) halfInterval {
	return halfInterval{
		start: new(big.Int).SetUint64(uint64(i.Start)),
		end:   exclusive(i.End),
	}
}

func fromHalf(h halfInterval) Interval {
	end := new(big.Int).Sub(h.end, big.NewInt(1))
	return Interval{Start: Step(h.start.Uint64()), End: Step(end.Uint64())}
}

func copyHalf(intervals []halfInterval) []halfInterval {
	out := make([]halfInterval, len(intervals))
	for index, interval := range intervals {
		out[index] = halfInterval{
			start: new(big.Int).Set(interval.start),
			end:   new(big.Int).Set(interval.end),
		}
	}
	return out
}

func normalizeHalf(intervals []halfInterval) []halfInterval {
	if len(intervals) == 0 {
		return nil
	}
	ordered := copyHalf(intervals)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].start.Cmp(ordered[j].start) != 0 {
			return ordered[i].start.Cmp(ordered[j].start) < 0
		}
		return ordered[i].end.Cmp(ordered[j].end) < 0
	})

	result := make([]halfInterval, 0, len(ordered))
	current := ordered[0]
	for _, interval := range ordered[1:] {
		if interval.start.Cmp(current.end) <= 0 {
			if interval.end.Cmp(current.end) > 0 {
				current.end.Set(interval.end)
			}
			continue
		}
		result = append(result, current)
		current = interval
	}
	return append(result, current)
}

// NewRangeSet normalizes a union of closed intervals without expanding steps.
func NewRangeSet(intervals []Interval) RangeSet {
	if len(intervals) == 0 {
		return RangeSet{Intervals: []Interval{}}
	}
	halves := make([]halfInterval, 0, len(intervals))
	for _, interval := range intervals {
		if interval.Valid() {
			halves = append(halves, toHalf(interval))
		}
	}
	halves = normalizeHalf(halves)
	out := RangeSet{Intervals: []Interval{}}
	for _, interval := range halves {
		out.Intervals = append(out.Intervals, fromHalf(interval))
	}
	return out
}

func (r RangeSet) halves() []halfInterval {
	out := make([]halfInterval, 0, len(r.Intervals))
	for _, interval := range r.Intervals {
		out = append(out, toHalf(interval))
	}
	return normalizeHalf(out)
}

func rangeSetFromHalf(intervals []halfInterval) RangeSet {
	intervals = normalizeHalf(intervals)
	out := RangeSet{Intervals: []Interval{}}
	for _, interval := range intervals {
		out.Intervals = append(out.Intervals, fromHalf(interval))
	}
	return out
}

// IsEmpty reports whether the set contains no steps.
func (r RangeSet) IsEmpty() bool {
	return len(r.Intervals) == 0
}

// Equal reports whether two normalized range sets contain the same steps.
func (r RangeSet) Equal(other RangeSet) bool {
	if len(r.Intervals) != len(other.Intervals) {
		return false
	}
	for index := range r.Intervals {
		if r.Intervals[index] != other.Intervals[index] {
			return false
		}
	}
	return true
}

// Union returns a normalized union without mutating either operand.
func (r RangeSet) Union(other RangeSet) RangeSet {
	halves := append(r.halves(), other.halves()...)
	return rangeSetFromHalf(halves)
}

// Intersect returns the intersection of two range sets.
func (r RangeSet) Intersect(other RangeSet) RangeSet {
	left := r.halves()
	right := other.halves()
	result := make([]halfInterval, 0)
	for li, ri := 0, 0; li < len(left) && ri < len(right); {
		start := maxBig(left[li].start, right[ri].start)
		end := minBig(left[li].end, right[ri].end)
		if start.Cmp(end) < 0 {
			result = append(result, halfInterval{start: new(big.Int).Set(start), end: new(big.Int).Set(end)})
		}
		leftEnds := left[li].end.Cmp(right[ri].end) <= 0
		rightEnds := right[ri].end.Cmp(left[li].end) <= 0
		if leftEnds {
			li++
		}
		if rightEnds {
			ri++
		}
	}
	return rangeSetFromHalf(result)
}

// Subtract returns the steps in r that are not in other.
func (r RangeSet) Subtract(other RangeSet) RangeSet {
	result := make([]halfInterval, 0)
	for _, current := range r.halves() {
		pieces := []halfInterval{current}
		for _, removed := range other.halves() {
			next := make([]halfInterval, 0, len(pieces)+1)
			for _, piece := range pieces {
				if removed.end.Cmp(piece.start) <= 0 || removed.start.Cmp(piece.end) >= 0 {
					next = append(next, piece)
					continue
				}
				if removed.start.Cmp(piece.start) > 0 {
					next = append(next, halfInterval{
						start: new(big.Int).Set(piece.start),
						end:   new(big.Int).Set(removed.start),
					})
				}
				if removed.end.Cmp(piece.end) < 0 {
					next = append(next, halfInterval{
						start: new(big.Int).Set(removed.end),
						end:   new(big.Int).Set(piece.end),
					})
				}
			}
			pieces = next
		}
		result = append(result, pieces...)
	}
	return rangeSetFromHalf(result)
}

// Length returns the number of steps in the range set.
func (r RangeSet) Length() *big.Int {
	total := big.NewInt(0)
	for _, interval := range r.halves() {
		length := new(big.Int).Sub(interval.end, interval.start)
		total.Add(total, length)
	}
	return total
}

// Partition returns maximal adjacent cells whose membership is constant in r.
func (r RangeSet) Partition() []Interval {
	normalized := r.halves()
	cells := make([]Interval, 0, len(normalized))
	for _, interval := range normalized {
		cells = append(cells, fromHalf(interval))
	}
	return cells
}

// PartitionRanges returns maximal cells separated by all input range boundaries.
func PartitionRanges(ranges []RangeSet) []Interval {
	boundaries := make(map[string]*big.Int)
	for _, set := range ranges {
		for _, interval := range set.halves() {
			boundaries[interval.start.String()] = new(big.Int).Set(interval.start)
			boundaries[interval.end.String()] = new(big.Int).Set(interval.end)
		}
	}
	points := make([]*big.Int, 0, len(boundaries))
	for _, point := range boundaries {
		points = append(points, point)
	}
	sort.Slice(points, func(i, j int) bool {
		return points[i].Cmp(points[j]) < 0
	})
	if len(points) == 0 {
		return []Interval{}
	}

	cells := make([]Interval, 0, len(points)-1)
	for index := 0; index+1 < len(points); index++ {
		start := points[index]
		endExclusive := points[index+1]
		end := new(big.Int).Sub(endExclusive, big.NewInt(1))
		if start.Cmp(endExclusive) >= 0 || !start.IsUint64() || !end.IsUint64() {
			continue
		}
		interval := Interval{Start: Step(start.Uint64()), End: Step(end.Uint64())}
		if !NewRangeSet([]Interval{interval}).Intersect(unionRanges(ranges)).IsEmpty() {
			cells = append(cells, interval)
		}
	}
	return cells
}

func unionRanges(ranges []RangeSet) RangeSet {
	union := RangeSet{Intervals: []Interval{}}
	for _, set := range ranges {
		union = union.Union(set)
	}
	return union
}

func maxBig(a, b *big.Int) *big.Int {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

func minBig(a, b *big.Int) *big.Int {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

func intervalLength(interval Interval) *big.Int {
	half := toHalf(interval)
	return new(big.Int).Sub(half.end, half.start)
}
