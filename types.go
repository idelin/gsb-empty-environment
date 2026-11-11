package netsec

// OverflowPolicy selects what a bounded channel does when a send reaches a
// full queue. Sequence numbers are assigned to every send attempt before the
// policy is resolved, so either policy leaves a permanent gap in the sequence
// space when a message is discarded.
type OverflowPolicy int

const (
	// DropNew discards the arriving message. The queued messages keep their
	// sequence numbers and the new number is never delivered.
	DropNew OverflowPolicy = iota
	// DropOld discards the oldest queued message, then enqueues the arriving
	// message at the tail. The receiver later sees the new sequence number
	// without the discarded one.
	DropOld
)

func (p OverflowPolicy) String() string {
	switch p {
	case DropNew:
		return "drop-new"
	case DropOld:
		return "drop-old"
	default:
		return "unknown-overflow-policy"
	}
}

// ActionKind distinguishes the outputs a transition performs.
type ActionKind int

const (
	// SendAction enqueues a message on a channel.
	SendAction ActionKind = iota
)

// Action is an output emitted when a transition fires.
type Action struct {
	// Kind is currently always SendAction.
	Kind ActionKind
	// Channel is the target channel name.
	Channel string
	// Message is the protocol-level message tag carried by the send.
	Message string
}

// Send is a convenience constructor for a send action.
func Send(channel, message string) Action {
	return Action{Kind: SendAction, Channel: channel, Message: message}
}

// Transition is one edge of an endpoint state machine.
//
// An empty OnRecv marks an internal edge that fires without consuming a
// message. A non-empty OnRecv edge fires only when the head of the endpoint's
// incoming channel carries that message tag. Several edges may share the same
// source and trigger, modelling nondeterministic hosts.
type Transition struct {
	From   string
	OnRecv string
	To     string
	Do     []Action
}

// SuccessBinding binds a terminal success state to the incoming messages the
// endpoint must have received before declaring it. Every required tag must
// have been dequeued at least once; when RequireNoGap is set, the received
// sequence numbers must additionally cover 1..max without holes. Declaring
// the state while a requirement is unmet is a half authentication and is
// reported as an Unsafe trace.
type SuccessBinding struct {
	// State is the terminal success state name.
	State string
	// RequiredTags lists message tags that must have been received.
	RequiredTags []string
	// RequireNoGap demands that every sequence number from 1 through the
	// highest received number was dequeued. Drop-old on a full queue evicts
	// earlier numbers and creates exactly such a gap.
	RequireNoGap bool
}

// Endpoint describes one protocol participant.
type Endpoint struct {
	// Name uniquely identifies the endpoint.
	Name string
	// Init is its initial state.
	Init string
	// Incoming is the channel from which this endpoint dequeues messages.
	Incoming string
	// Transitions is the endpoint state machine.
	Transitions []Transition
	// Success binds each terminal success state to the highest sequence
	// numbers it requires on the incoming channel. Success states are
	// terminal.
	Success []SuccessBinding
}

// Channel describes a bounded FIFO queue connecting endpoints.
type Channel struct {
	// Name uniquely identifies the channel.
	Name string
	// Capacity is the maximum number of queued messages; it must be >= 1.
	Capacity int
}

// Spec is the fully input-driven model: endpoint machines, channel topology
// and queue capacities. The overflow policy is supplied per Check call so the
// same spec can be compared under both strategies.
type Spec struct {
	Endpoints []Endpoint
	Channels  []Channel
}

// EventKind tags the steps recorded in an explored trace.
type EventKind int

const (
	// EvInternal: an internal transition fired.
	EvInternal EventKind = iota
	// EvRecv: a message was dequeued and consumed by an endpoint.
	EvRecv
	// EvSend: a send attempt was assigned a sequence number.
	EvSend
	// EvDrop: the send was discarded by the overflow policy.
	EvDrop
	// EvSuccess: an endpoint entered a terminal success state.
	EvSuccess
)

func (k EventKind) String() string {
	switch k {
	case EvInternal:
		return "internal"
	case EvRecv:
		return "recv"
	case EvSend:
		return "send"
	case EvDrop:
		return "drop"
	case EvSuccess:
		return "success"
	default:
		return "unknown-event"
	}
}

// Event is one step in a trace.
type Event struct {
	Kind     EventKind
	Endpoint string
	Channel  string
	// Seq is the channel-wide sequence number; 0 when not applicable.
	Seq int
	// Tag is the message tag; empty when not applicable.
	Tag string
	// State is the endpoint state resulting from the step.
	State string
	// Bound is the required sequence number carried by an EvSuccess event.
	Bound int
	// Missing lists required sequence numbers that were never received; it is
	// only populated on an EvSuccess event that violates the binding.
	Missing []int
	// MissingTags lists required message tags that were never received.
	MissingTags []string
}

// Verdict is the overall result of a search.
type Verdict int

const (
	// Safe means the reachable state space was exhaustively explored and no
	// trace enters a success state without every required message.
	Safe Verdict = iota
	// Unsafe means such a half-authentication trace exists; Report.Trace
	// reproduces it.
	Unsafe
	// Unknown means a budget was exhausted before the search could complete,
	// so neither safety nor unsafety can be claimed.
	Unknown
)

func (v Verdict) String() string {
	switch v {
	case Safe:
		return "safe"
	case Unsafe:
		return "unsafe"
	case Unknown:
		return "unknown"
	default:
		return "unknown-verdict"
	}
}

// SearchStats summarises the work the checker performed.
type SearchStats struct {
	// VisitedStates is the number of distinct global states expanded.
	VisitedStates int
	// FiredTransitions is the number of endpoint transitions taken.
	FiredTransitions int
	// DroppedSends is the number of send attempts discarded by queues.
	DroppedSends int
	// MaxDepth is the longest explored trace length.
	MaxDepth int
}

// Budget bounds the search. Exhausting MaxStates or reaching MaxSteps yields
// an Unknown verdict rather than an unsound Safe claim.
type Budget struct {
	// MaxStates caps the number of distinct global states explored.
	MaxStates int
	// MaxSteps caps the length of any single trace.
	MaxSteps int
}

// Report is the result returned by Check.
type Report struct {
	Verdict Verdict
	Policy  OverflowPolicy
	// Trace is the violating trace, populated only when Verdict == Unsafe.
	Trace  []Event
	Reason string
	Stats  SearchStats
}
