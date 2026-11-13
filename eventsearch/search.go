// Package eventsearch exhaustively explores interleavings of the three
// lifecycle threads used by the REISSUE-20261113-0070 model checker.
//
// The goal is deliberately narrow: find the shortest schedule in which a
// successful Call(resource) happens after Revoke. A finalizer running, a dead
// object, or a high destructor count is not by itself a finding.
package eventsearch

import (
	"fmt"
	"strings"
)

// MaxThreads is the search bound required by the model.
const MaxThreads = 3

// Implementation selects the lifecycle transition rules.
type Implementation uint8

const (
	// Safe uses generation checks and host-approved resurrection.
	Safe Implementation = iota
	// Pending reproduces the intentionally vulnerable implementation in
	// example.org/netsec/q0070/pending.
	Pending
)

func (i Implementation) String() string {
	switch i {
	case Safe:
		return "safe"
	case Pending:
		return "pending-vulnerable"
	default:
		return fmt.Sprintf("Implementation(%d)", uint8(i))
	}
}

// Options controls the initial host policy and slot state.
type Options struct {
	// AllowResurrection is the host policy for the object under test.
	AllowResurrection bool
	// OccupiedGlobalSlot preloads slot "g1" with an unrelated sentinel object.
	// The finalizer always asks for "g1" and "g2" in that order.
	OccupiedGlobalSlot bool
}

// Action is one atomic event in a thread program.
type Action uint8

const (
	ActionAllocate Action = iota + 1
	ActionIssueTicket
	ActionStrongDrop
	ActionRevoke
	ActionFinalize
	ActionCall
	ActionReallocate
)

func (a Action) String() string {
	switch a {
	case ActionAllocate:
		return "Allocate"
	case ActionIssueTicket:
		return "IssueTicket"
	case ActionStrongDrop:
		return "StrongDrop"
	case ActionRevoke:
		return "Revoke"
	case ActionFinalize:
		return "Finalize"
	case ActionCall:
		return "Call"
	case ActionReallocate:
		return "Reallocate"
	default:
		return fmt.Sprintf("Action(%d)", uint8(a))
	}
}

// Thread indexes are fixed so traces are easy to compare.
const (
	ScriptThread    = 0
	HostThread      = 1
	FinalizerThread = 2
)

var program = [MaxThreads][]Action{
	ScriptThread:    {ActionIssueTicket, ActionCall},
	HostThread:      {ActionAllocate, ActionStrongDrop, ActionRevoke, ActionReallocate},
	FinalizerThread: {ActionFinalize},
}

// Step is one scheduling decision.
type Step struct {
	Thread int    `json:"thread"`
	Action Action `json:"action"`
	Detail string `json:"detail"`
}

func (s Step) String() string {
	return fmt.Sprintf("T%d %-11s %s", s.Thread, s.Action, s.Detail)
}

// Resurrection is an immutable record of one callback request.
type Resurrection struct {
	Slot     string `json:"slot"`
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
}

// Snapshot describes the state at the finding or at search exhaustion.
type Snapshot struct {
	ObjectID         int            `json:"object_id"`
	Address          int            `json:"address"`
	AllocGen         int            `json:"allocation_generation"`
	State            string         `json:"state"`
	Strong           int            `json:"strong"`
	CapGen           int            `json:"capability_generation"`
	Revoked          bool           `json:"revoked"`
	ResourceID       int            `json:"resource_id"`
	ResourceCalls    int            `json:"resource_calls"`
	GlobalSlotG1     int            `json:"global_slot_g1"`
	GlobalSlotG2     int            `json:"global_slot_g2"`
	WeakGetAttempted bool           `json:"weak_get_attempted"`
	WeakGetSucceeded bool           `json:"weak_get_succeeded"`
	Requests         []Resurrection `json:"resurrection_requests,omitempty"`
}

// Result is a shortest counterexample or a proof that the bounded event graph
// contains no post-revoke resource call.
type Result struct {
	Implementation Implementation `json:"implementation"`
	Found          bool           `json:"found"`
	Depth          int            `json:"depth"`
	ResourceCalls  int            `json:"resource_calls"`
	Steps          []Step         `json:"steps,omitempty"`
	Final          Snapshot       `json:"final"`
	StatesExplored int            `json:"states_explored"`
}

