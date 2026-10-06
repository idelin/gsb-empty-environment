// Package dagexec implements a DAG task execution model with three join
// (launch) conditions: AllSuccess, AnySuccess and AllTerminal.
//
// A task is launched at most once, exactly when its condition becomes
// satisfied. When the condition becomes impossible to satisfy (e.g. a
// parent failed or was cancelled), the task is skipped immediately instead
// of waiting forever. Every launch/skip decision records a reason whose
// causes reference the parent outcomes (failed and cancelled are recorded
// separately), forming an inspectable reason chain via Explain.
package dagexec

import (
	"fmt"
	"sort"
	"strings"
)

// State is the lifecycle state of a task.
type State int

const (
	Pending State = iota
	Running
	Succeeded
	Failed
	Cancelled
	Skipped
)

func (s State) String() string {
	switch s {
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

// Terminal reports whether the state is a final state. Skipped counts as
// terminal so that downstream AllTerminal joins can still fire.
func (s State) Terminal() bool {
	switch s {
	case Succeeded, Failed, Cancelled, Skipped:
		return true
	}
	return false
}

// Condition is the join/launch condition of a task over its parents.
type Condition int

const (
	// AllSuccess launches the task once every parent has succeeded.
	AllSuccess Condition = iota
	// AnySuccess launches the task once at least one parent has succeeded.
	AnySuccess
	// AllTerminal launches the task once every parent has reached a
	// terminal state, regardless of outcome.
	AllTerminal
)

func (c Condition) String() string {
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

// Decision is the outcome of evaluating a join condition.
type Decision int

const (
	// Wait means the condition is neither satisfied nor impossible yet.
	Wait Decision = iota
	// Launch means the condition is satisfied.
	Launch
	// Skip means the condition can never be satisfied anymore.
	Skip
)

func (d Decision) String() string {
	switch d {
	case Wait:
		return "wait"
	case Launch:
		return "launch"
	case Skip:
		return "skip"
	}
	return "unknown"
}

// Cause records one parent's contribution to a launch/skip decision.
// Failed and Cancelled parents are recorded as distinct states.
type Cause struct {
	Parent string
	State  State
}

// Reason explains why a task was launched or skipped. Causes are derived
// from the current parent states, sorted by parent ID; they stabilize once
// all parents are terminal, which makes the reason chain independent of
// terminal-event ordering.
type Reason struct {
	Decision Decision
	Cond     Condition
	Causes   []Cause
}

func (r Reason) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s]", r.Decision, r.Cond)
	if len(r.Causes) > 0 {
		b.WriteString(": ")
		for i, c := range r.Causes {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s=%s", c.Parent, c.State)
		}
	}
	return b.String()
}

// evaluate is the pure core of the model: given a condition and the current
// parent states (sorted by parent ID), it decides whether to launch, skip or
// keep waiting, and returns the canonical causes for launch/skip.
//
// A task with no parents is a root and always launches.
func evaluate(cond Condition, parents []Cause) (Decision, []Cause) {
	if len(parents) == 0 {
		return Launch, nil
	}
	switch cond {
	case AllSuccess:
		var unsuccessful []Cause
		for _, p := range parents {
			if p.State == Succeeded {
				continue
			}
			if p.State.Terminal() {
				unsuccessful = append(unsuccessful, p)
			}
		}
		if len(unsuccessful) > 0 {
			// A parent already failed/was cancelled/was skipped: the
			// condition can never hold anymore, skip immediately.
			return Skip, unsuccessful
		}
		for _, p := range parents {
			if p.State != Succeeded {
				return Wait, nil
			}
		}
		return Launch, parents
	case AnySuccess:
		var succeeded []Cause
		allTerminal := true
		for _, p := range parents {
			if p.State == Succeeded {
				succeeded = append(succeeded, p)
			}
			if !p.State.Terminal() {
				allTerminal = false
			}
		}
		if len(succeeded) > 0 {
			return Launch, succeeded
		}
		if allTerminal {
			// Every parent is terminal and none succeeded: no success
			// can ever arrive, skip immediately.
			return Skip, parents
		}
		return Wait, nil
	case AllTerminal:
		for _, p := range parents {
			if !p.State.Terminal() {
				return Wait, nil
			}
		}
		return Launch, parents
	}
	return Wait, nil
}

type node struct {
	id       string
	cond     Condition
	parents  []string // sorted, deduplicated
	state    State
	decision Decision
}

// Scheduler tracks task states and makes launch/skip decisions. It is not
// safe for concurrent use; serialize Complete calls (e.g. from a single
// event loop).
type Scheduler struct {
	nodes  map[string]*node
	order  []string // topological order, deterministic
	frozen bool
}

// New returns an empty Scheduler.
func New() *Scheduler {
	return &Scheduler{nodes: make(map[string]*node)}
}

// Add registers a task. parents may be empty (root task). Add panics on a
// duplicate ID or on calls after Start.
func (s *Scheduler) Add(id string, cond Condition, parents ...string) {
	if s.frozen {
		panic("dagexec: Add after Start")
	}
	if _, dup := s.nodes[id]; dup {
		panic("dagexec: duplicate task " + id)
	}
	ps := append([]string(nil), parents...)
	sort.Strings(ps)
	uniq := ps[:0]
	for i, p := range ps {
		if i == 0 || p != ps[i-1] {
			uniq = append(uniq, p)
		}
	}
	s.nodes[id] = &node{id: id, cond: cond, parents: uniq}
}

// freeze validates the graph and computes a deterministic topological
// order. It is idempotent.
func (s *Scheduler) freeze() error {
	if s.frozen {
		return nil
	}
	children := make(map[string][]string, len(s.nodes))
	indegree := make(map[string]int, len(s.nodes))
	for id, n := range s.nodes {
		indegree[id] = len(n.parents)
		for _, p := range n.parents {
			if _, ok := s.nodes[p]; !ok {
				return fmt.Errorf("dagexec: task %q has unknown parent %q", id, p)
			}
			children[p] = append(children[p], id)
		}
	}
	var ready []string
	for id, d := range indegree {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	var order []string
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, c := range children[id] {
			indegree[c]--
			if indegree[c] == 0 {
				// insert keeping ready sorted for determinism
				i := sort.SearchStrings(ready, c)
				ready = append(ready, "")
				copy(ready[i+1:], ready[i:])
				ready[i] = c
			}
		}
	}
	if len(order) != len(s.nodes) {
		return fmt.Errorf("dagexec: cycle detected (%d of %d tasks ordered)", len(order), len(s.nodes))
	}
	s.order = order
	s.frozen = true
	return nil
}

// evaluateLocked scans pending tasks in topological order and applies
// launch/skip decisions until a fixpoint. Skips cascade within the scan
// (a skipped parent may make a child's condition impossible or, for
// AllTerminal, satisfied once the other parents are terminal). Launched
// tasks are not terminal, so they never unblock anything in the same scan.
// Only the decision is recorded; the reason's causes are derived lazily
// from parent states so they are independent of terminal-event ordering.
func (s *Scheduler) evaluateLocked() (launched []string) {
	changed := true
	for changed {
		changed = false
		for _, id := range s.order {
			n := s.nodes[id]
			if n.state != Pending {
				continue
			}
			ps := make([]Cause, len(n.parents))
			for i, p := range n.parents {
				ps[i] = Cause{Parent: p, State: s.nodes[p].state}
			}
			dec, _ := evaluate(n.cond, ps)
			switch dec {
			case Launch:
				n.state = Running
				n.decision = Launch
				launched = append(launched, id)
			case Skip:
				n.state = Skipped
				n.decision = Skip
				changed = true
			}
		}
	}
	return launched
}

// Start freezes the graph and launches all root tasks. It returns the IDs
// of the tasks to run (each root exactly once; a second call returns nil).
func (s *Scheduler) Start() ([]string, error) {
	if err := s.freeze(); err != nil {
		return nil, err
	}
	return s.evaluateLocked(), nil
}

// Complete records the terminal outcome of a running task and returns the
// IDs of any tasks that became ready to launch as a result. outcome must be
// Succeeded, Failed or Cancelled. Each task is returned by Start/Complete
// at most once in the whole run.
func (s *Scheduler) Complete(id string, outcome State) ([]string, error) {
	n, ok := s.nodes[id]
	if !ok {
		return nil, fmt.Errorf("dagexec: unknown task %q", id)
	}
	switch outcome {
	case Succeeded, Failed, Cancelled:
	default:
		return nil, fmt.Errorf("dagexec: invalid outcome %s for task %q", outcome, id)
	}
	if n.state != Running {
		return nil, fmt.Errorf("dagexec: task %q is %s, not running", id, n.state)
	}
	n.state = outcome
	return s.evaluateLocked(), nil
}

// State returns the current state of a task.
func (s *Scheduler) State(id string) State {
	return s.nodes[id].state
}

// Reason returns the launch/skip reason of a task. The decision is the one
// recorded when the task left Pending; the causes are derived from the
// current parent states and stabilize once all parents are terminal. The
// zero Reason (Decision == Wait) is returned for undecided tasks.
func (s *Scheduler) Reason(id string) Reason {
	n := s.nodes[id]
	r := Reason{Decision: n.decision, Cond: n.cond}
	if n.decision == Launch || n.decision == Skip {
		ps := make([]Cause, len(n.parents))
		for i, p := range n.parents {
			ps[i] = Cause{Parent: p, State: s.nodes[p].state}
		}
		_, r.Causes = evaluate(n.cond, ps)
	}
	return r
}

// Condition returns the join condition of a task.
func (s *Scheduler) Condition(id string) Condition {
	return s.nodes[id].cond
}

// Parents returns the sorted parent IDs of a task.
func (s *Scheduler) Parents(id string) []string {
	return append([]string(nil), s.nodes[id].parents...)
}

// IDs returns all task IDs in deterministic topological order.
func (s *Scheduler) IDs() []string {
	if !s.frozen {
		ids := make([]string, 0, len(s.nodes))
		for id := range s.nodes {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return ids
	}
	return append([]string(nil), s.order...)
}

// Explain renders the reason chain of a task: its own decision and,
// recursively, the parents that caused it. Failed and cancelled parents
// appear as distinct lines.
func (s *Scheduler) Explain(id string) string {
	var b strings.Builder
	s.explain(&b, id, 0)
	return b.String()
}

func (s *Scheduler) explain(b *strings.Builder, id string, depth int) {
	n := s.nodes[id]
	fmt.Fprintf(b, "%s%s: %s", strings.Repeat("  ", depth), id, n.state)
	r := s.Reason(id)
	if r.Decision == Skip || len(r.Causes) > 0 {
		fmt.Fprintf(b, " (%s)", r)
	}
	b.WriteByte('\n')
	for _, c := range r.Causes {
		s.explain(b, c.Parent, depth+1)
	}
}

// Clone returns a deep copy of the scheduler, useful for what-if
// exploration of alternative terminal orderings.
func (s *Scheduler) Clone() *Scheduler {
	c := &Scheduler{
		nodes:  make(map[string]*node, len(s.nodes)),
		order:  append([]string(nil), s.order...),
		frozen: s.frozen,
	}
	for id, n := range s.nodes {
		nn := *n
		nn.parents = append([]string(nil), n.parents...)
		c.nodes[id] = &nn
	}
	return c
}
