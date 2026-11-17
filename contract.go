// Package netsec provides a small, deterministic contract monitor for
// asynchronous security checks.
//
// The monitor is built around one hard rule: a cancellation confirmation is a
// barrier. Once an operation generation has been confirmed as cancelled, a
// result belonging to that generation must never be consumed. Result
// generation may still race with cancellation; it is the hand-off to the
// caller (consumption) that the barrier protects.
//
// Events are ordered by the order in which they are recorded. This makes a
// Monitor safe to use from multiple goroutines: the mutex acquisition order is
// the observed order, and every event receives a monotonically increasing
// sequence number.
package netsec

import (
	"fmt"
	"strings"
	"sync"
)

// EventKind identifies one event in an operation's lifecycle.
//
// EventKind is a string type so that events can be passed through JSON or a
// test host without an out-of-band enum. The constants below are the canonical
// values; Record also accepts common spelling variants such as
// "cancel_requested" and "result_produced".
type EventKind string

// Canonical event kinds.
const (
	Start           EventKind = "start"
	CancelRequest   EventKind = "cancel-request"
	CancelConfirm   EventKind = "cancel-confirm"
	ResultGenerated EventKind = "result-generated"
	ResultConsumed  EventKind = "result-consumed"
)

// Event kind aliases. All aliases resolve to the canonical kinds above.
const (
	EventStart              = Start
	EventStarted            = Start
	EventInitiate           = Start
	EventInitiated          = Start
	StartEvent              = Start
	StartedEvent            = Start
	OperationStarted        = Start
	OpStarted               = Start
	Started                 = Start
	Begin                   = Start
	Initiated               = Start
	EventCancelRequest      = CancelRequest
	EventCancelRequested    = CancelRequest
	CancelRequestEvent      = CancelRequest
	CancelRequestedEvent    = CancelRequest
	CancelRequested         = CancelRequest
	CancellationRequested   = CancelRequest
	OpCancelRequested       = CancelRequest
	EventCancelConfirm      = CancelConfirm
	EventCancelConfirmed    = CancelConfirm
	CancelConfirmEvent      = CancelConfirm
	CancelConfirmedEvent    = CancelConfirm
	CancelConfirmed         = CancelConfirm
	CancellationConfirmed   = CancelConfirm
	CancelAck               = CancelConfirm
	CancelAcknowledged      = CancelConfirm
	Cancelled               = CancelConfirm
	Canceled                = CancelConfirm
	EventCancelAck          = CancelConfirm
	EventCancelAcknowledged = CancelConfirm
	EventCancelConfirmation = CancelConfirm
	OpCancelConfirmed       = CancelConfirm
	EventResultGenerated    = ResultGenerated
	EventResultProduced     = ResultGenerated
	ResultGeneratedEvent    = ResultGenerated
	ResultProducedEvent     = ResultGenerated
	ResultProduced          = ResultGenerated
	ResultReady             = ResultGenerated
	ResultArrived           = ResultGenerated
	ResultAvailable         = ResultGenerated
	ResultDelivered         = ResultGenerated
	EventResultReady        = ResultGenerated
	EventResultProduce      = ResultGenerated
	OpResultGenerated       = ResultGenerated
	EventResultConsumed     = ResultConsumed
	EventResultConsume      = ResultConsumed
	ResultConsumedEvent     = ResultConsumed
	ResultConsume           = ResultConsumed
	EventConsume            = ResultConsumed
	Consumed                = ResultConsumed
	OpResultConsumed        = ResultConsumed
)

// EventType is an alias for EventKind.
type EventType = EventKind

// Kind is an alias for EventKind.
type Kind = EventKind

// Event describes one observation made by the test host.
//
// OperationID and Generation identify the operation instance. Generation is
// what makes reuse of an operation ID safe: a cancellation confirmation for
// generation N can never suppress generation N+1.
//
// For a first generation, Generation may be left at zero. When a new Start is
// recorded for an already-used operation ID with Generation zero, the monitor
// automatically advances to the next generation. Once an ID has been reused,
// callers that deliver late events for an older generation must set the
// explicit generation on those events.
//
// Type, Event, Name, OperationId, Operation, OpID, Op, ID and Gen are accepted
// as input-only synonyms for Kind, OperationID and Generation. Canonical
// fields take precedence.
type Event struct {
	Kind        EventKind
	OperationID string
	Generation  int
	Sequence    uint64

	// Input-only compatibility aliases.
	Type        EventKind
	Event       EventKind
	Name        EventKind
	OperationId string
	Operation   string
	OpID        string
	Op          string
	ID          string
	Gen         int
	Seq         uint64
}

// NewEvent builds an event from its kind, operation ID and generation.
func NewEvent(kind EventKind, operationID string, generation int) Event {
	return Event{Kind: kind, OperationID: operationID, Generation: generation}
}

