package sched

// Gap is one minimal uncovered interval of a required qualification
// within a shift.
type Gap struct {
	Qualification string `json:"qualification"`
	Start         int64  `json:"start"`
	End           int64  `json:"end"`
}

// ShiftResult is the per-shift verdict at a given knowledge horizon.
type ShiftResult struct {
	ShiftID     string `json:"shift_id"`
	Schedulable bool   `json:"schedulable"`
	Gaps        []Gap  `json:"gaps,omitempty"`
}

// Evaluate checks every shift at the given horizon: for each qualification
// required by the shift's position, the employee's coverage known before
// the horizon must cover the whole shift. Each required qualification is
// checked independently; multiple certificates may chain seamlessly
// (touching boundaries do not create gaps).
func (s *Store) Evaluate(horizon int) []ShiftResult {
	results := make([]ShiftResult, 0, len(s.shifts))
	for _, sh := range s.shifts {
		results = append(results, s.evaluateShift(sh, horizon))
	}
	return results
}

func (s *Store) evaluateShift(sh Shift, horizon int) ShiftResult {
	res := ShiftResult{ShiftID: sh.ID, Schedulable: true}
	pos, ok := s.positions[sh.Position]
	if !ok {
		res.Schedulable = false
		res.Gaps = append(res.Gaps, Gap{
			Qualification: "<unknown position " + sh.Position + ">",
			Start:         sh.Start,
			End:           sh.End,
		})
		return res
	}
	for _, qual := range pos.Required {
		cover := s.Coverage(sh.Employee, qual, horizon)
		for _, g := range Gaps(sh.Start, sh.End, cover) {
			res.Schedulable = false
			res.Gaps = append(res.Gaps, Gap{Qualification: qual, Start: g.Start, End: g.End})
		}
	}
	return res
}

// AffectedShifts returns the IDs of shifts that are not schedulable at the
// given horizon, in input order.
func (s *Store) AffectedShifts(horizon int) []string {
	var out []string
	for _, r := range s.Evaluate(horizon) {
		if !r.Schedulable {
			out = append(out, r.ShiftID)
		}
	}
	return out
}

// Recheck returns the shift results that a newly recorded event could
// change. Only the employee's shifts intersecting the event's business
// interval are re-evaluated, located through the interval index.
func (s *Store) Recheck(ev Event, horizon int) []ShiftResult {
	var out []ShiftResult
	for _, si := range s.CandidateShifts(ev.Employee, ev.Start, ev.End) {
		out = append(out, s.evaluateShift(s.shifts[si], horizon))
	}
	return out
}

// RecheckFullScan is the reference implementation of Recheck that ignores
// the index and scans all shifts.
func (s *Store) RecheckFullScan(ev Event, horizon int) []ShiftResult {
	var out []ShiftResult
	for _, si := range s.CandidateShiftsFullScan(ev.Employee, ev.Start, ev.End) {
		out = append(out, s.evaluateShift(s.shifts[si], horizon))
	}
	return out
}