// Trace renders a compact multi-line counterexample.
func (r Result) Trace() string {
	if !r.Found {
		return fmt.Sprintf("%s: no post-revoke successful Call(resource) in the bounded search (%d states)",
			r.Implementation, r.StatesExplored)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: shortest post-revoke Call(resource), depth=%d, calls=%d\n",
		r.Implementation, r.Depth, r.ResourceCalls)
	for i, step := range r.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, step)
	}
	return strings.TrimRight(b.String(), "\n")
}

type ticketState struct {
	objectID int
	address  int
	allocGen int
	capGen   int
	resource int
	used     bool
}

const (
	stateNone = iota
	stateLive
	stateFinalizing
	stateDead
)

const sentinelObject = 99

type modelState struct {
	pc [MaxThreads]int

	allocated       bool
	reallocated     bool
	objectID        int
	address         int
	allocGen        int
	objState        int
	strong          int
	capGen          int
	revoked         bool
	revokedObjectID int
	revokedResource int
	resourceID      int
	lastResource    int
	ticket          *ticketState

	allow bool

	resourceLive  [3]bool
	resourceCalls [3]int

	queue     bool
	finalized bool

	slotG1 int
	slotG2 int

	weakAttempted bool
	weakSucceeded bool
	requests      []Resurrection

	totalCalls     int
	postRevokeCall bool
	trace          []Step
}

// FindShortest performs breadth-first event search. Breadth-first exploration
// makes the first violation found a shortest interleaving.
func FindShortest(impl Implementation, opts Options) Result {
	if impl != Safe && impl != Pending {
		return Result{Implementation: impl}
	}

	start := modelState{
		objState:     stateNone,
		allow:        opts.AllowResurrection,
		resourceLive: [3]bool{false, true, true},
	}
	if opts.OccupiedGlobalSlot {
		start.slotG1 = sentinelObject
	}

	frontier := []modelState{start}
	visited := map[string]bool{start.key(): true}
	explored := 1
	last := start

	for len(frontier) > 0 {
		next := make([]modelState, 0, len(frontier)*MaxThreads)
		for _, state := range frontier {
			last = state
			if state.postRevokeCall {
				return Result{
					Implementation: impl,
					Found:          true,
					Depth:          len(state.trace),
					ResourceCalls:  state.totalCalls,
					Steps:          append([]Step(nil), state.trace...),
					Final:          state.snapshot(),
					StatesExplored: explored,
				}
			}

			for thread := 0; thread < MaxThreads; thread++ {
				action, ok := nextAction(state, thread)
				if !ok || !enabled(state, thread, action, impl) {
					continue
				}
				child := state.clone()
				apply(&child, thread, action, impl)
				key := child.key()
				if visited[key] {
					continue
				}
				visited[key] = true
				explored++
				next = append(next, child)
			}
		}
		frontier = next
	}

	return Result{
		Implementation: impl,
		Found:          false,
		Final:          last.snapshot(),
		StatesExplored: explored,
	}
}

func nextAction(state modelState, thread int) (Action, bool) {
	pc := state.pc[thread]
	if pc >= len(program[thread]) {
		return 0, false
	}
	return program[thread][pc], true
}

func enabled(state modelState, thread int, action Action, impl Implementation) bool {
	switch action {
	case ActionAllocate:
		return !state.allocated
	case ActionIssueTicket:
		return state.objState == stateLive && state.strong > 0 && !state.revoked &&
			state.resourceID != 0 && state.resourceLive[state.resourceID] && state.ticket == nil
	case ActionStrongDrop:
		return state.objState != stateNone && state.strong > 0
	case ActionRevoke:
		return state.allocated && state.objState != stateDead && !state.revoked
	case ActionFinalize:
		return state.queue && state.objState == stateFinalizing && !state.finalized
	case ActionCall:
		return state.ticket != nil && !state.ticket.used
	case ActionReallocate:
		return state.allocated && state.objState == stateDead && !state.reallocated
	default:
		return false
	}
}