// NewStartedEvent builds a Start event.
func NewStartedEvent(operationID string, generation int) Event {
	return NewEvent(Start, operationID, generation)
}

// NewCancelRequestedEvent builds a CancelRequest event.
func NewCancelRequestedEvent(operationID string, generation int) Event {
	return NewEvent(CancelRequest, operationID, generation)
}

// NewCancelConfirmedEvent builds a CancelConfirm event.
func NewCancelConfirmedEvent(operationID string, generation int) Event {
	return NewEvent(CancelConfirm, operationID, generation)
}

// NewResultGeneratedEvent builds a ResultGenerated event.
func NewResultGeneratedEvent(operationID string, generation int) Event {
	return NewEvent(ResultGenerated, operationID, generation)
}

// NewResultConsumedEvent builds a ResultConsumed event.
func NewResultConsumedEvent(operationID string, generation int) Event {
	return NewEvent(ResultConsumed, operationID, generation)
}

func (e Event) operationID() string {
	switch {
	case e.OperationID != "":
		return e.OperationID
	case e.OperationId != "":
		return e.OperationId
	case e.Operation != "":
		return e.Operation
	case e.OpID != "":
		return e.OpID
	case e.Op != "":
		return e.Op
	default:
		return e.ID
	}
}

func (e Event) requestedGeneration() int {
	if e.Generation != 0 {
		return e.Generation
	}
	return e.Gen
}

func normalizeKind(kind EventKind) EventKind {
	s := strings.ToLower(strings.TrimSpace(string(kind)))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.TrimPrefix(s, "event-")
	s = strings.TrimPrefix(s, "op-")
	s = strings.TrimPrefix(s, "operation-")

	switch s {
	case "start", "started", "begin", "begun", "initiate", "initiated",
		"operation-start", "operation-started":
		return Start
	case "cancel-request", "cancel-requested", "cancel", "request-cancel",
		"cancellation-request", "cancellation-requested", "requested":
		return CancelRequest
	case "cancel-confirm", "cancel-confirmed", "cancel-ack",
		"cancel-acknowledged", "cancellation-confirm", "cancellation-confirmed",
		"cancellation-ack", "cancellation-acknowledged", "cancel-confirmation",
		"cancelled", "canceled", "confirmed", "ack":
		return CancelConfirm
	case "result-generated", "result-generation", "result-produced",
		"result-produce", "result-ready", "result-arrived", "result",
		"result-available", "result-delivered", "generated", "produced",
		"available", "delivered":
		return ResultGenerated
	case "result-consumed", "result-consume", "consume", "consumed",
		"consumption", "result-consumption":
		return ResultConsumed
	default:
		return EventKind(s)
	}
}

func (k EventKind) String() string {
	if k == "" {
		return "unknown"
	}
	return string(k)
}

// ViolationKind identifies the reason a contract violation was reported.
type ViolationKind string

// ViolationConsumeAfterCancelConfirmation means a result was consumed after
// its generation's cancellation had already been confirmed.
const ViolationConsumeAfterCancelConfirmation ViolationKind = "consume-after-cancel-confirmation"

// ConsumeAfterCancelConfirmation is an alias for
// ViolationConsumeAfterCancelConfirmation.
const ConsumeAfterCancelConfirmation = ViolationConsumeAfterCancelConfirmation

// Violation describes a barrier breach. Index is zero-based in the input event
// slice; Position is one-based. Sequence is the monitor's own total order.
type Violation struct {
	Kind        ViolationKind
	Type        ViolationKind
	OperationID string
	OperationId string
	OpID        string
	Op          string
	Generation  int
	Gen         int
	EventKind   EventKind
	Event       EventKind

	Index    int
	Position int
	Pos      int
	Sequence uint64
	// At is an alias for the zero-based Index.
	At int

	// EventIndex/EventPosition are aliases for Index/Position.
	EventIndex    int
	EventPosition int

	ConsumeIndex    int
	ConsumePosition int
	ConsumeSequence uint64
	ConsumedAt      int

	ConfirmIndex    int
	ConfirmPosition int
	ConfirmSequence uint64
	ConfirmedAt     int

	Message         string
	Reason          string
	LateConsumption bool
}

// Error lets a Violation be used as an error.
func (v Violation) Error() string {
	if v.Message != "" {
		return v.Message
	}
	return fmt.Sprintf("%s: operation %q generation %d consumed at event %d after cancellation confirmation at event %d",
		v.Kind, v.OperationID, v.Generation, v.Position, v.ConfirmPosition)
}

// ConsumptionOrder records where a consumption fell relative to cancellation
// events. Before-confirmation consumption is a legitimate race, not a
// violation.
type ConsumptionOrder string

