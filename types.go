package nvmerecov

import (
	"errors"
	"fmt"
)

// RequestID is the stable command identifier used by submission queues,
// barriers, completion receipts, and recovery reports.
type RequestID uint64

// Op is a zone command kind.
type Op uint8

const (
	OpAppend Op = iota + 1
	OpReset
	OpRead
)

func (op Op) String() string {
	switch op {
	case OpAppend:
		return "append"
	case OpReset:
		return "reset"
	case OpRead:
		return "read"
	default:
		return fmt.Sprintf("op(%d)", uint8(op))
	}
}

// Command is one submitted request. Data is used by Append, Addr and Count by
// Read, and neither by Reset. After lists additional predecessor requests; the
// preceding command in the same queue is always an implicit predecessor.
type Command struct {
	ID    RequestID
	Op    Op
	Zone  int
	Addr  uint64
	Count uint64
	Data  Blocks
	After []RequestID
}

// Barrier is a cross-queue synchronization point. No command at index Before
// (or any later command in this queue) may execute until every request in After
// has executed. A barrier at len(Commands) has no request to gate.
type Barrier struct {
	Before int
	After  []RequestID
}

// Queue is one ordered submission queue. Commands execute in slice order.
type Queue struct {
	Commands []Command
	Barriers []Barrier
}

// Zone describes a zone. Initial is optional initial content; when nil the
// zone starts empty, which is the usual recovery input. Reset always returns a
// zone to the empty state.
type Zone struct {
	Capacity uint64
	Initial  Blocks
}

// Model is the sealed device program: at most four queues, at most three
// zones, and at most thirty requests in the intended workloads.
type Model struct {
	Queues []Queue
	Zones  []Zone
}

// Sample is a block value sampled at the breakpoint.
type Sample struct {
	Address uint64
	Block   Block
}

// ObservedRange is exact expected block content at a breakpoint. It is also
// how a complete or partial zone image is supplied: Start is the absolute LBA
// and Blocks describes the image from Start onward. The block string is
// compared by real content, never by hashing, and is never expanded.
type ObservedRange struct {
	Start  uint64
	Blocks Blocks
}

// ZoneObservation describes one zone at the migration breakpoint.
type ZoneObservation struct {
	Zone            int
	HasWritePointer bool
	WritePointer    uint64
	Samples         []Sample
	Ranges          []ObservedRange
}

// Receipt is a successful completion receipt that survived migration. Which
// payload field is required depends on the command: Append requires HasLBA,
// Read requires HasBlocks, and Reset carries no payload.
type Receipt struct {
	Request   RequestID
	HasLBA    bool
	LBA       uint64
	HasBlocks bool
	Blocks    Blocks
}

// Observation is the sealed breakpoint evidence. A request not named by a
// receipt may have executed with a lost receipt or may not have executed.
type Observation struct {
	Zones    []ZoneObservation
	Receipts []Receipt
}

// OutcomeKind identifies a request outcome.
type OutcomeKind uint8

const (
	// OutcomeNotExecuted means the request was still in the unexecuted tail.
	OutcomeNotExecuted OutcomeKind = iota
	// OutcomeAppend is a successful Append; LBA is the returned start address.
	OutcomeAppend
	// OutcomeReset is a successful Reset acknowledgement.
	OutcomeReset
	// OutcomeRead is a successful Read; Blocks is its returned content.
	OutcomeRead
)

func (k OutcomeKind) String() string {
	switch k {
	case OutcomeNotExecuted:
		return "not-executed"
	case OutcomeAppend:
		return "append"
	case OutcomeReset:
		return "reset"
	case OutcomeRead:
		return "read"
	default:
		return fmt.Sprintf("outcome(%d)", uint8(k))
	}
}

// Outcome is one exact result of executing, or not executing, a request.
type Outcome struct {
	Kind   OutcomeKind
	LBA    uint64
	Blocks Blocks
}

