package retrysys

import (
	"fmt"
	"strings"
)

// AttemptID uniquely identifies one retry attempt with a human-readable path:
//
//	C1                     - top-level call number 1
//	C1.P2                  - proxy attempt 2 of call 1
//	C1.P2.S1               - service attempt 1 under that proxy attempt
//	C1.P2.S1.R(replica-0)  - physical request to a replica under it
//
// Every logical attempt and every physical request gets a distinct id, so
// cancellation, replies, and rejections can all be attributed in the trace.
type AttemptID struct {
	Call    int
	Proxy   int // 0 at/under the service layer entry, >0 for a proxy attempt
	Service int // 0 for the proxy-level id itself, >0 for a service attempt
	Replica string
}

// String renders the hierarchical attempt path.
func (a AttemptID) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "C%d", a.Call)
	if a.Proxy > 0 {
		fmt.Fprintf(&b, ".P%d", a.Proxy)
	}
	if a.Service > 0 {
		fmt.Fprintf(&b, ".S%d", a.Service)
	}
	if a.Replica != "" {
		fmt.Fprintf(&b, ".R(%s)", a.Replica)
	}
	return b.String()
}

// Kind classifies a ledger event.
type Kind string

const (
	KindAttempt  Kind = "attempt"  // a logical retry attempt started
	KindSend     Kind = "send"     // a physical replica request was sent
	KindReject   Kind = "reject"   // a planned send was refused by budget/deadline
	KindReply    Kind = "reply"    // a replica reply arrived
	KindCancel   Kind = "cancel"   // an in-flight attempt was cancelled
	KindTimeout  Kind = "timeout"  // an attempt timer fired
	KindExpire   Kind = "expire"   // the shared deadline expired
	KindTerminal Kind = "terminal" // a layer reached its terminal result
)

// Why explains why an attempt or physical request ended or was refused.
type Why string

const (
	WhyOK            Why = "ok"
	WhyReplicaFail   Why = "replica-failure"
	WhyAttemptBudget Why = "attempt-budget-exhausted"
	WhySharedBudget  Why = "shared-budget-exhausted"
	WhyDeadline      Why = "deadline-reached"
	WhyTimeout       Why = "attempt-timeout"
	WhyCancelled     Why = "cancelled"
	WhyLate          Why = "late-after-terminal"
	WhyExpired       Why = "deadline-expired"
)

// Event is one immutable entry in the audit ledger.
type Event struct {
	Tick int
	Kind Kind
	ID   AttemptID
	Why  Why
	Note string
}

func (e Event) String() string {
	s := fmt.Sprintf("t=%-3d %-8s %s", e.Tick, e.Kind, e.ID)
	if e.Why != "" {
		s += " " + string(e.Why)
	}
	if e.Note != "" {
		s += " " + e.Note
	}
	return s
}

// Ledger records everything the retry layers observe. It is the single source
// of truth used by the tests (and the safety checker) to count actual
// requests and reconstruct terminal states.
type Ledger struct {
	events []Event
}

// Record appends an event.
func (l *Ledger) Record(tick int, kind Kind, id AttemptID, why Why, note string) {
	l.events = append(l.events, Event{Tick: tick, Kind: kind, ID: id, Why: why, Note: note})
}

// Events returns a copy of the recorded events.
func (l *Ledger) Events() []Event {
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}

// PhysicalSends counts the replica requests that actually left a retry layer
// (rejections and retries that never got budget are not counted).
func (l *Ledger) PhysicalSends(call int) int {
	n := 0
	for _, e := range l.events {
		if e.Kind == KindSend && e.ID.Call == call {
			n++
		}
	}
	return n
}

// FirstTerminal returns the first terminal event for the given call and layer
// ("proxy" or "service"), plus whether one exists. The first terminal event
// is the final state by definition: later events must never change it.
func (l *Ledger) FirstTerminal(call int, layer string) (Event, bool) {
	for _, e := range l.events {
		if e.Kind == KindTerminal && e.ID.Call == call && e.Note == layer {
			return e, true
		}
	}
	return Event{}, false
}

// terminalKey scopes a terminal event. The lower layer terminates once per
// upper attempt, so a service terminal is identified by (call, upper
// attempt); the proxy terminates exactly once per call.
func terminalKey(e Event) string {
	if e.Note == "service" {
		return fmt.Sprintf("%d/service/P%d", e.ID.Call, e.ID.Proxy)
	}
	return fmt.Sprintf("%d/%s", e.ID.Call, e.Note)
}

// Check validates the safety invariants directly from the ledger and returns
// a human-readable violation. Returning nil means all invariants hold.
//
//	(S1) no physical send at or after the deadline;
//	(S2) physical sends never exceed the shared request budget;
//	(S3) once a layer is terminal, it neither sends nor terminates again
//	     (late failures after success cannot change the terminal state).
func (l *Ledger) Check(deadline, maxRequests int) error {
	terminals := map[string]int{} // terminalKey -> terminal tick
	sends := map[int]int{}
	maxCall := 0
	for _, e := range l.events {
		if e.ID.Call > maxCall {
			maxCall = e.ID.Call
		}
		if e.Kind == KindTerminal {
			key := terminalKey(e)
			if tt, ok := terminals[key]; ok {
				return fmt.Errorf("S3 violated: %s terminal twice at t=%d and t=%d (%s)",
					key, tt, e.Tick, e.Why)
			}
			terminals[key] = e.Tick
		}
		if e.Kind == KindSend {
			if e.Tick >= deadline {
				return fmt.Errorf("S1 violated: send %s at t=%d >= deadline t=%d",
					e.ID, e.Tick, deadline)
			}
			sends[e.ID.Call]++
			proxyKey := fmt.Sprintf("%d/proxy", e.ID.Call)
			serviceKey := fmt.Sprintf("%d/service/P%d", e.ID.Call, e.ID.Proxy)
			for _, key := range []string{proxyKey, serviceKey} {
				if tt, ok := terminals[key]; ok && e.Tick >= tt {
					return fmt.Errorf("S3 violated: send %s at t=%d after %s terminal t=%d",
						e.ID, e.Tick, key, tt)
				}
			}
		}
	}
	for c := 1; c <= maxCall; c++ {
		if sends[c] > maxRequests {
			return fmt.Errorf("S2 violated: call %d sent %d > budget %d", c, sends[c], maxRequests)
		}
	}
	return nil
}
