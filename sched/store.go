package sched

import "fmt"

// EventKind distinguishes certificate issuance from revocation.
type EventKind string

const (
	// EventIssue records that a certificate is valid over its business
	// interval [Start, End).
	EventIssue EventKind = "issue"
	// EventRevoke records that a certificate is invalid over its business
	// interval [Start, End). Revocations of the same certificate combine
	// as a union.
	EventRevoke EventKind = "revoke"
)

// Event is an append-only log entry. Seq is the record (system) sequence
// number: a query as of seq S only sees events with Seq < S, so a
// retroactive event never changes the answer of an earlier query.
// Start/End describe the business validity interval in minutes.
type Event struct {
	Seq      int       `json:"seq"`
	Kind     EventKind `json:"kind"`
	CertID   string    `json:"cert"`
	Employee string    `json:"employee"`
	Qual     string    `json:"qual"`
	Start    Minutes   `json:"start"`
	End      Minutes   `json:"end"`
}

// Valid returns the business validity interval of the event.
func (e Event) Valid() Interval { return Interval{Start: e.Start, End: e.End} }

func validateEvent(ev Event) error {
	if ev.Kind != EventIssue && ev.Kind != EventRevoke {
		return fmt.Errorf("event seq %d: unknown kind %q", ev.Seq, ev.Kind)
	}
	if ev.CertID == "" || ev.Employee == "" || ev.Qual == "" {
		return fmt.Errorf("event seq %d: cert, employee and qual are required", ev.Seq)
	}
	if ev.Valid().Empty() {
		return fmt.Errorf("event seq %d: empty validity interval [%d, %d)", ev.Seq, ev.Start, ev.End)
	}
	return nil
}

// Store is an append-only event log ordered by record sequence number.
type Store struct {
	events []Event
}

// Events returns the recorded events in sequence order.
func (s *Store) Events() []Event { return s.events }

// Add appends an event. Sequence numbers must be strictly increasing.
func (s *Store) Add(ev Event) error {
	if err := validateEvent(ev); err != nil {
		return err
	}
	if n := len(s.events); n > 0 && s.events[n-1].Seq >= ev.Seq {
		return fmt.Errorf("event seq %d: sequence numbers must be strictly increasing (last is %d)", ev.Seq, s.events[n-1].Seq)
	}
	s.events = append(s.events, ev)
	return nil
}

// coverage returns the merged valid intervals for employee+qual using only
// events with Seq < asOfSeq: issued intervals minus the union of known
// revocations, merged so chained certificates are seamless.
func (s *Store) coverage(employee, qual string, asOfSeq int) []Interval {
	issued := map[string][]Interval{}
	revoked := map[string][]Interval{}
	var order []string
	for _, ev := range s.events {
		if ev.Seq >= asOfSeq {
			break
		}
		if ev.Employee != employee || ev.Qual != qual {
			continue
		}
		switch ev.Kind {
		case EventIssue:
			if _, seen := issued[ev.CertID]; !seen {
				order = append(order, ev.CertID)
			}
			issued[ev.CertID] = append(issued[ev.CertID], ev.Valid())
		case EventRevoke:
			revoked[ev.CertID] = append(revoked[ev.CertID], ev.Valid())
		}
	}
	var pieces []Interval
	for _, certID := range order {
		for _, iv := range issued[certID] {
			pieces = append(pieces, Subtract(iv, revoked[certID])...)
		}
	}
	return Merge(pieces)
}
