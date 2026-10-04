package main

import (
	"fmt"
	"sort"
	"time"
)

// Solve analyses a possibly incomplete gate record and reports the minimum
// and maximum possible fee together with the insert-time windows of the
// missing event that realise them. At most one event may be inserted.
func Solve(events []Event, r Rules) Result {
	if r.Unit <= 0 {
		return Result{Status: StatusInvalid, Reason: "unit duration must be positive"}
	}
	if r.Free < 0 || r.UnitPrice < 0 || r.DailyCap < 0 {
		return Result{Status: StatusInvalid, Reason: "rule parameters must be non-negative"}
	}
	if len(events) == 0 {
		return Result{Status: StatusInvalid, Reason: "no events"}
	}
	for i, e := range events {
		if e.Type != Enter && e.Type != Exit {
			return Result{Status: StatusInvalid, Reason: fmt.Sprintf("event %d: unknown type %q", i, e.Type)}
		}
		if i > 0 && e.Time.Before(events[i-1].Time) {
			return Result{Status: StatusInvalid, Reason: "events must be sorted by time"}
		}
	}

	n := len(events)
	var defects []int
	for i := 1; i < n; i++ {
		if events[i].Type == events[i-1].Type {
			defects = append(defects, i)
		}
	}

	if n%2 == 0 {
		if len(defects) == 0 && events[0].Type == Enter {
			fee, segs := billCompleted(events, r)
			ext := &Extreme{FeeCents: &fee, Windows: []Window{}, Segments: segs}
			return Result{Status: StatusOK, Min: ext, Max: ext}
		}
		return Result{Status: StatusUnrecoverable, Reason: "even number of events but not a legal alternating enter/exit sequence; inserting one event keeps the length odd and cannot fix it"}
	}

	// Odd length: exactly one event is missing. An insertion can repair the
	// alternation only at a position where two equal events meet (or at an
	// end when the record alternates but terminates wrongly).
	var pos int
	var typ EventType
	switch {
	case len(defects) >= 2:
		return Result{Status: StatusUnrecoverable, Reason: "sequence has more than one alternation defect; one inserted event is not enough"}
	case len(defects) == 1:
		pos = defects[0]
		if events[0].Type != Enter || events[n-1].Type != Exit {
			return Result{Status: StatusUnrecoverable, Reason: "no single insertion yields a record that starts with enter and ends with exit"}
		}
		typ = other(events[pos].Type)
	default: // alternating but odd length
		if events[0].Type == Enter {
			pos, typ = n, Exit // missing final exit
		} else {
			pos, typ = 0, Enter // missing initial enter
		}
	}
	return solveWithInsertion(events, r, pos, typ)
}

// completedAt maps an index in the completed sequence back to the observed
// events; the inserted event itself carries a zero time placeholder.
func completedAt(a []Event, pos int, typ EventType, j int) Event {
	switch {
	case j < pos:
		return a[j]
	case j == pos:
		return Event{Type: typ}
	default:
		return a[j-1]
	}
}

