package railsim

import (
	"fmt"
	"sort"
)

// TrainID identifies a train.
type TrainID string

// SegmentID identifies a track segment.
type SegmentID string

// Occupancy is one reserved half-open interval on a segment.
type Occupancy struct {
	Train TrainID
	Iv    Interval
}

// ConflictError describes a rejected reservation.
type ConflictError struct {
	Train   TrainID
	Segment SegmentID
	Iv      Interval
	Other   TrainID
	OtherIv Interval
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("railsim: %s %v on segment %s conflicts with %s %v",
		e.Train, e.Iv, e.Segment, e.Other, e.OtherIv)
}

// Table holds every committed segment reservation. Reservations on one
// segment are kept sorted by start time.
type Table struct {
	segs map[SegmentID][]Occupancy
}

// NewTable returns an empty reservation table.
func NewTable() *Table { return &Table{segs: make(map[SegmentID][]Occupancy)} }

// insertSorted keeps a segment's occupancy list ordered by start time.
func insertSorted(list []Occupancy, o Occupancy) []Occupancy {
	i := sort.Search(len(list), func(i int) bool { return list[i].Iv.Start >= o.Iv.Start })
	list = append(list, Occupancy{})
	copy(list[i+1:], list[i:])
	list[i] = o
	return list
}

// Reserve atomically places every (segment, interval) pair of one train's
// path. If any interval conflicts with an existing reservation of another
// train — or with another interval of the same request on a revisited
// segment — nothing is reserved and a *ConflictError is returned.
func (t *Table) Reserve(train TrainID, segs []SegmentID, ivs []Interval) error {
	if len(segs) != len(ivs) {
		return fmt.Errorf("railsim: %d segments but %d intervals", len(segs), len(ivs))
	}
	for i := range segs {
		if !ivs[i].Valid() {
			return fmt.Errorf("railsim: invalid interval %v for segment %s", ivs[i], segs[i])
		}
		for j := i + 1; j < len(segs); j++ {
			if segs[i] == segs[j] && ivs[i].Overlaps(ivs[j]) {
				return &ConflictError{Train: train, Segment: segs[i], Iv: ivs[i],
					Other: train, OtherIv: ivs[j]}
			}
		}
	}
	for i, seg := range segs {
		for _, o := range t.segs[seg] {
			if o.Train == train {
				continue
			}
			if o.Iv.Overlaps(ivs[i]) {
				return &ConflictError{Train: train, Segment: seg, Iv: ivs[i],
					Other: o.Train, OtherIv: o.Iv}
			}
		}
	}
	for i, seg := range segs {
		t.segs[seg] = insertSorted(t.segs[seg], Occupancy{Train: train, Iv: ivs[i]})
	}
	return nil
}

// Cancel removes every reservation of a train and returns how many were
// removed. Freed ticks become reservable immediately, including the exact
// release tick (half-open semantics).
func (t *Table) Cancel(train TrainID) int {
	removed := 0
	for seg, list := range t.segs {
		kept := list[:0]
		for _, o := range list {
			if o.Train == train {
				removed++
			} else {
				kept = append(kept, o)
			}
		}
		if len(kept) == 0 {
			delete(t.segs, seg)
		} else {
			t.segs[seg] = kept
		}
	}
	return removed
}

// All returns a deep copy of every reservation, keyed by segment.
func (t *Table) All() map[SegmentID][]Occupancy {
	out := make(map[SegmentID][]Occupancy, len(t.segs))
	for seg, list := range t.segs {
		cp := make([]Occupancy, len(list))
		copy(cp, list)
		out[seg] = cp
	}
	return out
}

// Verify interval-scans every segment and reports the first overlap.
func (t *Table) Verify() error { return Scan(t.All()) }

// earliestGap returns the earliest start >= ready at which a duration-dur
// occupancy fits into the sorted occupancy list without overlap.
func earliestGap(list []Occupancy, ready, dur int) int {
	e := ready
	for _, o := range list {
		if o.Iv.End <= e {
			continue
		}
		if o.Iv.Start >= e+dur {
			break
		}
		e = o.Iv.End
	}
	return e
}

// SchedulePath computes the earliest feasible half-open occupancies for a
// train travelling route with per-segment durations, entering the first
// segment no earlier than ready and waiting at stations between segments
// as needed, then reserves the whole path atomically. If any segment
// cannot fit before horizon, nothing is reserved.
func (t *Table) SchedulePath(train TrainID, route []SegmentID, dur []int, ready, horizon int) ([]Interval, error) {
	if len(route) == 0 || len(route) != len(dur) {
		return nil, fmt.Errorf("railsim: bad route/duration pair (%d vs %d)", len(route), len(dur))
	}
	ivs := make([]Interval, len(route))
	at := ready
	for i, seg := range route {
		if dur[i] <= 0 {
			return nil, fmt.Errorf("railsim: non-positive duration %d on segment %s", dur[i], seg)
		}
		at = earliestGap(t.segs[seg], at, dur[i])
		if at+dur[i] > horizon {
			return nil, fmt.Errorf("railsim: train %s cannot fit segment %s before horizon %d",
				train, seg, horizon)
		}
		ivs[i] = Interval{Start: at, End: at + dur[i]}
		at += dur[i]
	}
	if err := t.Reserve(train, route, ivs); err != nil {
		return nil, err
	}
	return ivs, nil
}
