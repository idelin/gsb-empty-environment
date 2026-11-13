package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// EventName identifies a state-machine transition or externally visible
// protocol step.  Every event is dispatched twice: once before the transition
// and once after it.
type EventName string

const (
	EventWriteBegin    EventName = "source.write.begin"
	EventWriteCommit   EventName = "source.write.commit"
	EventRoundSnapshot EventName = "round.snapshot"

	EventPageCopyBegin EventName = "page.copy.begin"
	EventPageCopyDone  EventName = "page.copy.done"
	EventPageStage     EventName = "target.stage"
	EventPageReadback  EventName = "target.readback"
	EventDirtyAck      EventName = "source.dirty.ack"

	EventFreezeBegin EventName = "freeze.begin"
	EventFreezeWait  EventName = "freeze.wait"
	EventFreezeDone  EventName = "freeze.done"
	EventThaw        EventName = "source.thaw"

	EventFinalPageCopy EventName = "cutover.page.copy"
	EventDeviceState   EventName = "cutover.device.state"
	EventManifestBuild EventName = "cutover.manifest.build"
	EventReceiptSign   EventName = "cutover.receipt.sign"

	EventReceiptDelivery  EventName = "handoff.receipt.delivery"
	EventReceiptVerify    EventName = "target.receipt.verify"
	EventTargetStartCheck EventName = "target.start.check"
	EventTargetStart      EventName = "target.start"
	EventTargetVisible    EventName = "target.visible"

	EventStartupConfirmation EventName = "target.startup.confirmation"
	EventSourceConfirm       EventName = "source.confirm"
	EventSourceTerminated    EventName = "source.terminated"

	EventAbort                 EventName = "migration.abort"
	EventQuarantineInvalidated EventName = "target.quarantine.invalidated"
	EventSourceReleased        EventName = "source.released"
)

// ProtocolEvents returns every event name emitted by the state machine.  It is
// useful for exhaustive fault-injection and trace-coverage harnesses.
func ProtocolEvents() []EventName {
	return []EventName{
		EventWriteBegin, EventWriteCommit, EventRoundSnapshot,
		EventPageCopyBegin, EventPageCopyDone, EventPageStage,
		EventPageReadback, EventDirtyAck,
		EventFreezeBegin, EventFreezeWait, EventFreezeDone, EventThaw,
		EventFinalPageCopy, EventDeviceState, EventManifestBuild, EventReceiptSign,
		EventReceiptDelivery, EventReceiptVerify, EventTargetStartCheck,
		EventTargetStart, EventTargetVisible,
		EventStartupConfirmation, EventSourceConfirm, EventSourceTerminated,
		EventAbort, EventQuarantineInvalidated, EventSourceReleased,
	}
}

// EventPhase distinguishes pre-transition observation/failure injection from
// post-transition observation.
type EventPhase uint8

const (
	EventPhaseBefore EventPhase = iota
	EventPhaseAfter
)

func (p EventPhase) String() string {
	if p == EventPhaseBefore {
		return "before"
	}
	return "after"
}

// EventFields carries the structured facts associated with an event.  Only the
// fields relevant to a particular event are populated.
type EventFields struct {
	VM                 VMID
	Page               PageID
	Generation         Generation
	TargetGeneration   Generation
	ExpectedGeneration Generation
	Epoch              Epoch
	Round              int
	Kind               WriteKind
	Data               []byte
	Digest             Digest
	Cleared            bool
	Copied             int
	ClearedCount       int
	RemainingDirty     int
	WaitedWrites       uint64
	ManifestRoot       Digest
	ReceiptDigest      Digest
	Reason             string
}

// Event is delivered to a fault injector or recorder.
type Event struct {
	Name       EventName
	Phase      EventPhase
	Seq        uint64
	Occurrence uint64
	Fields     EventFields
}

// FaultInjector may fail an event.  A non-nil error is wrapped with
// ErrInjectedFault and returned by the operation that emitted the event.
// Injectors must not retain mutable event data.
type FaultInjector interface {
	Inject(Event) error
}

// FaultFunc adapts a function to FaultInjector.
type FaultFunc func(Event) error

