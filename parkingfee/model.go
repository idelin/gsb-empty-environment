package main

import "time"

// EventType is either an entry ("enter") or an exit ("exit") gate event.
type EventType string

const (
	Enter EventType = "enter"
	Exit  EventType = "exit"
)

func other(t EventType) EventType {
	if t == Enter {
		return Exit
	}
	return Enter
}

// Event is a single gate observation.
type Event struct {
	Time time.Time
	Type EventType
}

// Rules describes the billing policy. All fields are inputs.
type Rules struct {
	Free      time.Duration // free duration per parking segment
	Unit      time.Duration // billing unit; each segment rounds up to whole units
	UnitPrice int64         // price per billing unit, in cents
	DailyCap  int64         // per-calendar-day cap in cents; 0 means no cap
}

// Window is a time interval for the inserted event. A nil bound means the
// interval is open on that side.
type Window struct {
	From *time.Time `json:"from"`
	To   *time.Time `json:"to"`
}

// Segment is one recovered parking stay with its pre-cap fee.
type Segment struct {
	Enter    time.Time `json:"enter"`
	Exit     time.Time `json:"exit"`
	FeeCents int64     `json:"fee_cents"`
}

// Extreme describes one bound (minimum or maximum) of the possible fee.
type Extreme struct {
	// FeeCents is nil when the fee is unbounded on this side.
	FeeCents *int64 `json:"fee_cents"`
	// InsertWindows are the time intervals of the inserted event under
	// which this extreme fee is realised.
	Windows []Window `json:"insert_windows"`
	// Segments is one representative recovered record realising the fee.
	Segments []Segment `json:"segments"`
}

// InsertInfo describes where the missing event must be inserted.
type InsertInfo struct {
	Position int        `json:"position"` // index in the completed event sequence
	Type     EventType  `json:"type"`
	After    *time.Time `json:"after,omitempty"`  // previous observed event, if any
	Before   *time.Time `json:"before,omitempty"` // next observed event, if any
}

// Result statuses.
const (
	StatusOK            = "ok"            // fee uniquely determined
	StatusAmbiguous     = "ambiguous"     // several fees possible; see min/max
	StatusUnbounded     = "unbounded"     // fee has no upper bound
	StatusUnrecoverable = "unrecoverable" // cannot be fixed with <= 1 event
	StatusInvalid       = "invalid"       // malformed input
)

// Result is the tool output. It always reports both bounds and never
// collapses an ambiguous record into a single guessed bill.
type Result struct {
	Status   string      `json:"status"`
	Reason   string      `json:"reason,omitempty"`
	Inserted *InsertInfo `json:"inserted_event,omitempty"`
	Min      *Extreme    `json:"min,omitempty"`
	Max      *Extreme    `json:"max,omitempty"`
}

// dayKey identifies the calendar day of t in its own location.
func dayKey(t time.Time) string { return t.Format("2006-01-02") }

// segmentFee bills one segment: free duration is deducted, the remainder is
// rounded up to whole billing units.
func segmentFee(d time.Duration, r Rules) int64 {
	if r.UnitPrice <= 0 || r.Unit <= 0 {
		return 0
	}
	billable := d - r.Free
	if billable <= 0 {
		return 0
	}
	units := (billable + r.Unit - 1) / r.Unit
	return int64(units) * r.UnitPrice
}

// billCompleted fees a complete, legal event sequence. The total applies the
// daily cap per calendar day of each segment's entry time.
func billCompleted(events []Event, r Rules) (int64, []Segment) {
	dayFees := map[string]int64{}
	order := []string{}
	var segs []Segment
	for i := 0; i+1 < len(events); i += 2 {
		d := events[i+1].Time.Sub(events[i].Time)
		f := segmentFee(d, r)
		k := dayKey(events[i].Time)
		if _, ok := dayFees[k]; !ok {
			order = append(order, k)
		}
		dayFees[k] += f
		segs = append(segs, Segment{Enter: events[i].Time, Exit: events[i+1].Time, FeeCents: f})
	}
	var total int64
	for _, k := range order {
		v := dayFees[k]
		if r.DailyCap > 0 && v > r.DailyCap {
			v = r.DailyCap
		}
		total += v
	}
	return total, segs
}
