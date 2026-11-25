package migration

import (
	"math/big"
	"sort"
)

func intervalsHaveEqualLength(a, b Interval) bool {
	return intervalLength(a).Cmp(intervalLength(b)) == 0
}

func mapOldInterval(segment Segment, old Interval) (Interval, bool) {
	if !old.Valid() || !segment.Old.Contains(old.Start) || !segment.Old.Contains(old.End) {
		return Interval{}, false
	}

	oldBase := new(big.Int).SetUint64(uint64(segment.Old.Start))
	newBase := new(big.Int).SetUint64(uint64(segment.New.Start))
	newTop := new(big.Int).SetUint64(uint64(segment.New.End))
	oldStart := new(big.Int).SetUint64(uint64(old.Start))
	oldEnd := new(big.Int).SetUint64(uint64(old.End))

	start := new(big.Int)
	end := new(big.Int)
	switch segment.Orientation {
	case Forward:
		offset := new(big.Int).Sub(oldStart, oldBase)
		start.Add(newBase, offset)
		end.Add(newBase, new(big.Int).Sub(oldEnd, oldBase))
	case Reverse:
		start.Sub(newTop, new(big.Int).Sub(oldEnd, oldBase))
		end.Sub(newTop, new(big.Int).Sub(oldStart, oldBase))
	default:
		return Interval{}, false
	}
	if !start.IsUint64() || !end.IsUint64() || start.Cmp(end) > 0 {
		return Interval{}, false
	}
	return Interval{Start: Step(start.Uint64()), End: Step(end.Uint64())}, true
}

func mapNewInterval(segment Segment, next Interval) (Interval, bool) {
	if !next.Valid() || !segment.New.Contains(next.Start) || !segment.New.Contains(next.End) {
		return Interval{}, false
	}

	oldBase := new(big.Int).SetUint64(uint64(segment.Old.Start))
	oldTop := new(big.Int).SetUint64(uint64(segment.Old.End))
	newBase := new(big.Int).SetUint64(uint64(segment.New.Start))
	newStart := new(big.Int).SetUint64(uint64(next.Start))
	newEnd := new(big.Int).SetUint64(uint64(next.End))

	start := new(big.Int)
	end := new(big.Int)
	switch segment.Orientation {
	case Forward:
		offset := new(big.Int).Sub(newStart, newBase)
		start.Add(oldBase, offset)
		end.Add(oldBase, new(big.Int).Sub(newEnd, newBase))
	case Reverse:
		start.Sub(oldTop, new(big.Int).Sub(newEnd, newBase))
		end.Sub(oldTop, new(big.Int).Sub(newStart, newBase))
	default:
		return Interval{}, false
	}
	if !start.IsUint64() || !end.IsUint64() || start.Cmp(end) > 0 {
		return Interval{}, false
	}
	return Interval{Start: Step(start.Uint64()), End: Step(end.Uint64())}, true
}

func mapOldRange(segment Segment, old RangeSet) RangeSet {
	mapped := make([]Interval, 0, len(old.Intervals))
	for _, interval := range old.Intervals {
		if next, ok := mapOldInterval(segment, interval); ok {
			mapped = append(mapped, next)
		}
	}
	return NewRangeSet(mapped)
}

type usableMapping struct {
	claims    []MappingClaim
	coverage  RangeSet
	conflicts RangeSet
	blocked   RangeSet
	rejected  map[string]bool
}

func buildUsableMapping(input Input, active map[string]Segment, findings *[]Finding) usableMapping {
	activeIDs := make([]string, 0, len(active))
	for id := range active {
		activeIDs = append(activeIDs, id)
	}
	sort.Strings(activeIDs)

	newRanges := make([]RangeSet, 0, len(activeIDs))
	conflictInput := make([]Interval, 0)
	for _, id := range activeIDs {
		segment := active[id]
		set := NewRangeSet([]Interval{segment.New})
		newRanges = append(newRanges, set)
		conflictInput = append(conflictInput, segment.New)
	}

	conflicts := RangeSet{}
	for i := 0; i < len(newRanges); i++ {
		for j := i + 1; j < len(newRanges); j++ {
			conflicts = conflicts.Union(newRanges[i].Intersect(newRanges[j]))
		}
	}
	if !conflicts.IsEmpty() {
		*findings = append(*findings, Finding{
			Code:     "mapping_conflict",
			Severity: Blocking,
			Message:  "active migration segments occupy the same new-device step",
			Interval: intervalPointer(conflicts.Intervals[0]),
		})
	}

	domain := NewRangeSet([]Interval{input.Domain})
	outsideDomain := NewRangeSet(conflictInput).Subtract(domain)
	if !outsideDomain.IsEmpty() {
		*findings = append(*findings, Finding{
			Code:     "segment_outside_domain",
			Severity: Blocking,
			Message:  "a signed segment maps outside the requested new-device domain",
			Interval: intervalPointer(outsideDomain.Intervals[0]),
		})
	}

	blocked := conflicts
	rejected := make(map[string]bool)
	for _, id := range activeIDs {
		segment := active[id]
		if !segment.Old.Valid() || !segment.New.Valid() ||
			(segment.Orientation != Forward && segment.Orientation != Reverse) ||
			!intervalsHaveEqualLength(segment.Old, segment.New) {
			rejected[id] = true
			blocked = blocked.Union(NewRangeSet([]Interval{segment.New}))
		}
	}
	claims := make([]MappingClaim, 0)
	for _, id := range activeIDs {
		segment := active[id]
		if rejected[id] ||
			!domain.Intersect(NewRangeSet([]Interval{segment.New})).Equal(NewRangeSet([]Interval{segment.New})) {
			rejected[id] = true
			blocked = blocked.Union(NewRangeSet([]Interval{segment.New}))
			continue
		}

		usableNew := NewRangeSet([]Interval{segment.New}).Subtract(conflicts)
		for _, next := range usableNew.Intervals {
			old, ok := mapNewInterval(segment, next)
			if !ok {
				rejected[id] = true
				blocked = blocked.Union(NewRangeSet([]Interval{segment.New}))
				break
			}
			claims = append(claims, MappingClaim{
				SegmentID:   segment.ID,
				OldDeviceID: segment.OldDeviceID,
				Old:         old,
				New:         next,
				Orientation: segment.Orientation,
			})
		}
	}

	sort.Slice(claims, func(i, j int) bool {
		if claims[i].New.Start != claims[j].New.Start {
			return claims[i].New.Start < claims[j].New.Start
		}
		return claims[i].New.End < claims[j].New.End
	})

	coverage := RangeSet{}
	for _, claim := range claims {
		coverage = coverage.Union(NewRangeSet([]Interval{claim.New}))
	}
	return usableMapping{
		claims:    claims,
		coverage:  coverage,
		conflicts: conflicts,
		blocked:   blocked.Intersect(domain),
		rejected:  rejected,
	}
}

func intervalPointer(interval Interval) *Interval {
	copyInterval := interval
	return &copyInterval
}
