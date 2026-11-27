package txn

import (
	"errors"
	"fmt"
	"strings"
)

// JobType identifies the kind of job scheduled for a unit.
type JobType int

const (
	// JobStart starts (or keeps starting) a unit.
	JobStart JobType = iota
	// JobStop stops a unit.
	JobStop
	// JobRestart is start-like for ordering but also propagates through
	// reverse PartOf/BindsTo like a stop.
	JobRestart
	// JobVerifyActive is the internal no-op gate created for a satisfied
	// Requisite edge. It participates in ordering but never starts the unit.
	JobVerifyActive
)

// String returns the canonical lower-case job name.
func (t JobType) String() string {
	switch t {
	case JobStart:
		return "start"
	case JobStop:
		return "stop"
	case JobRestart:
		return "restart"
	case JobVerifyActive:
		return "verify-active"
	default:
		return fmt.Sprintf("job-%d", int(t))
	}
}

// startLike reports whether the job keeps the declared ordering polarity.
// Only JobStop reverses ordering edges.
func (t JobType) startLike() bool { return t != JobStop }

// TransactionMode selects how start/stop collisions are arbitrated.
type TransactionMode int

const (
	// ModeFail rejects and rolls back the whole transaction on any conflict.
	ModeFail TransactionMode = iota
	// ModeReplace prunes non-anchor, eliminable (reducible) jobs on conflict.
	ModeReplace
	// ModeReplaceIrreversibly behaves like ModeReplace but non-anchor
	// irreducible jobs may also be replaced; anchors remain protected.
	ModeReplaceIrreversibly
)

// ParseMode parses the canonical mode names "fail", "replace" and
// "replace-irreversibly".
func ParseMode(s string) (TransactionMode, error) {
	switch strings.TrimSpace(s) {
	case "fail":
		return ModeFail, nil
	case "replace":
		return ModeReplace, nil
	case "replace-irreversibly":
		return ModeReplaceIrreversibly, nil
	default:
		return 0, fmt.Errorf("%w: unknown mode %q", ErrInvalidRequest, s)
	}
}

func (m TransactionMode) String() string {
	switch m {
	case ModeFail:
		return "fail"
	case ModeReplace:
		return "replace"
	case ModeReplaceIrreversibly:
		return "replace-irreversibly"
	default:
		return fmt.Sprintf("mode-%d", int(m))
	}
}

// Unit describes one systemd unit and its direct dependency edges.
//
// PartOf lists parent units: when a parent is stopped/restarted the child is
// stopped/restarted too. Conflicts is interpreted symmetrically even when only
// one side declares it. Before/After hold explicit ordering relations.
type Unit struct {
	Name            string
	Requires        []string
	Requisite       []string
	Wants           []string
	BindsTo         []string
	PartOf          []string
	Conflicts       []string
	Before          []string
	After           []string
	IgnoreOnIsolate bool
}

// Graph is the full static unit dependency graph.
type Graph struct {
	Units map[string]*Unit
}

// Snapshot is the current active-state snapshot of units.
type Snapshot map[string]bool

// Active reports whether the unit is currently active.
func (s Snapshot) Active(name string) bool { return s[name] }

// Request describes one incoming manager request.
type Request struct {
	// Unit is the anchor unit the caller acts on.
	Unit string
	// Job is JobStart, JobStop or JobRestart.
	Job JobType
	// Isolate turns a start request into an isolate request.
	Isolate bool
	// Mode selects conflict arbitration.
	Mode TransactionMode
}

// PlannedJob is one retained job of the solved transaction.
type PlannedJob struct {
	Unit        string
	Type        JobType
	Anchor      bool
	Irreducible bool
}

// OrderEdge is a directed ordering constraint between two retained jobs.
// From must be executed before To. Reversed is true when the edge was flipped
// because at least one endpoint is a stop job.
type OrderEdge struct {
	From     string
	To       string
	Reversed bool
	Reason   string
}

// CutReport records one ordering cycle and the edge deleted to break it.
type CutReport struct {
	// Cycle lists the units on the minimal cycle, in cycle order.
	Cycle []string
	// Edge is the ordering edge that was deleted.
	Edge OrderEdge
}

// Plan is the executable result of Solve: retained jobs, a DAG of ordering
// edges, deterministic execution layers and the full cycle-cut report.
type Plan struct {
	Request Request
	Jobs    []PlannedJob
	Edges   []OrderEdge
	// Layers groups jobs that may run in parallel; layers execute in order.
	Layers [][]string
	// Cuts reports every ordering cycle that was broken.
	Cuts []CutReport
}

// Sentinel errors; use errors.Is to match them.
var (
	// ErrRequisiteNotActive is returned when a Requisite target is not active
	// in the snapshot and therefore cannot be pulled into the transaction.
	ErrRequisiteNotActive = errors.New("requisite target is not active")
	// ErrConflict is returned when start/stop jobs collide and the mode
	// forbids replacing the existing job.
	ErrConflict = errors.New("conflicting start and stop jobs")
	// ErrUnknownUnit is returned when a hard dependency points at a unit that
	// is absent from the graph.
	ErrUnknownUnit = errors.New("unknown unit referenced")
	// ErrInvalidRequest is returned for malformed requests or mode names.
	ErrInvalidRequest = errors.New("invalid transaction request")
)
