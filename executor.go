// Package dagexec executes a DAG of tasks gated by three kinds of join
// (start) conditions: AllSuccess, AnySuccess and AllTerminal.
//
// Semantics:
//   - AllSuccess: start once every parent has succeeded; the condition
//     becomes impossible as soon as any parent fails, is cancelled or is
//     skipped, and the task is skipped immediately at that moment.
//   - AnySuccess: start once at least one parent has succeeded; the
//     condition becomes impossible once every parent is terminal and none
//     succeeded, and the task is skipped immediately at that moment.
//   - AllTerminal: start once every parent reached a terminal state
//     (succeeded, failed, cancelled or skipped); this condition can never
//     become impossible, so the task never skips.
//
// Every start/skip decision records its reasons as a list of causes that
// reference the parents' outcomes, with failure and cancellation recorded
// as distinct outcomes. Skips propagate downstream, so the reason of a
// skipped task forms a chain back to the tasks that actually ran and
// failed or were cancelled (see RootCauses and Explain).
package dagexec

import (
	"fmt"
	"strings"
	"sync"
)

// Outcome is the lifecycle state of a task.
type Outcome int

const (
	Pending Outcome = iota
	Running
	Succeeded
	Failed
	Cancelled
	Skipped
)

// Terminal reports whether the outcome is final.
func (o Outcome) Terminal() bool { return o >= Succeeded }

// Success reports whether the outcome is a success.
func (o Outcome) Success() bool { return o == Succeeded }

func (o Outcome) String() string {
	switch o {
	case Pending:
		return "pending"
	case Running:
		return "running"
	case Succeeded:
		return "succeeded"
	case Failed:
		return "failed"
	case Cancelled:
		return "cancelled"
	case Skipped:
		return "skipped"
	}
	return "unknown"
}

// CondType is the join condition that gates a task's start.
type CondType int

const (
	// AllSuccess starts the task once every parent has succeeded.
	AllSuccess CondType = iota
	// AnySuccess starts the task once at least one parent has succeeded.
	AnySuccess
	// AllTerminal starts the task once every parent is terminal,
	// regardless of how it terminated.
	AllTerminal
)

func (c CondType) String() string {
	switch c {
	case AllSuccess:
		return "all-success"
	case AnySuccess:
		return "any-success"
	case AllTerminal:
		return "all-terminal"
	}
	return "unknown"
}

// Cause attributes a decision to one parent's outcome. Failed and
// Cancelled are recorded as distinct outcomes.
type Cause struct {
	Parent  string
	Outcome Outcome
}

// DecisionKind is the kind of a task's start decision.
type DecisionKind int

const (
	// Undecided means the task is still waiting on its parents.
	Undecided DecisionKind = iota
	// Started means the task's condition was satisfied and it started.
	Started
	// Skip means the task's condition became impossible and it was
	// skipped without ever running.
	Skip
)

func (k DecisionKind) String() string {
	switch k {
	case Undecided:
		return "undecided"
	case Started:
		return "started"
	case Skip:
		return "skipped"
	}
	return "unknown"
}

// Decision records why a task started or was skipped.
type Decision struct {
	Kind DecisionKind
	// Reasons are the parent outcomes that justified the decision at the
	// moment it was taken.
	Reasons []Cause
	// Trigger is the parent whose terminal event made the decision
	// possible ("" for parentless tasks).
	Trigger string
	// Seq is the executor-wide sequence number of the decision.
	Seq int
}

// Task is a node of the DAG.
type Task struct {
	Name     string
	Cond     CondType
	Parents  []string
	Outcome  Outcome
	Decision Decision

	starts  int
	termSeq int
}

// Starts reports how many times the task has started. It is 1 for a
// started task and 0 otherwise; the executor guarantees it never
// exceeds 1 (unique start).
func (t *Task) Starts() int { return t.starts }

// Executor holds the DAG and drives the decisions. It is safe for
// concurrent use.
type Executor struct {
	mu    sync.Mutex
	tasks map[string]*Task
	order []*Task
	seq   int
}

