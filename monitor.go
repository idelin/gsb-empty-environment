// Package netsec provides a contract monitor for asynchronous security-check
// operations. A test host feeds it lifecycle events (initiate, cancel
// request, cancel confirm, result generated, result consumed) and the
// monitor flags contract violations.
//
// The central rule is the cancel barrier: once a cancellation is confirmed,
// the result of that operation generation must never be consumed. A consume
// racing an in-flight cancel (requested but not yet confirmed) is a normal
// race and is recorded with its explicit ordering instead of being flagged.
//
// Operation IDs may be reused; each reuse is a distinct generation and is
// tracked independently.
package netsec

import (
	"fmt"
	"sync"
)

// EventKind enumerates the lifecycle events of one operation generation.
type EventKind int

const (
	EventInitiate EventKind = iota
	EventCancelRequest
	EventCancelConfirm
	EventResultGenerated
	EventResultConsumed
)

func (k EventKind) String() string {
	switch k {
	case EventInitiate:
		return "initiate"
	case EventCancelRequest:
		return "cancel-request"
	case EventCancelConfirm:
		return "cancel-confirm"
	case EventResultGenerated:
		return "result-generated"
	case EventResultConsumed:
		return "result-consumed"
	}
	return fmt.Sprintf("event(%d)", int(k))
}

// Event is one observed lifecycle event for a generation of an operation.
type Event struct {
	Op   string
	Gen  int
	Kind EventKind
}

// ViolationKind classifies contract breaches.
type ViolationKind int

const (
	// ViolationConsumeAfterConfirm is the cancel-barrier breach: the result
	// of a generation was consumed after its cancellation was confirmed.
	ViolationConsumeAfterConfirm ViolationKind = iota
	// ViolationConsumeWithoutResult is a consume with no result generated.
	ViolationConsumeWithoutResult
	// ViolationDuplicateConsume is a second consume of the same generation.
	ViolationDuplicateConsume
	// ViolationEventBeforeInitiate is any non-initiate event arriving before
	// the generation was initiated.
	ViolationEventBeforeInitiate
	// ViolationConfirmWithoutRequest is a cancel confirm with no prior
	// cancel request in the same generation.
	ViolationConfirmWithoutRequest
	// ViolationDuplicateInitiate is a repeated initiate of the same
	// operation generation.
	ViolationDuplicateInitiate
)

func (k ViolationKind) String() string {
	switch k {
	case ViolationConsumeAfterConfirm:
		return "consume-after-confirm"
	case ViolationConsumeWithoutResult:
		return "consume-without-result"
	case ViolationDuplicateConsume:
		return "duplicate-consume"
	case ViolationEventBeforeInitiate:
		return "event-before-initiate"
	case ViolationConfirmWithoutRequest:
		return "confirm-without-request"
	case ViolationDuplicateInitiate:
		return "duplicate-initiate"
	}
	return fmt.Sprintf("violation(%d)", int(k))
}

// Violation pinpoints one contract breach. Seq is the 1-based position of
// the offending event in the global recorded stream.
type Violation struct {
	Seq    int
	Op     string
	Gen    int
	Kind   ViolationKind
	Event  EventKind
	Detail string
}

func (v Violation) String() string {
	return fmt.Sprintf("seq=%d op=%q gen=%d %s: %s (%s)",
		v.Seq, v.Op, v.Gen, v.Kind, v.Event, v.Detail)
}

// LogEntry is one recorded event in a generation's ordered timeline. Note
// carries the explicit ordering annotation for races (e.g. a consume that
// arrived while a cancel was still pending).
type LogEntry struct {
	Seq  int
	Kind EventKind
	Note string
}

type genKey struct {
	op  string
	gen int
}

type genState struct {
	initiated bool
	requested bool
	confirmed bool
	generated bool
	consumed  bool
	log       []LogEntry
}

// Monitor records operation events and reports contract violations. It is
// safe for concurrent use by a test host.
type Monitor struct {
	mu         sync.Mutex
	seq        int
	gens       map[genKey]*genState
	nextGen    map[string]int
	violations []Violation
}

