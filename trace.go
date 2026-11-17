package netsec

import (
	"fmt"
	"strings"
)

// TraceStatus describes how evaluation ended.
type TraceStatus uint8

const (
	TraceInvalid TraceStatus = iota
	// TraceCompleted means the root expression produced a boolean verdict.
	TraceCompleted
	// TraceCanceled means the context was canceled, or a check reported
	// cancellation, before a verdict could be produced.
	TraceCanceled
	// TraceCheckFailed means a check returned an error or an invalid result.
	TraceCheckFailed
	// TracePanicked means a check panicked.
	TracePanicked
)

func (s TraceStatus) String() string {
	switch s {
	case TraceCompleted:
		return "completed"
	case TraceCanceled:
		return "canceled"
	case TraceCheckFailed:
		return "check-failed"
	case TracePanicked:
		return "panicked"
	default:
		return "invalid"
	}
}

// StepStatus describes what happened at one expression leaf.
type StepStatus uint8

const (
	StepInvalid StepStatus = iota
	// StepOK means the check was invoked and returned a valid result.
	StepOK
	// StepCanceled means evaluation stopped at this leaf before invoking the
	// check because the context was already canceled. It carries no evidence.
	StepCanceled
	// StepCheckCanceled means the check was invoked and reported cancellation.
	// It carries no evidence.
	StepCheckCanceled
	// StepCheckError means the check was invoked and returned an error or an
	// invalid result. It carries no evidence.
	StepCheckError
	// StepPanicked means the check was invoked and panicked. It carries no
	// evidence.
	StepPanicked
)

func (s StepStatus) String() string {
	switch s {
	case StepOK:
		return "ok"
	case StepCanceled:
		return "canceled"
	case StepCheckCanceled:
		return "check-canceled"
	case StepCheckError:
		return "check-error"
	case StepPanicked:
		return "panicked"
	default:
		return "invalid"
	}
}

// Invoked reports whether the check was actually entered. Canceled-at-leaf
// steps are stop markers rather than executed checks.
func (s StepStatus) Invoked() bool {
	return s == StepOK || s == StepCheckCanceled || s == StepCheckError || s == StepPanicked
}

// Step is one leaf occurrence in an execution trace.
//
// Node is the exact occurrence site in the policy expression, not merely the
// rule ID. For a shared expression node, each parent position has a different
// Node value. This prevents evidence from one occurrence being presented as
// evidence that another occurrence ran.
type Step struct {
	Seq         int
	Node        string
	RuleID      string
	RuleVersion string
	Status      StepStatus
	Verdict     Verdict
	Evidence    Evidence
	Detail      string
}

// Invoked reports whether the check for this step was actually entered.
func (s Step) Invoked() bool {
	return s.Status.Invoked()
}

// Trace is the sealed record of one evaluation.
type Trace struct {
	ProgramVersion string
	ProgramDigest  Digest
	Status         TraceStatus
	Verdict        Verdict
	Steps          []Step
	Seal           Digest
}

// Clone returns a deep copy of the trace.
func (t Trace) Clone() Trace {
	out := t
	out.Steps = append([]Step(nil), t.Steps...)
	return out
}

// ExecutedRules returns the rule IDs whose checks were actually invoked, in
// execution order. Repeated shared sites appear repeatedly; canceled-before-
// invocation stop markers are omitted.
func (t Trace) ExecutedRules() []string {
	var ids []string
	for _, step := range t.InvokedSteps() {
		ids = append(ids, step.RuleID)
	}
	return ids
}

// InvokedSteps returns a copy of the steps whose checks were actually entered.
// It excludes cancellation stop markers recorded before a check invocation.
func (t Trace) InvokedSteps() []Step {
	var steps []Step
	for _, step := range t.Steps {
		if step.Invoked() {
			steps = append(steps, step)
		}
	}
	return steps
}

func traceStatusFor(step StepStatus) TraceStatus {
	switch step {
	case StepCanceled, StepCheckCanceled:
		return TraceCanceled
	case StepCheckError:
		return TraceCheckFailed
	case StepPanicked:
		return TracePanicked
	default:
		return TraceInvalid
	}
}

func sealTrace(programDigest Digest, trace Trace) Digest {
	chain := make([]byte, sha256Size)
	for i, step := range trace.Steps {
		e := newEncoder("netsec.step.v1")
		e.digest(programDigest)
		e.bytes(chain)
		e.uint64(uint64(i))
		e.uint64(uint64(int64(step.Seq)))
		e.string(step.Node)
		e.string(step.RuleID)
		e.string(step.RuleVersion)
		e.byte(byte(step.Status))
		e.byte(byte(step.Verdict))
		e.digest(step.Evidence.Digest)
		e.string(step.Evidence.Summary)
		e.string(step.Detail)
		sum := e.sum()
		chain = sum[:]
	}

	e := newEncoder("netsec.trace.v1")
	e.digest(programDigest)
	e.string(trace.ProgramVersion)
	e.byte(byte(trace.Status))
	e.byte(byte(trace.Verdict))
	e.uint64(uint64(len(trace.Steps)))
	e.bytes(chain)
	return e.sum()
}

const sha256Size = 32

func formatStep(s Step) string {
	var b strings.Builder
	fmt.Fprintf(&b, "step %d node=%s rule=%s@%s status=%s verdict=%s",
		s.Seq, s.Node, s.RuleID, s.RuleVersion, s.Status, s.Verdict)
	if !s.Evidence.Digest.IsZero() {
		fmt.Fprintf(&b, " evidence=%s", s.Evidence.Digest)
	}
	if s.Detail != "" {
		fmt.Fprintf(&b, " detail=%q", s.Detail)
	}
	return b.String()
}