func apply(s *modelState, thread int, action Action, impl Implementation) {
	if action != ActionFinalize {
		s.pc[thread]++
	}

	switch action {
	case ActionAllocate:
		s.allocated = true
		s.objectID = 1
		s.address = 1
		s.allocGen = 1
		s.objState = stateLive
		s.strong = 1
		s.resourceID = 1
		s.lastResource = 1
		s.requests = nil
		s.queue = false
		s.finalized = false
		s.trace = append(s.trace, Step{
			Thread: thread,
			Action: action,
			Detail: "object A id=1 addr=1 gen=1 resource=1 strong=1",
		})
	case ActionIssueTicket:
		s.ticket = &ticketState{
			objectID: s.objectID,
			address:  s.address,
			allocGen: s.allocGen,
			capGen:   s.capGen,
			resource: s.resourceID,
		}
		s.trace = append(s.trace, Step{
			Thread: thread,
			Action: action,
			Detail: fmt.Sprintf("ticket binds id=%d addr=%d allocGen=%d capGen=%d resource=%d",
				s.objectID, s.address, s.allocGen, s.capGen, s.resourceID),
		})
	case ActionStrongDrop:
		s.strong--
		if s.strong == 0 && s.objState == stateLive {
			if s.finalized {
				s.die(true)
				s.trace = append(s.trace, Step{
					Thread: thread,
					Action: action,
					Detail: "post-finalization strong drop -> dead directly; no second finalizer",
				})
				return
			}
			s.objState = stateFinalizing
			s.queue = true
		}
		s.trace = append(s.trace, Step{
			Thread: thread,
			Action: action,
			Detail: fmt.Sprintf("strong -> %d, state=%s, finalizeQueued=%t",
				s.strong, stateString(s.objState), s.queue),
		})
	case ActionRevoke:
		s.capGen++
		s.revoked = true
		if s.resourceID != 0 {
			s.lastResource = s.resourceID
			s.revokedObjectID = s.objectID
			s.revokedResource = s.resourceID
			s.resourceLive[s.resourceID] = false
			s.resourceID = 0
		}
		s.trace = append(s.trace, Step{
			Thread: thread,
			Action: action,
			Detail: fmt.Sprintf("capGen=%d, resource=%d cleared", s.capGen, s.lastResource),
		})
	case ActionFinalize:
		s.pc[thread]++
		s.finalized = true
		s.queue = false
		s.weakAttempted = true
		s.weakSucceeded = false
		s.requests = []Resurrection{{
			Slot: "g1",
		}, {
			Slot: "g2",
		}}

		if impl == Pending {
			// VULNERABILITY: ignore host policy and revocation.
			if s.lastResource != 0 {
				s.resourceID = s.lastResource
				s.resourceLive[s.resourceID] = true
			}
			s.objState = stateLive
			s.strong = 1
			s.slotG1 = s.objectID
			s.slotG2 = s.objectID
			for i := range s.requests {
				s.requests[i].Accepted = true
				s.requests[i].Reason = "VULNERABLE_ACCEPTED"
			}
			s.trace = append(s.trace, Step{
				Thread: thread,
				Action: action,
				Detail: "WeakGet denied; two requests -> old resource restored and object put in g1,g2",
			})
			return
		}

		reason := ""
		switch {
		case !s.allow:
			reason = "host_denied"
		case s.revoked:
			reason = "capability_revoked"
		case s.resourceID == 0 || !s.resourceLive[s.resourceID]:
			reason = "resource_unavailable"
		}
		if reason == "" {
			s.requests[0].Accepted = true
			s.requests[0].Reason = "accepted"
			s.requests[1].Reason = "duplicate_request"
			s.objState = stateLive
			s.strong = 1
			s.slotG1 = s.objectID
			s.trace = append(s.trace, Step{
				Thread: thread,
				Action: action,
				Detail: "WeakGet denied; host allowed: g1 accepted, g2 duplicate rejected",
			})
			return
		}

		for i := range s.requests {
			s.requests[i].Reason = reason
		}
		s.die(true)
		s.trace = append(s.trace, Step{
			Thread: thread,
			Action: action,
			Detail: fmt.Sprintf("WeakGet denied; requests rejected (%s); object dead, resource cleared", reason),
		})
	case ActionCall:
		t := s.ticket
		valid := t != nil && !t.used &&
			t.objectID == s.objectID &&
			t.address == s.address &&
			t.allocGen == s.allocGen &&
			s.objState == stateLive &&
			s.strong > 0 &&
			t.resource == s.resourceID &&
			t.resource != 0 &&
			s.resourceLive[t.resource]
		if valid && impl == Safe {
			valid = !s.revoked && t.capGen == s.capGen
		}
		t.used = true
		if !valid {
			s.trace = append(s.trace, Step{
				Thread: thread,
				Action: action,
				Detail: "Call rejected; no host-resource invocation",
			})
			return
		}

		s.resourceCalls[t.resource]++
		s.totalCalls++
		if t.objectID == s.revokedObjectID && t.resource == s.revokedResource {
			s.postRevokeCall = true
			s.trace = append(s.trace, Step{
				Thread: thread,
				Action: action,
				Detail: fmt.Sprintf("Call(resource=%d) SUCCEEDED AFTER REVOKE; host calls=%d",
					t.resource, s.resourceCalls[t.resource]),
			})
			return
		}
		s.trace = append(s.trace, Step{
			Thread: thread,
			Action: action,
			Detail: fmt.Sprintf("Call(resource=%d) before revoke; host calls=%d",
				t.resource, s.resourceCalls[t.resource]),
		})
	case ActionReallocate:
		s.allocated = true
		s.reallocated = true
		s.objectID = 2
		s.allocGen++
		s.objState = stateLive
		s.strong = 1
		s.resourceID = 2
		s.lastResource = 2
		s.capGen = 0
		s.revoked = false
		s.trace = append(s.trace, Step{
			Thread: thread,
			Action: action,
			Detail: fmt.Sprintf("object B id=2 addr=%d gen=%d resource=2; old weak/ticket stale",
				s.address, s.allocGen),
		})
	}
}