const (
	// ConsumptionNoCancellation means no cancellation request was recorded for
	// the generation.
	ConsumptionNoCancellation ConsumptionOrder = "no-cancellation"
	// ConsumptionBeforeCancelRequest means consumption preceded both the
	// cancellation request and its confirmation.
	ConsumptionBeforeCancelRequest ConsumptionOrder = "before-cancel-request"
	// ConsumptionAfterCancelRequest means consumption happened after the
	// cancellation request but before confirmation.
	ConsumptionAfterCancelRequest ConsumptionOrder = "after-cancel-request-before-confirmation"
	// ConsumptionAfterCancelConfirmation is the forbidden order.
	ConsumptionAfterCancelConfirmation ConsumptionOrder = "after-cancel-confirmation"
)

// Consumption order aliases.
const (
	OrderNoCancellation           = ConsumptionNoCancellation
	OrderBeforeCancelRequest      = ConsumptionBeforeCancelRequest
	OrderAfterCancelRequest       = ConsumptionAfterCancelRequest
	OrderAfterCancelConfirmation  = ConsumptionAfterCancelConfirmation
	ConsumptionBeforeConfirmation = ConsumptionBeforeCancelRequest
	ConsumptionAfterConfirmation  = ConsumptionAfterCancelConfirmation
	NoCancellation                = ConsumptionNoCancellation
	BeforeCancelRequest           = ConsumptionBeforeCancelRequest
	AfterCancelRequest            = ConsumptionAfterCancelRequest
	AfterCancelConfirm            = ConsumptionAfterCancelConfirmation
)

// Consumption is the audit record for one ResultConsumed event. Its order is
// recomputed against the complete generation history, so a consumption that
// happened before a later cancellation request is still reported as
// "before-cancel-request".
type Consumption struct {
	OperationID string
	OperationId string
	OpID        string
	Op          string
	Generation  int
	Gen         int

	Index    int
	Position int
	Pos      int
	Sequence uint64

	Order              ConsumptionOrder
	BeforeConfirmation bool
	AfterConfirmation  bool
	Violation          bool
	HasCancelRequest   bool
	HasCancelConfirm   bool

	CancelRequestIndex    int
	CancelRequestPosition int
	CancelRequestSequence uint64

	CancelConfirmIndex    int
	CancelConfirmPosition int
	CancelConfirmSequence uint64
}

// AnomalyKind identifies a lifecycle inconsistency that is not itself a
// barrier breach. Anomalies are reported separately so they cannot mask the
// barrier verdict.
type AnomalyKind string

const (
	AnomalyUnknownEvent                AnomalyKind = "unknown-event"
	AnomalyDuplicateStart              AnomalyKind = "duplicate-start"
	AnomalyCancelConfirmWithoutStart   AnomalyKind = "cancel-confirm-without-start"
	AnomalyCancelConfirmWithoutRequest AnomalyKind = "cancel-confirm-without-request"
	AnomalyDuplicateCancelRequest      AnomalyKind = "duplicate-cancel-request"
	AnomalyDuplicateCancelConfirm      AnomalyKind = "duplicate-cancel-confirm"
	AnomalyCancelRequestAfterConfirm   AnomalyKind = "cancel-request-after-confirm"
	AnomalyResultWithoutStart          AnomalyKind = "result-without-start"
	AnomalyConsumeWithoutStart         AnomalyKind = "consume-without-start"
	AnomalyDuplicateConsumption        AnomalyKind = "duplicate-consumption"
)

// Anomaly is a non-barrier lifecycle problem.
type Anomaly struct {
	Kind        AnomalyKind
	OperationID string
	OperationId string
	OpID        string
	Op          string
	Generation  int
	Gen         int
	EventKind   EventKind
	Index       int
	Position    int
	Pos         int
	Sequence    uint64
	Message     string
}

// Error lets an Anomaly be used as an error.
func (a Anomaly) Error() string {
	if a.Message != "" {
		return a.Message
	}
	return fmt.Sprintf("%s: operation %q generation %d at event %d",
		a.Kind, a.OperationID, a.Generation, a.Position)
}

// GenerationInfo is a snapshot of one operation generation.
type GenerationInfo struct {
	OperationID string
	OperationId string
	OpID        string
	Op          string
	Generation  int
	Gen         int

	Started         bool
	CancelRequested bool
	CancelConfirmed bool

	StartSequence         uint64
	CancelRequestSequence uint64
	CancelConfirmSequence uint64

	ResultGenerated int
	ResultConsumed  int
	// ResultGeneratedAfterConfirmation counts callbacks that arrived after the
	// barrier. They are not violations by themselves; consuming them is.
	ResultGeneratedAfterConfirmation int
}

// Ordering is one event in monitor sequence order.
type Ordering struct {
	Sequence uint64
	Index    int
	Position int
	Event    Event
}