func solveWithInsertion(a []Event, r Rules, pos int, typ EventType) Result {
	n := len(a)

	// The inserted event pairs with exactly one neighbour; every other
	// adjacent pair of the observed record is a constant segment.
	var varEnter, varExit time.Time // fixed endpoint of the varying segment
	if typ == Exit {
		varEnter = a[pos-1].Time
	} else {
		varExit = a[pos].Time
	}
	base := map[string]int64{} // pre-cap constant fees per calendar day
	for i := 0; i+1 <= n; i += 2 {
		if i == pos || i+1 == pos {
			continue
		}
		e := completedAt(a, pos, typ, i)
		x := completedAt(a, pos, typ, i+1)
		base[dayKey(e.Time)] += segmentFee(x.Time.Sub(e.Time), r)
	}
	var uncappedSum, cappedSum int64
	for _, v := range base {
		uncappedSum += v
		if r.DailyCap > 0 && v > r.DailyCap {
			cappedSum += r.DailyCap
		} else {
			cappedSum += v
		}
	}

	// Time domain of the inserted event.
	var lo, hi *time.Time
	if typ == Exit {
		t := a[pos-1].Time
		lo = &t
		if pos < n {
			t := a[pos].Time
			hi = &t
		}
	} else {
		if pos > 0 {
			t := a[pos-1].Time
			lo = &t
		}
		t := a[pos].Time
		hi = &t
	}

	inserted := &InsertInfo{Position: pos, Type: typ, After: lo, Before: hi}

	completedWith := func(t time.Time) []Event {
		out := make([]Event, 0, n+1)
		out = append(out, a[:pos]...)
		out = append(out, Event{Time: t, Type: typ})
		out = append(out, a[pos:]...)
		return out
	}

	// feeAt evaluates the total fee when the inserted event happens at t.
	feeAt := func(t time.Time) int64 {
		var d time.Duration
		var day string
		if typ == Exit {
			d = t.Sub(varEnter)
			day = dayKey(varEnter)
		} else {
			d = varExit.Sub(t)
			day = dayKey(t)
		}
		g := segmentFee(d, r)
		if r.DailyCap <= 0 {
			return uncappedSum + g
		}
		b := base[day]
		cappedB := b
		if cappedB > r.DailyCap {
			cappedB = r.DailyCap
		}
		withVar := b + g
		if withVar > r.DailyCap {
			withVar = r.DailyCap
		}
		return cappedSum - cappedB + withVar
	}

	// Degenerate rules: the varying segment never costs anything.
	if r.UnitPrice == 0 {
		fee := cappedSum
		t := varEnter
		if typ == Enter {
			t = varExit
		}
		_, segs := billCompleted(completedWith(t), r)
		ext := &Extreme{FeeCents: &fee, Windows: []Window{{From: lo, To: hi}}, Segments: segs}
		return Result{Status: StatusOK, Inserted: inserted, Min: ext, Max: ext}
	}

	// The fee as a function of the inserted time is a step function. Its
	// breakpoints are the rounding thresholds of the varying segment plus
	// (for an inserted enter, whose calendar day moves) midnight boundaries.
	var breaks []time.Time
	addBreak := func(t time.Time) {
		if lo != nil && !t.After(*lo) {
			return
		}
		if hi != nil && !t.Before(*hi) {
			return
		}
		breaks = append(breaks, t)
	}

	unboundedMax := r.DailyCap == 0 &&
		((typ == Exit && hi == nil) || (typ == Enter && lo == nil))

	if typ == Exit {
		// Rounding thresholds at t = enter + Free + m*Unit.
		var limit time.Time
		if hi != nil {
			limit = *hi
		} else if r.DailyCap > 0 {
			limit = varEnter.Add(saturationDuration(r) + r.Unit)
		} else {
			limit = varEnter.Add(r.Free + 2*r.Unit) // enough to locate the minimum
		}
		for m := int64(0); ; m++ {
			t := varEnter.Add(r.Free + time.Duration(m)*r.Unit)
			if !t.Before(limit) {
				break
			}
			addBreak(t)
		}
	} else {
		// Rounding thresholds at t = exit - Free - m*Unit.
		var limit time.Time
		if lo != nil {
			limit = *lo
		} else if r.DailyCap > 0 {
			limit = varExit.Add(-saturationDuration(r) - r.Unit)
		} else {
			limit = varExit.Add(-r.Free - 2*r.Unit)
		}
		for m := int64(0); ; m++ {
			t := varExit.Add(-(r.Free + time.Duration(m)*r.Unit))
			if !t.After(limit) {
				break
			}
			addBreak(t)
		}
		if r.DailyCap > 0 {
			// Midnight boundaries: the inserted enter's calendar day (and
			// hence the day the segment is billed to) changes there.
			loBound := limit
			if lo != nil {
				loBound = *lo
			}
			if lo == nil {
				// Clamp the open side at a midnight so the outermost cell
				// does not straddle two days.
				midnight := startOfDay(varExit.Add(-saturationDuration(r)))
				breaks = append(breaks, midnight)
				loBound = midnight
			}
			for cur := startOfDay(loBound).AddDate(0, 0, 1); hi == nil || cur.Before(*hi); cur = cur.AddDate(0, 0, 1) {
				if hi != nil && !cur.Before(*hi) {
					break
				}
				addBreak(cur)
				if hi == nil { // unreachable for inserted enter, kept for safety
					break
				}
			}
		}
	}

	sort.Slice(breaks, func(i, j int) bool { return breaks[i].Before(breaks[j]) })
	breaks = dedupeTimes(breaks)

	// Build cells between consecutive bounds and evaluate the fee once per
	// cell (the fee is constant inside a cell by construction).
	bounds := make([]*time.Time, 0, len(breaks)+2)
	bounds = append(bounds, lo)
	for i := range breaks {
		bounds = append(bounds, &breaks[i])
	}
	bounds = append(bounds, hi)

	type cell struct {
		lo, hi *time.Time
		sample time.Time
		fee    int64
	}
	cells := make([]cell, 0, len(bounds)-1)
	for i := 0; i+1 < len(bounds); i++ {
		c := cell{lo: bounds[i], hi: bounds[i+1]}
		switch {
		case c.lo == nil:
			c.sample = c.hi.Add(-r.Unit)
		case c.hi == nil:
			c.sample = c.lo.Add(r.Unit)
		default:
			c.sample = c.lo.Add(c.hi.Sub(*c.lo) / 2)
		}
		c.fee = feeAt(c.sample)
		cells = append(cells, c)
	}

	// Merge adjacent cells with equal fees into windows.
	type group struct {
		from, to *time.Time
		sample   time.Time
		fee      int64
	}
	var groups []group
	for _, c := range cells {
		if len(groups) > 0 && groups[len(groups)-1].fee == c.fee {
			groups[len(groups)-1].to = c.hi
			continue
		}
		groups = append(groups, group{from: c.lo, to: c.hi, sample: c.sample, fee: c.fee})
	}

	minFee, maxFee := cells[0].fee, cells[0].fee
	for _, c := range cells {
		if c.fee < minFee {
			minFee = c.fee
		}
		if c.fee > maxFee {
			maxFee = c.fee
		}
	}

	collect := func(want int64) ([]Window, time.Time) {
		var ws []Window
		var sample time.Time
		for _, g := range groups {
			if g.fee == want {
				ws = append(ws, Window{From: g.from, To: g.to})
				if sample.IsZero() {
					sample = g.sample
				}
			}
		}
		return ws, sample
	}

	minWindows, minSample := collect(minFee)
	_, minSegs := billCompleted(completedWith(minSample), r)
	minExt := &Extreme{FeeCents: &minFee, Windows: minWindows, Segments: minSegs}

	var maxExt *Extreme
	status := StatusOK
	switch {
	case unboundedMax:
		// With no daily cap and an open-ended insert domain the fee grows
		// without bound as the segment lengthens.
		maxExt = &Extreme{
			FeeCents: nil,
			Windows:  []Window{{From: lo, To: hi}},
			Segments: minSegs,
		}
		status = StatusUnbounded
	default:
		maxWindows, maxSample := collect(maxFee)
		_, maxSegs := billCompleted(completedWith(maxSample), r)
		maxExt = &Extreme{FeeCents: &maxFee, Windows: maxWindows, Segments: maxSegs}
		if maxFee != minFee {
			status = StatusAmbiguous
		}
	}

	return Result{Status: status, Inserted: inserted, Min: minExt, Max: maxExt}
}

// saturationDuration returns a segment duration whose fee alone reaches the
// daily cap, used to bound breakpoint generation on open-ended domains.
func saturationDuration(r Rules) time.Duration {
	units := r.DailyCap/r.UnitPrice + 2
	return r.Free + time.Duration(units)*r.Unit
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func dedupeTimes(ts []time.Time) []time.Time {
	out := ts[:0]
	for i, t := range ts {
		if i > 0 && t.Equal(ts[i-1]) {
			continue
		}
		out = append(out, t)
	}
	return out
}