func (s *modelState) die(clearResource bool) {
	s.objState = stateDead
	s.strong = 0
	if s.resourceID != 0 {
		s.lastResource = s.resourceID
		if clearResource {
			s.resourceLive[s.resourceID] = false
		}
		s.resourceID = 0
	}
	s.queue = false
}

func (s modelState) clone() modelState {
	copy := s
	if s.ticket != nil {
		t := *s.ticket
		copy.ticket = &t
	}
	copy.requests = append([]Resurrection(nil), s.requests...)
	copy.trace = append([]Step(nil), s.trace...)
	return copy
}

func (s modelState) key() string {
	var ticket string
	if s.ticket != nil {
		ticket = fmt.Sprintf("%d/%d/%d/%d/%d/%t",
			s.ticket.objectID, s.ticket.address, s.ticket.allocGen,
			s.ticket.capGen, s.ticket.resource, s.ticket.used)
	}
	return fmt.Sprintf(
		"%v|%t|%t|%d|%d|%d|%d|%d|%d|%t|%d|%d|%d|%d|%t|%v|%t|%t|%d|%d|%t|%t|%v|%s|%d|%v|%t",
		s.pc, s.allocated, s.reallocated, s.objectID, s.address, s.allocGen,
		s.objState, s.strong, s.capGen, s.revoked, s.revokedObjectID, s.revokedResource,
		s.resourceID, s.lastResource,
		s.allow, s.resourceLive, s.queue, s.finalized, s.slotG1, s.slotG2,
		s.weakAttempted, s.weakSucceeded, s.requests, ticket,
		s.totalCalls, s.resourceCalls, s.postRevokeCall,
	)
}

func (s modelState) snapshot() Snapshot {
	requests := append([]Resurrection(nil), s.requests...)
	calls := 0
	for _, n := range s.resourceCalls {
		calls += n
	}
	return Snapshot{
		ObjectID:         s.objectID,
		Address:          s.address,
		AllocGen:         s.allocGen,
		State:            stateString(s.objState),
		Strong:           s.strong,
		CapGen:           s.capGen,
		Revoked:          s.revoked,
		ResourceID:       s.resourceID,
		ResourceCalls:    calls,
		GlobalSlotG1:     s.slotG1,
		GlobalSlotG2:     s.slotG2,
		WeakGetAttempted: s.weakAttempted,
		WeakGetSucceeded: s.weakSucceeded,
		Requests:         requests,
	}
}

func stateString(state int) string {
	switch state {
	case stateNone:
		return "none"
	case stateLive:
		return "live"
	case stateFinalizing:
		return "finalizing"
	case stateDead:
		return "dead"
	default:
		return "unknown"
	}
}