// OrderedEvent is an alias for Ordering.
type OrderedEvent = Ordering

// Report is a complete, immutable snapshot of the monitor.
type Report struct {
	Events       []Event
	Order        []Ordering
	Generations  []GenerationInfo
	Consumptions []Consumption
	Violations   []Violation
	Problems     []Violation
	Anomalies    []Anomaly
	Violation    *Violation
	Violated     bool
	HasViolation bool
}

// OK reports whether the barrier was respected.
func (r Report) OK() bool {
	return len(r.Violations) == 0
}

// FirstViolation returns the earliest barrier breach, if any.
func (r Report) FirstViolation() (Violation, bool) {
	if len(r.Violations) == 0 {
		return Violation{}, false
	}
	return r.Violations[0], true
}

// HasViolations reports whether any barrier violation was recorded.
func (r Report) HasViolations() bool {
	return len(r.Violations) > 0
}

// IsSafe reports whether the barrier was respected.
func (r Report) IsSafe() bool {
	return r.OK()
}

// Outcome is returned for each recorded event. A nil Violation means the
// event did not breach the barrier.
type Outcome struct {
	Accepted    bool
	Valid       bool
	Allowed     bool
	Denied      bool
	Violated    bool
	Index       int
	Position    int
	Sequence    uint64
	Kind        EventKind
	OperationID string
	Generation  int

	Violation   *Violation
	Anomaly     *Anomaly
	Consumption *Consumption
}

// OK reports whether the event was accepted without a barrier violation.
func (o Outcome) OK() bool {
	return o.Accepted && o.Violation == nil
}

type generationKey struct {
	operationID string
	generation  int
}

type generationState struct {
	key generationKey

	started  bool
	startSeq uint64
	startIdx int

	cancelRequestSeq uint64
	cancelRequestIdx int

	cancelConfirmed  bool
	cancelConfirmSeq uint64
	cancelConfirmIdx int

	resultSeqs  []uint64
	consumeSeqs []uint64
	consumeIdxs []int
}

type consumeRef struct {
	key generationKey
	seq uint64
	idx int
}

type recordedEvent struct {
	seq uint64
	idx int
	ev  Event
}

// Monitor records events and reports barrier violations. The zero value is
// ready for use and safe for concurrent Record calls.
type Monitor struct {
	mu sync.Mutex

	nextSeq uint64
	events  []recordedEvent

	generations map[generationKey]*generationState
	genOrder    []generationKey
	latest      map[string]int
	maxGen      map[string]int
	hasStarted  map[string]bool
	maxStarted  map[string]int

	consumeRefs []consumeRef
	violations  []Violation
	anomalies   []Anomaly
}

// Guard is an alias for Monitor.
type Guard = Monitor

// Tracker is an alias for Monitor.
type Tracker = Monitor

// ContractMonitor is an alias for Monitor.
type ContractMonitor = Monitor

// Checker is an alias for Monitor.
type Checker = Monitor

// NewMonitor returns an empty monitor.
func NewMonitor() *Monitor {
	return &Monitor{}
}

// New is an alias for NewMonitor.
func New() *Monitor {
	return NewMonitor()
}

// NewContractMonitor is an alias for NewMonitor.
func NewContractMonitor() *Monitor {
	return NewMonitor()
}

// NewTracker is an alias for NewMonitor.
func NewTracker() *Monitor {
	return NewMonitor()
}

// NewContract is an alias for NewMonitor.
func NewContract() *Monitor {
	return NewMonitor()
}

// NewGuard is an alias for NewMonitor.
func NewGuard() *Monitor {
	return NewMonitor()
}

// Reset clears all recorded state. The monitor remains reusable.
func (m *Monitor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSeq = 0
	m.events = nil
	m.generations = nil
	m.genOrder = nil
	m.latest = nil
	m.maxGen = nil
	m.hasStarted = nil
	m.maxStarted = nil
	m.consumeRefs = nil
	m.violations = nil
	m.anomalies = nil
}

// Len returns the number of recorded events.
func (m *Monitor) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

// Record adds an event and returns the immediate outcome. The barrier check is
// performed at the point of consumption.
func (m *Monitor) Record(event Event) Outcome {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recordLocked(event, len(m.events))
}

// Observe is an alias for Record.
func (m *Monitor) Observe(event Event) Outcome {
	return m.Record(event)
}

// Add is an alias for Record.
func (m *Monitor) Add(event Event) Outcome {
	return m.Record(event)
}

// Emit is an alias for Record.
func (m *Monitor) Emit(event Event) Outcome {
	return m.Record(event)
}

// Append is an alias for Record.
func (m *Monitor) Append(event Event) Outcome {
	return m.Record(event)
}

// Check is an alias for Record.
func (m *Monitor) Check(event Event) Outcome {
	return m.Record(event)
}

