package sched

import "sort"

// Kind classifies a recorded event.
type Kind int

const (
	// Issue records that a certificate is valid for its business interval.
	Issue Kind = iota
	// Revoke records that coverage is withdrawn for its business interval.
	Revoke
)

// Event is one recorded fact. Seq is the record (entry) sequence number:
// a query at horizon H may only use events with Seq < H. The business
// interval [Start, End) is when the fact is effective in the real world,
// which may lie in the past (retroactive issue/revocation).
type Event struct {
	Seq           int    `json:"seq"`
	Kind          Kind   `json:"kind"`
	Employee      string `json:"employee"`
	Qualification string `json:"qualification"`
	Start         int64  `json:"start"`
	End           int64  `json:"end"`
}

// Shift is one schedulable unit of work.
type Shift struct {
	ID       string `json:"id"`
	Employee string `json:"employee"`
	Position string `json:"position"`
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
}

// Position lists the qualifications its shifts require.
type Position struct {
	ID       string   `json:"id"`
	Required []string `json:"required"`
}

type qualKey struct {
	employee      string
	qualification string
}

// Store holds the domain data plus a certificate-to-shift interval index.
//
// Index layout:
//   - eventsByKey: (employee, qualification) -> event indices, so coverage
//     for one qualification never scans unrelated events.
//   - shiftsByEmp: employee -> shift indices sorted by start time, so the
//     shifts intersecting a certificate interval are found by binary search
//     instead of a full scan.
type Store struct {
	positions   map[string]Position
	shifts      []Shift
	events      []Event
	eventsByKey map[qualKey][]int
	shiftsByEmp map[string][]int
}

// NewStore builds a Store and its indexes from the given data.
// Events are re-numbered with record sequence 1..n in the order given.
func NewStore(positions []Position, shifts []Shift, events []Event) *Store {
	s := &Store{
		positions:   make(map[string]Position, len(positions)),
		eventsByKey: make(map[qualKey][]int),
		shiftsByEmp: make(map[string][]int),
	}
	for _, p := range positions {
		s.positions[p.ID] = p
	}
	for i, sh := range shifts {
		s.shifts = append(s.shifts, sh)
		s.shiftsByEmp[sh.Employee] = append(s.shiftsByEmp[sh.Employee], i)
	}
	for _, idxs := range s.shiftsByEmp {
		sort.Slice(idxs, func(a, b int) bool {
			if s.shifts[idxs[a]].Start != s.shifts[idxs[b]].Start {
				return s.shifts[idxs[a]].Start < s.shifts[idxs[b]].Start
			}
			return s.shifts[idxs[a]].End < s.shifts[idxs[b]].End
		})
	}
	for _, ev := range events {
		s.AddEvent(ev)
	}
	return s
}

// AddEvent records an event, assigning it the next sequence number, and
// updates the (employee, qualification) event index.
func (s *Store) AddEvent(ev Event) int {
	ev.Seq = len(s.events) + 1
	s.events = append(s.events, ev)
	idx := len(s.events) - 1
	k := qualKey{ev.Employee, ev.Qualification}
	s.eventsByKey[k] = append(s.eventsByKey[k], idx)
	return ev.Seq
}

// Positions returns the position definition, and whether it exists.
func (s *Store) Position(id string) (Position, bool) {
	p, ok := s.positions[id]
	return p, ok
}

// Shifts returns all shifts in input order.
func (s *Store) Shifts() []Shift { return s.shifts }

// Coverage returns the merged valid intervals for (employee, qualification)
// as known strictly before record sequence horizon: issued business
// intervals minus revoked business intervals.
func (s *Store) Coverage(employee, qualification string, horizon int) []Interval {
	var issued, revoked []Interval
	for _, idx := range s.eventsByKey[qualKey{employee, qualification}] {
		ev := s.events[idx]
		if ev.Seq >= horizon {
			continue // not yet known at this horizon
		}
		iv := Interval{ev.Start, ev.End}
		if ev.Kind == Issue {
			issued = append(issued, iv)
		} else {
			revoked = append(revoked, iv)
		}
	}
	return Subtract(MergeUnion(issued), MergeUnion(revoked))
}

// CandidateShifts returns the indices of the employee's shifts that
// intersect [start, end), using the per-employee interval index
// (binary search over shifts sorted by start).
func (s *Store) CandidateShifts(employee string, start, end int64) []int {
	idxs := s.shiftsByEmp[employee]
	// First shift with Start < end; anything at or after cut starts too late.
	cut := sort.Search(len(idxs), func(i int) bool {
		return s.shifts[idxs[i]].Start >= end
	})
	var out []int
	for _, si := range idxs[:cut] {
		if s.shifts[si].End > start {
			out = append(out, si)
		}
	}
	sort.Ints(out)
	return out
}

// CandidateShiftsFullScan is the reference implementation that ignores the
// index and scans every shift. It must agree with CandidateShifts.
func (s *Store) CandidateShiftsFullScan(employee string, start, end int64) []int {
	var out []int
	for i, sh := range s.shifts {
		if sh.Employee == employee && sh.Start < end && sh.End > start {
			out = append(out, i)
		}
	}
	sort.Ints(out)
	return out
}