// Inject implements FaultInjector.
func (f FaultFunc) Inject(ev Event) error {
	return f(ev)
}

type eventKey struct {
	name       EventName
	phase      EventPhase
	occurrence uint64
}

// EventDispatcher provides deterministic, per-event fault injection.  It is
// safe for concurrent use.  The callback is always invoked without an internal
// dispatcher lock and without any memory/target lock, so a callback may issue
// writes or inspect state to model races.
type EventDispatcher struct {
	mu       sync.Mutex
	fault    FaultInjector
	seq      uint64
	counts   map[eventKey]uint64
	recorder func(Event)
}

// NewEventDispatcher constructs a dispatcher.  record may be nil.
func NewEventDispatcher(fault FaultInjector, record ...func(Event)) *EventDispatcher {
	d := &EventDispatcher{fault: fault, counts: make(map[eventKey]uint64)}
	if len(record) > 0 {
		d.recorder = record[0]
	}
	return d
}

// SetFault replaces the fault injector atomically.
func (d *EventDispatcher) SetFault(f FaultInjector) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.fault = f
	d.mu.Unlock()
}

// SetRecorder replaces the observer atomically.
func (d *EventDispatcher) SetRecorder(record func(Event)) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.recorder = record
	d.mu.Unlock()
}

// Emit dispatches one event and returns the injector's failure, if any.
func (d *EventDispatcher) Emit(name EventName, phase EventPhase, fields EventFields) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	d.seq++
	seq := d.seq
	key := eventKey{name: name, phase: phase}
	d.counts[key]++
	occ := d.counts[key]
	fault := d.fault
	recorder := d.recorder
	d.mu.Unlock()

	ev := Event{Name: name, Phase: phase, Seq: seq, Occurrence: occ, Fields: fields}
	if recorder != nil {
		recorder(ev)
	}
	if fault != nil {
		if err := fault.Inject(ev); err != nil {
			return fmt.Errorf("%w: %s/%s #%d: %w", ErrInjectedFault, name, phase, occ, err)
		}
	}
	return nil
}

// FaultAt builds a scripted fault at an exact, one-based event occurrence.
type FaultAt struct {
	Name       EventName
	Phase      EventPhase
	Occurrence uint64
	Err        error
}

// ScriptedFaults injects a fixed error at selected event occurrences.
type ScriptedFaults struct {
	mu     sync.Mutex
	faults map[eventKey]error
	seen   map[eventKey]bool
}

// NewScriptedFaults builds a deterministic fault script.  A zero Err defaults
// to ErrInjectedFault.
func NewScriptedFaults(points ...FaultAt) *ScriptedFaults {
	f := &ScriptedFaults{
		faults: make(map[eventKey]error),
		seen:   make(map[eventKey]bool),
	}
	for _, p := range points {
		if p.Err == nil {
			p.Err = ErrInjectedFault
		}
		f.faults[eventKey{name: p.Name, phase: p.Phase, occurrence: p.Occurrence}] = p.Err
	}
	return f
}

// Inject implements FaultInjector.
func (f *ScriptedFaults) Inject(ev Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := eventKey{name: ev.Name, phase: ev.Phase, occurrence: ev.Occurrence}
	if err, ok := f.faults[key]; ok {
		f.seen[key] = true
		return err
	}
	return nil
}

// Fired reports whether an exact point was reached.
func (f *ScriptedFaults) Fired(name EventName, phase EventPhase, occurrence uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen[eventKey{name: name, phase: phase, occurrence: occurrence}]
}

// DropReceipt models a receipt that never reaches the target.
func DropReceipt() FaultInjector {
	return FaultFunc(func(ev Event) error {
		if ev.Name == EventReceiptDelivery && ev.Phase == EventPhaseBefore {
			return errors.New("receipt lost in transit")
		}
		return nil
	})
}

// DropStartupConfirmation models a confirmation that never reaches the
// source after the target has already become visible.
func DropStartupConfirmation() FaultInjector {
	return FaultFunc(func(ev Event) error {
		if ev.Name == EventStartupConfirmation && ev.Phase == EventPhaseAfter {
			return errors.New("startup confirmation lost in transit")
		}
		return nil
	})
}