// NewMonitor returns an empty Monitor.
func NewMonitor() *Monitor {
	return &Monitor{
		gens:    make(map[genKey]*genState),
		nextGen: make(map[string]int),
	}
}

// Begin allocates the next generation for op and records its initiate
// event, supporting operation-ID reuse. It returns the new generation.
func (m *Monitor) Begin(op string) int {
	m.mu.Lock()
	gen := m.nextGen[op] + 1
	m.nextGen[op] = gen
	m.mu.Unlock()
	m.Record(Event{Op: op, Gen: gen, Kind: EventInitiate})
	return gen
}

// Emit records a single event for a generation of op.
func (m *Monitor) Emit(op string, gen int, kind EventKind) {
	m.Record(Event{Op: op, Gen: gen, Kind: kind})
}

// Record ingests one event, updating generation state, appending to the
// ordered timeline, and flagging any contract violation.
func (m *Monitor) Record(ev Event) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.seq++
	seq := m.seq
	k := genKey{op: ev.Op, gen: ev.Gen}
	st := m.gens[k]
	if st == nil {
		st = &genState{}
		m.gens[k] = st
	}

	note := ""
	addViolation := func(kind ViolationKind, detail string) {
		m.violations = append(m.violations, Violation{
			Seq:    seq,
			Op:     ev.Op,
			Gen:    ev.Gen,
			Kind:   kind,
			Event:  ev.Kind,
			Detail: detail,
		})
	}

	switch ev.Kind {
	case EventInitiate:
		if st.initiated {
			addViolation(ViolationDuplicateInitiate,
				"generation already initiated; reuse the ID with a new generation")
		}
		st.initiated = true

	case EventCancelRequest:
		if !st.initiated {
			addViolation(ViolationEventBeforeInitiate,
				"cancel request for a generation that was never initiated")
		} else {
			st.requested = true
			if st.confirmed {
				note = "cancel-request-after-confirm"
			}
		}

	case EventCancelConfirm:
		if !st.initiated {
			addViolation(ViolationEventBeforeInitiate,
				"cancel confirm for a generation that was never initiated")
		} else {
			if !st.requested {
				addViolation(ViolationConfirmWithoutRequest,
					"cancel confirmed without a preceding cancel request")
			}
			st.confirmed = true
		}

	case EventResultGenerated:
		if !st.initiated {
			addViolation(ViolationEventBeforeInitiate,
				"result generated for a generation that was never initiated")
		} else {
			st.generated = true
			if st.confirmed {
				// Normal race: the check completed after the cancel was
				// confirmed. Harmless as long as the result is never consumed.
				note = "generated-after-confirm"
			}
		}

	case EventResultConsumed:
		switch {
		case !st.initiated:
			addViolation(ViolationEventBeforeInitiate,
				"result consumed for a generation that was never initiated")
		case st.confirmed:
			addViolation(ViolationConsumeAfterConfirm,
				"cancel barrier breached: result consumed after cancel confirm; caller may wrongly allow the request")
		case !st.generated:
			addViolation(ViolationConsumeWithoutResult,
				"result consumed before any result was generated")
		case st.consumed:
			addViolation(ViolationDuplicateConsume,
				"result of this generation consumed more than once")
		default:
			st.consumed = true
			switch {
			case !st.requested:
				note = "consumed-before-cancel-request"
			default:
				// Normal race: consume won against an in-flight cancel.
				note = "consumed-while-cancel-pending"
			}
		}
	}

	st.log = append(st.log, LogEntry{Seq: seq, Kind: ev.Kind, Note: note})
}

// Violations returns a copy of all recorded violations in stream order.
func (m *Monitor) Violations() []Violation {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Violation, len(m.violations))
	copy(out, m.violations)
	return out
}

// Timeline returns the ordered event log for one generation, including
// ordering annotations for races.
func (m *Monitor) Timeline(op string, gen int) []LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.gens[genKey{op: op, gen: gen}]
	if st == nil {
		return nil
	}
	out := make([]LogEntry, len(st.log))
	copy(out, st.log)
	return out
}