// NotExecuted is the singleton unexecuted outcome.
var NotExecuted = Outcome{Kind: OutcomeNotExecuted}

// AppendOutcome returns the receipt produced by an Append.
func AppendOutcome(lba uint64) Outcome {
	return Outcome{Kind: OutcomeAppend, LBA: lba}
}

// ResetOutcome returns the receipt produced by a Reset.
func ResetOutcome() Outcome {
	return Outcome{Kind: OutcomeReset}
}

// ReadOutcome returns the content produced by a Read.
func ReadOutcome(blocks Blocks) Outcome {
	return Outcome{Kind: OutcomeRead, Blocks: Normalize(blocks)}
}

// Equal compares outcomes by exact content. Identical Read content with
// different run boundaries is still one executed result.
func (o Outcome) Equal(other Outcome) bool {
	if o.Kind != other.Kind {
		return false
	}
	switch o.Kind {
	case OutcomeAppend:
		return o.LBA == other.LBA
	case OutcomeRead:
		return o.Blocks.Equal(other.Blocks)
	default:
		return true
	}
}

func (o Outcome) String() string {
	switch o.Kind {
	case OutcomeNotExecuted:
		return "not executed"
	case OutcomeAppend:
		return fmt.Sprintf("append LBA=%d", o.LBA)
	case OutcomeReset:
		return "reset ack"
	case OutcomeRead:
		return "read " + o.Blocks.String()
	default:
		return fmt.Sprintf("outcome(%d)", uint8(o.Kind))
	}
}

// Step is one executed request in an independently replayable prefix.
type Step struct {
	Request RequestID
	Op      Op
	Zone    int
	Result  Outcome
}

// Prefix is an execution prefix in actual execution order. Replaying the
// steps from an empty device reproduces every Result and reaches the observed
// breakpoint state.
type Prefix struct {
	Steps []Step
}

// MissingResult reports all exact outcomes compatible with the observations
// for one request whose successful receipt was lost.
type MissingResult struct {
	Request RequestID
	Op      Op

	// Possible contains unexecuted and every distinct executed result.
	Possible []Outcome

	// Witnesses contains one independently replayable prefix for each
	// distinct possible outcome.
	Witnesses []Prefix

	// Publishable is true only when there is exactly one distinct executed
	// result. It may coexist with non-execution; use Deterministic when the
	// caller also needs certainty that the request ran.
	Publishable bool

	// Deterministic is true when there is exactly one possible outcome
	// overall, either one executed result or only non-execution.
	Deterministic bool
}

// UniqueExecuted returns the sole executed result, if there is exactly one,
// even when non-execution is also possible.
func (m MissingResult) UniqueExecuted() (Outcome, bool) {
	var found Outcome
	count := 0
	for _, outcome := range m.Possible {
		if outcome.Kind != OutcomeNotExecuted {
			found = outcome
			count++
		}
	}
	return found, count == 1
}

// Analysis is the result of recovering every missing receipt under one legal
// prefix.
type Analysis struct {
	Valid  bool
	Reason string

	// Prefix is one independently replayable witness when Valid is true.
	Prefix Prefix

	// Missing is ordered by request number.
	Missing []MissingResult

	// Witnesses contains two prefixes with different outcomes when at least
	// one missing receipt is not unique. Otherwise it is empty.
	Witnesses []Prefix
}

// Assumption is a host-supplied hypothetical result for one request.
type Assumption struct {
	Request RequestID
	Outcome Outcome
}

// JointResult answers a batch of assumptions. Satisfiable is true only when a
// single legal prefix satisfies the observations and every assumption at once.
type JointResult struct {
	Satisfiable bool
	Reason      string
	Prefix      Prefix
}

var (
	ErrInvalidModel       = errors.New("nvmerecov: invalid model")
	ErrInvalidObservation = errors.New("nvmerecov: invalid observation")
	ErrUnknownRequest     = errors.New("nvmerecov: unknown request")
)