func (m *Monitor) initLocked() {
	if m.generations == nil {
		m.generations = make(map[generationKey]*generationState)
	}
	if m.latest == nil {
		m.latest = make(map[string]int)
	}
	if m.maxGen == nil {
		m.maxGen = make(map[string]int)
	}
	if m.hasStarted == nil {
		m.hasStarted = make(map[string]bool)
	}
	if m.maxStarted == nil {
		m.maxStarted = make(map[string]int)
	}
}

func (m *Monitor) stateLocked(key generationKey) *generationState {
	m.initLocked()
	if state, ok := m.generations[key]; ok {
		return state
	}
	state := &generationState{key: key, startIdx: -1, cancelRequestIdx: -1, cancelConfirmIdx: -1}
	m.generations[key] = state
	m.genOrder = append(m.genOrder, key)
	return state
}

func (m *Monitor) resolveGeneration(operationID string, kind EventKind, requested int) int {
	m.initLocked()

	latest, seen := m.latest[operationID]
	if kind == Start {
		generation := requested
		if generation <= 0 {
			if m.hasStarted[operationID] {
				generation = m.maxStarted[operationID] + 1
			} else if seen {
				generation = latest
			} else {
				generation = 0
			}
		}
		if !seen || generation > latest {
			m.latest[operationID] = generation
		}
		if !seen || generation > m.maxGen[operationID] {
			m.maxGen[operationID] = generation
		}
		m.hasStarted[operationID] = true
		if !seen || generation > m.maxStarted[operationID] {
			m.maxStarted[operationID] = generation
		}
		return generation
	}

	if requested != 0 {
		if !seen || requested > latest {
			m.latest[operationID] = requested
		}
		if !seen || requested > m.maxGen[operationID] {
			m.maxGen[operationID] = requested
		}
		return requested
	}
	if seen {
		return latest
	}
	return 0
}

func (m *Monitor) addAnomaly(kind AnomalyKind, key generationKey, eventKind EventKind, seq uint64, idx int, format string, args ...any) Anomaly {
	message := fmt.Sprintf(format, args...)
	anomaly := Anomaly{
		Kind:        kind,
		OperationID: key.operationID,
		OperationId: key.operationID,
		OpID:        key.operationID,
		Op:          key.operationID,
		Generation:  key.generation,
		Gen:         key.generation,
		EventKind:   eventKind,
		Index:       idx,
		Position:    idx + 1,
		Pos:         idx + 1,
		Sequence:    seq,
		Message:     message,
	}
	m.anomalies = append(m.anomalies, anomaly)
	return anomaly
}