// NewExecutor returns an empty executor.
func NewExecutor() *Executor {
	return &Executor{tasks: make(map[string]*Task)}
}

// Add registers a task. Parents must have been registered before, which
// makes cycles impossible by construction. A parentless task starts
// immediately. Add returns an error on duplicate names or unknown
// parents.
func (e *Executor) Add(name string, cond CondType, parents ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.tasks[name]; ok {
		return fmt.Errorf("dagexec: duplicate task %q", name)
	}
	for _, p := range parents {
		if _, ok := e.tasks[p]; !ok {
			return fmt.Errorf("dagexec: task %q: unknown parent %q", name, p)
		}
	}
	t := &Task{Name: name, Cond: cond, Parents: append([]string(nil), parents...), Outcome: Pending}
	e.tasks[name] = t
	e.order = append(e.order, t)
	if len(parents) == 0 {
		e.startLocked(t, nil, "")
	}
	return nil
}

// Complete marks a running task with a terminal outcome (Succeeded,
// Failed or Cancelled) and wakes up its downstream tasks, starting or
// skipping them as their conditions dictate. Skips cascade in the same
// call. It returns the names of the tasks that started, in registration
// order.
func (e *Executor) Complete(name string, outcome Outcome) ([]string, error) {
	if !outcome.Terminal() || outcome == Skipped {
		return nil, fmt.Errorf("dagexec: invalid completion outcome %s", outcome)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tasks[name]
	if !ok {
		return nil, fmt.Errorf("dagexec: unknown task %q", name)
	}
	if t.Outcome != Running {
		return nil, fmt.Errorf("dagexec: task %q is %s, not running", name, t.Outcome)
	}
	t.Outcome = outcome
	e.seq++
	t.termSeq = e.seq
	var started []string
	e.propagateLocked(t, &started)
	return started, nil
}

// Task returns a copy of the named task, for inspection.
func (e *Executor) Task(name string) (Task, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tasks[name]
	if !ok {
		return Task{}, false
	}
	cp := *t
	cp.Parents = append([]string(nil), t.Parents...)
	cp.Decision.Reasons = append([]Cause(nil), t.Decision.Reasons...)
	return cp, true
}

func (e *Executor) propagateLocked(t *Task, started *[]string) {
	for _, c := range e.order {
		if c.Decision.Kind != Undecided || !dependsOn(c, t.Name) {
			continue
		}
		e.evaluateLocked(c, t.Name, started)
	}
}

func dependsOn(t *Task, parent string) bool {
	for _, p := range t.Parents {
		if p == parent {
			return true
		}
	}
	return false
}

func (e *Executor) evaluateLocked(t *Task, trigger string, started *[]string) {
	outcomes := make([]Outcome, len(t.Parents))
	for i, p := range t.Parents {
		outcomes[i] = e.tasks[p].Outcome
	}
	kind, reasons := decide(t.Cond, t.Parents, outcomes)
	switch kind {
	case Started:
		e.startLocked(t, reasons, trigger)
		*started = append(*started, t.Name)
	case Skip:
		e.skipLocked(t, reasons, trigger)
		// The skip is itself a terminal event: it may doom or unblock
		// downstream tasks in the same cascade.
		e.propagateLocked(t, started)
	}
}

// decide is the pure core of the join semantics: given the parents'
// current outcomes it returns the decision to take right now, or
// Undecided to keep waiting. Both "satisfied" and "impossible" are
// monotone: once either holds it keeps holding, so deciding at the
// first event where it holds is both correct and as early as possible.
func decide(cond CondType, parents []string, outcomes []Outcome) (DecisionKind, []Cause) {
	switch cond {
	case AllSuccess:
		var bad []Cause
		for i, o := range outcomes {
			if o.Terminal() && !o.Success() {
				bad = append(bad, Cause{parents[i], o})
			}
		}
		if len(bad) > 0 {
			return Skip, bad
		}
		for _, o := range outcomes {
			if o != Succeeded {
				return Undecided, nil
			}
		}
		return Started, allCauses(parents, outcomes)
	case AnySuccess:
		var ok []Cause
		allTerminal := true
		for i, o := range outcomes {
			if o == Succeeded {
				ok = append(ok, Cause{parents[i], o})
			}
			if !o.Terminal() {
				allTerminal = false
			}
		}
		if len(ok) > 0 {
			return Started, ok
		}
		if allTerminal {
			return Skip, allCauses(parents, outcomes)
		}
		return Undecided, nil
	case AllTerminal:
		for _, o := range outcomes {
			if !o.Terminal() {
				return Undecided, nil
			}
		}
		return Started, allCauses(parents, outcomes)
	}
	return Undecided, nil
}

func allCauses(parents []string, outcomes []Outcome) []Cause {
	causes := make([]Cause, len(parents))
	for i := range parents {
		causes[i] = Cause{Parent: parents[i], Outcome: outcomes[i]}
	}
	return causes
}

func (e *Executor) startLocked(t *Task, reasons []Cause, trigger string) {
	e.seq++
	t.Decision = Decision{Kind: Started, Reasons: reasons, Trigger: trigger, Seq: e.seq}
	t.Outcome = Running
	t.starts++
}

func (e *Executor) skipLocked(t *Task, reasons []Cause, trigger string) {
	e.seq++
	t.Decision = Decision{Kind: Skip, Reasons: reasons, Trigger: trigger, Seq: e.seq}
	t.Outcome = Skipped
	t.termSeq = e.seq
}

// RootCauses follows the reason chain of a skipped task down to the
// tasks that actually ran and failed or were cancelled. A skipped task
// never has a success among its root causes.
func (e *Executor) RootCauses(name string) []Cause {
	e.mu.Lock()
	defer e.mu.Unlock()
	var leaves []Cause
	seenTask := make(map[string]bool)
	seenLeaf := make(map[string]bool)
	var walk func(n string)
	walk = func(n string) {
		if seenTask[n] {
			return
		}
		seenTask[n] = true
		t := e.tasks[n]
		if t == nil || t.Decision.Kind != Skip {
			return
		}
		for _, c := range t.Decision.Reasons {
			if c.Outcome == Skipped {
				walk(c.Parent)
			} else if !seenLeaf[c.Parent] {
				seenLeaf[c.Parent] = true
				leaves = append(leaves, c)
			}
		}
	}
	walk(name)
	return leaves
}

// Explain renders the reason chain of a task as an indented tree, e.g.:
//
//	F: skipped
//	  because D was skipped
//	    because A failed
//	    because B was cancelled
func (e *Executor) Explain(name string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var b strings.Builder
	var walk func(n string, depth int)
	walk = func(n string, depth int) {
		t := e.tasks[n]
		if t == nil {
			return
		}
		indent := strings.Repeat("  ", depth)
		switch t.Decision.Kind {
		case Skip:
			fmt.Fprintf(&b, "%s%s: skipped\n", indent, n)
			for _, c := range t.Decision.Reasons {
				p := e.tasks[c.Parent]
				if c.Outcome == Skipped && p != nil && p.Decision.Kind == Skip {
					fmt.Fprintf(&b, "%s  because %s was skipped\n", indent, c.Parent)
					walk(c.Parent, depth+2)
				} else {
					fmt.Fprintf(&b, "%s  because %s %s\n", indent, c.Parent, verb(c.Outcome))
				}
			}
		case Started:
			fmt.Fprintf(&b, "%s%s: started (%s)\n", indent, n, t.Outcome)
		default:
			fmt.Fprintf(&b, "%s%s: %s\n", indent, n, t.Outcome)
		}
	}
	walk(name, 0)
	return b.String()
}

func verb(o Outcome) string {
	switch o {
	case Failed:
		return "failed"
	case Cancelled:
		return "was cancelled"
	case Succeeded:
		return "succeeded"
	case Skipped:
		return "was skipped"
	}
	return o.String()
}