func (m *Monitor) recordLocked(event Event, index int) Outcome {
	m.initLocked()

	kind := normalizeKind(event.Kind)
	if kind == "" {
		kind = normalizeKind(event.Type)
	}
	if kind == "" {
		kind = normalizeKind(event.Event)
	}
	if kind == "" {
		kind = normalizeKind(event.Name)
	}
	operationID := event.operationID()
	generation := m.resolveGeneration(operationID, kind, event.requestedGeneration())

	m.nextSeq++
	seq := m.nextSeq
	canonical := Event{
		Kind:        kind,
		OperationID: operationID,
		Generation:  generation,
		Sequence:    seq,
	}
	m.events = append(m.events, recordedEvent{seq: seq, idx: index, ev: canonical})

	outcome := Outcome{
		Accepted:    true,
		Index:       index,
		Position:    index + 1,
		Sequence:    seq,
		Kind:        kind,
		OperationID: operationID,
		Generation:  generation,
	}
	key := generationKey{operationID: operationID, generation: generation}

	switch kind {
	case Start:
		state := m.stateLocked(key)
		if state.started {
			anomaly := m.addAnomaly(AnomalyDuplicateStart, key, kind, seq, index,
				"duplicate start for operation %q generation %d", operationID, generation)
			outcome.Anomaly = &anomaly
		} else {
			state.started = true
			state.startSeq = seq
			state.startIdx = index
		}

	case CancelRequest:
		state := m.stateLocked(key)
		switch {
		case state.cancelConfirmed:
			anomaly := m.addAnomaly(AnomalyCancelRequestAfterConfirm, key, kind, seq, index,
				"cancel request for operation %q generation %d arrived after confirmation", operationID, generation)
			outcome.Anomaly = &anomaly
			if state.cancelRequestSeq == 0 {
				state.cancelRequestSeq = seq
				state.cancelRequestIdx = index
			}
		case state.cancelRequestSeq != 0:
			anomaly := m.addAnomaly(AnomalyDuplicateCancelRequest, key, kind, seq, index,
				"duplicate cancel request for operation %q generation %d", operationID, generation)
			outcome.Anomaly = &anomaly
		default:
			state.cancelRequestSeq = seq
			state.cancelRequestIdx = index
		}

	case CancelConfirm:
		state := m.stateLocked(key)
		if !state.started {
			anomaly := m.addAnomaly(AnomalyCancelConfirmWithoutStart, key, kind, seq, index,
				"cancel confirmation for operation %q generation %d has no start", operationID, generation)
			outcome.Anomaly = &anomaly
		}
		if state.cancelRequestSeq == 0 {
			anomaly := m.addAnomaly(AnomalyCancelConfirmWithoutRequest, key, kind, seq, index,
				"cancel confirmation for operation %q generation %d has no request", operationID, generation)
			outcome.Anomaly = &anomaly
		}
		if state.cancelConfirmed {
			anomaly := m.addAnomaly(AnomalyDuplicateCancelConfirm, key, kind, seq, index,
				"duplicate cancel confirmation for operation %q generation %d", operationID, generation)
			outcome.Anomaly = &anomaly
		} else {
			state.cancelConfirmed = true
			state.cancelConfirmSeq = seq
			state.cancelConfirmIdx = index
		}

	case ResultGenerated:
		state := m.stateLocked(key)
		if !state.started {
			anomaly := m.addAnomaly(AnomalyResultWithoutStart, key, kind, seq, index,
				"result for operation %q generation %d has no start", operationID, generation)
			outcome.Anomaly = &anomaly
		}
		state.resultSeqs = append(state.resultSeqs, seq)

	case ResultConsumed:
		state := m.stateLocked(key)
		if !state.started {
			anomaly := m.addAnomaly(AnomalyConsumeWithoutStart, key, kind, seq, index,
				"consumption for operation %q generation %d has no start", operationID, generation)
			outcome.Anomaly = &anomaly
		}
		if len(state.consumeSeqs) > 0 {
			anomaly := m.addAnomaly(AnomalyDuplicateConsumption, key, kind, seq, index,
				"duplicate consumption for operation %q generation %d", operationID, generation)
			outcome.Anomaly = &anomaly
		}
		state.consumeSeqs = append(state.consumeSeqs, seq)
		state.consumeIdxs = append(state.consumeIdxs, index)
		m.consumeRefs = append(m.consumeRefs, consumeRef{key: key, seq: seq, idx: index})

		if state.cancelConfirmed && state.cancelConfirmSeq < seq {
			confirmPosition := state.cancelConfirmIdx + 1
			violation := Violation{
				Kind:            ViolationConsumeAfterCancelConfirmation,
				Type:            ViolationConsumeAfterCancelConfirmation,
				OperationID:     operationID,
				OperationId:     operationID,
				OpID:            operationID,
				Op:              operationID,
				Generation:      generation,
				Gen:             generation,
				EventKind:       kind,
				Event:           kind,
				Index:           index,
				Position:        index + 1,
				Pos:             index + 1,
				Sequence:        seq,
				At:              index,
				EventIndex:      index,
				EventPosition:   index + 1,
				ConsumeIndex:    index,
				ConsumePosition: index + 1,
				ConsumeSequence: seq,
				ConsumedAt:      index,
				ConfirmIndex:    state.cancelConfirmIdx,
				ConfirmPosition: confirmPosition,
				ConfirmSequence: state.cancelConfirmSeq,
				ConfirmedAt:     state.cancelConfirmIdx,
				LateConsumption: true,
				Message: fmt.Sprintf(
					"result consumed after cancellation confirmation: operation %q generation %d, consume event %d follows confirm event %d",
					operationID, generation, index+1, confirmPosition),
			}
			violation.Reason = violation.Message
			m.violations = append(m.violations, violation)
			outcome.Violation = &violation
		}
		consumption := buildConsumption(state, operationID, generation, seq, index)
		outcome.Consumption = &consumption

	default:
		outcome.Accepted = false
		anomaly := m.addAnomaly(AnomalyUnknownEvent, key, kind, seq, index,
			"unknown event kind %q for operation %q generation %d", kind, operationID, generation)
		outcome.Anomaly = &anomaly
	}

	outcome.Valid = outcome.Accepted
	outcome.Violated = outcome.Violation != nil
	outcome.Allowed = outcome.Accepted && !outcome.Violated
	outcome.Denied = outcome.Violated
	return outcome
}

func buildConsumption(state *generationState, operationID string, generation int, seq uint64, index int) Consumption {
	consumption := Consumption{
		OperationID:           operationID,
		OperationId:           operationID,
		OpID:                  operationID,
		Op:                    operationID,
		Generation:            generation,
		Gen:                   generation,
		Index:                 index,
		Position:              index + 1,
		Pos:                   index + 1,
		Sequence:              seq,
		CancelRequestIndex:    state.cancelRequestIdx,
		CancelRequestPosition: -1,
		CancelRequestSequence: state.cancelRequestSeq,
		CancelConfirmIndex:    state.cancelConfirmIdx,
		CancelConfirmPosition: -1,
		CancelConfirmSequence: state.cancelConfirmSeq,
	}
	if state.cancelRequestIdx >= 0 {
		consumption.CancelRequestPosition = state.cancelRequestIdx + 1
	}
	if state.cancelConfirmIdx >= 0 {
		consumption.CancelConfirmPosition = state.cancelConfirmIdx + 1
	}
	consumption.HasCancelRequest = state.cancelRequestSeq != 0
	consumption.HasCancelConfirm = state.cancelConfirmSeq != 0

	confirmedFirst := state.cancelConfirmSeq != 0 && state.cancelConfirmSeq < seq
	blocked := confirmedFirst
	consumption.AfterConfirmation = blocked
	consumption.BeforeConfirmation = !blocked
	consumption.Violation = blocked

	switch {
	case blocked:
		consumption.Order = ConsumptionAfterCancelConfirmation
	case state.cancelRequestSeq != 0 && state.cancelRequestSeq < seq:
		consumption.Order = ConsumptionAfterCancelRequest
	case state.cancelRequestSeq != 0 && state.cancelRequestSeq > seq:
		consumption.Order = ConsumptionBeforeCancelRequest
	default:
		consumption.Order = ConsumptionNoCancellation
	}
	return consumption
}

// Report returns a snapshot of all events, audit records and violations.
func (m *Monitor) Report() Report {
	m.mu.Lock()
	defer m.mu.Unlock()

	report := Report{
		Events:       make([]Event, 0, len(m.events)),
		Order:        make([]Ordering, 0, len(m.events)),
		Generations:  make([]GenerationInfo, 0, len(m.genOrder)),
		Consumptions: make([]Consumption, 0, len(m.consumeRefs)),
		Violations:   append([]Violation(nil), m.violations...),
		Anomalies:    append([]Anomaly(nil), m.anomalies...),
	}
	for _, recorded := range m.events {
		report.Events = append(report.Events, recorded.ev)
		report.Order = append(report.Order, Ordering{
			Sequence: recorded.seq,
			Index:    recorded.idx,
			Position: recorded.idx + 1,
			Event:    recorded.ev,
		})
	}
	for _, key := range m.genOrder {
		state := m.generations[key]
		lateResults := 0
		for _, resultSequence := range state.resultSeqs {
			if state.cancelConfirmSeq != 0 && resultSequence > state.cancelConfirmSeq {
				lateResults++
			}
		}
		report.Generations = append(report.Generations, GenerationInfo{
			OperationID:                      key.operationID,
			OperationId:                      key.operationID,
			OpID:                             key.operationID,
			Op:                               key.operationID,
			Generation:                       key.generation,
			Gen:                              key.generation,
			Started:                          state.started,
			CancelRequested:                  state.cancelRequestSeq != 0,
			CancelConfirmed:                  state.cancelConfirmed,
			StartSequence:                    state.startSeq,
			CancelRequestSequence:            state.cancelRequestSeq,
			CancelConfirmSequence:            state.cancelConfirmSeq,
			ResultGenerated:                  len(state.resultSeqs),
			ResultConsumed:                   len(state.consumeSeqs),
			ResultGeneratedAfterConfirmation: lateResults,
		})
	}
	for _, ref := range m.consumeRefs {
		state := m.generations[ref.key]
		report.Consumptions = append(report.Consumptions,
			buildConsumption(state, ref.key.operationID, ref.key.generation, ref.seq, ref.idx))
	}
	report.Violated = len(report.Violations) > 0
	report.Problems = append([]Violation(nil), report.Violations...)
	report.HasViolation = report.Violated
	if report.Violated {
		violation := report.Violations[0]
		report.Violation = &violation
	}
	return report
}

// Snapshot is an alias for Report.
func (m *Monitor) Snapshot() Report {
	return m.Report()
}

// Analyze is an alias for Report.
func (m *Monitor) Analyze() Report {
	return m.Report()
}

// Audit is an alias for Report.
func (m *Monitor) Audit() Report {
	return m.Report()
}

// Result is an alias for Report.
func (m *Monitor) Result() Report {
	return m.Report()
}

// Violations returns the barrier violations recorded so far.
func (m *Monitor) Violations() []Violation {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Violation(nil), m.violations...)
}

// Problems is an alias for Violations.
func (m *Monitor) Problems() []Violation {
	return m.Violations()
}

// BarrierViolations is an alias for Violations.
func (m *Monitor) BarrierViolations() []Violation {
	return m.Violations()
}

// Anomalies returns non-barrier lifecycle inconsistencies.
func (m *Monitor) Anomalies() []Anomaly {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Anomaly(nil), m.anomalies...)
}

// Consumptions returns the ordering audit for all consumption events.
func (m *Monitor) Consumptions() []Consumption {
	return m.Report().Consumptions
}

// Events returns canonical copies of the recorded events.
func (m *Monitor) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	events := make([]Event, 0, len(m.events))
	for _, recorded := range m.events {
		events = append(events, recorded.ev)
	}
	return events
}

// FirstViolation returns the earliest barrier violation recorded so far.
func (m *Monitor) FirstViolation() (Violation, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.violations) == 0 {
		return Violation{}, false
	}
	return m.violations[0], true
}

// OK reports whether no barrier violation has been recorded.
func (m *Monitor) OK() bool {
	return len(m.Violations()) == 0
}

// IsSafe is an alias for OK.
func (m *Monitor) IsSafe() bool {
	return m.OK()
}

// Validate returns the first barrier violation as an error, or nil.
func (m *Monitor) Validate() error {
	violation, ok := m.FirstViolation()
	if !ok {
		return nil
	}
	return violation
}

// Analyze records every event in slice order and returns the resulting report.
func Analyze(events []Event) Report {
	monitor := NewMonitor()
	for index, event := range events {
		monitor.mu.Lock()
		monitor.recordLocked(event, index)
		monitor.mu.Unlock()
	}
	return monitor.Report()
}

// AnalyzeEvents is the variadic form of Analyze.
func AnalyzeEvents(events ...Event) Report {
	return Analyze(events)
}

// Check returns only the barrier violations for the supplied events.
func Check(events []Event) []Violation {
	return Analyze(events).Violations
}

// CheckEvents is the variadic form of Check.
func CheckEvents(events ...Event) []Violation {
	return Check(events)
}

// DetectViolations is an alias for Check.
func DetectViolations(events []Event) []Violation {
	return Check(events)
}

// HasViolation reports whether the supplied events contain a barrier breach.
func HasViolation(events []Event) bool {
	return len(Check(events)) > 0
}

// Allowed reports whether the supplied events respect the barrier.
func Allowed(events []Event) bool {
	return !HasViolation(events)
}

// Evaluate is an alias for Analyze.
func Evaluate(events []Event) Report {
	return Analyze(events)
}

// Verify returns the first barrier violation as an error, or nil.
func Verify(events []Event) error {
	violation, ok := Analyze(events).FirstViolation()
	if !ok {
		return nil
	}
	return violation
}

// Validate is an alias for Verify.
func Validate(events []Event) error {
	return Verify(events)
}

// Run is an alias for Verify.
func Run(events []Event) error {
	return Verify(events)
}

// FirstViolation returns the earliest barrier violation in the event slice.
func FirstViolation(events []Event) (Violation, bool) {
	return Analyze(events).FirstViolation()
}

// CheckOne records one event and returns its immediate barrier violation.
func CheckOne(event Event) (Violation, bool) {
	outcome := NewMonitor().Record(event)
	if outcome.Violation == nil {
		return Violation{}, false
	}
	return *outcome.Violation, true
}

func (m *Monitor) event(kind EventKind, operationID string, generation int) Outcome {
	return m.Record(Event{Kind: kind, OperationID: operationID, Generation: generation})
}

// Start records a Start event. Generation is optional; if omitted the monitor
// auto-advances when an operation ID has been reused.
func (m *Monitor) Start(operationID string, generation ...int) Outcome {
	return m.event(Start, operationID, firstGeneration(generation))
}

// CancelRequest records a cancellation request.
func (m *Monitor) CancelRequest(operationID string, generation ...int) Outcome {
	return m.event(CancelRequest, operationID, firstGeneration(generation))
}

// CancelConfirm records a cancellation confirmation, which forms the barrier.
func (m *Monitor) CancelConfirm(operationID string, generation ...int) Outcome {
	return m.event(CancelConfirm, operationID, firstGeneration(generation))
}

// ResultGenerated records that an operation result was produced. Result
// generation after confirmation is permitted; consuming it is not.
func (m *Monitor) ResultGenerated(operationID string, generation ...int) Outcome {
	return m.event(ResultGenerated, operationID, firstGeneration(generation))
}

// ResultProduced is an alias for ResultGenerated.
func (m *Monitor) ResultProduced(operationID string, generation ...int) Outcome {
	return m.ResultGenerated(operationID, generation...)
}

// ResultConsumed records a result hand-off to the caller and checks the
// barrier at that point.
func (m *Monitor) ResultConsumed(operationID string, generation ...int) Outcome {
	return m.event(ResultConsumed, operationID, firstGeneration(generation))
}

// Consume is an alias for ResultConsumed.
func (m *Monitor) Consume(operationID string, generation ...int) Outcome {
	return m.ResultConsumed(operationID, generation...)
}

func firstGeneration(generation []int) int {
	if len(generation) == 0 {
		return 0
	}
	return generation[0]
}
