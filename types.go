// Package systemd implements a deterministic, in-memory transaction solver
// for a small but complete subset of systemd unit dependencies.
//
// The package does not talk to a running systemd instance. It expands a
// requested operation into jobs, resolves job conflicts according to the
// transaction mode, and produces an ordered execution plan.
package systemd

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ActiveState is an optional, more expressive form of Unit.Active. When it is
// set to active or reloading the unit is considered active.
type ActiveState string

const (
	StateActive       ActiveState = "active"
	StateReloading    ActiveState = "reloading"
	StateInactive     ActiveState = "inactive"
	StateFailed       ActiveState = "failed"
	StateActivating   ActiveState = "activating"
	StateDeactivating ActiveState = "deactivating"
	StateMaintenance  ActiveState = "maintenance"
)

// Unit is a snapshot of one unit and its dependency declarations.
//
// PartOf is stored in systemd's declaration direction: a.PartOf contains b
// means that a is part of b, and stopping or restarting b propagates to a.
// Before and After describe ordering only. They never imply a requirement,
// and a requirement never implies ordering.
type Unit struct {
	Name string

	// Active is the snapshot used by Requisite and for deciding whether a
	// start/stop job is necessary. State can be used instead of Active.
	Active bool
	State  ActiveState

	Requires  []string
	Requisite []string
	Wants     []string
	BindsTo   []string
	PartOf    []string
	Conflicts []string

	Before []string
	After  []string

	IgnoreOnIsolate bool
}

// IsActive reports whether the unit is active in the supplied snapshot.
// Active is the primary signal. State makes the convenience values "active"
// and "reloading" work as well.
func (u Unit) IsActive() bool {
	return u.Active || u.State == StateActive || u.State == StateReloading
}

// Running is an alias for IsActive.
func (u Unit) Running() bool { return u.IsActive() }

// Graph is a unit dependency graph. Units is exported to make small test
// fixtures convenient; callers should use AddUnit when duplicate detection is
// important.
type Graph struct {
	Units map[string]*Unit
}

// NewGraph constructs a graph from value snapshots. Later entries with the
// same name replace earlier entries.
func NewGraph(units ...Unit) *Graph {
	g := &Graph{Units: make(map[string]*Unit, len(units))}
	for i := range units {
		u := units[i]
		if strings.TrimSpace(u.Name) == "" {
			continue
		}
		copy := cloneUnit(u)
		g.Units[copy.Name] = &copy
	}
	return g
}

// NewGraphFromUnits is the slice form of NewGraph.
func NewGraphFromUnits(units []Unit) *Graph { return NewGraph(units...) }

// NewGraphPtr is convenient when a fixture already uses pointers.
func NewGraphPtr(units ...*Unit) *Graph {
	g := &Graph{Units: make(map[string]*Unit, len(units))}
	for _, u := range units {
		if u == nil || strings.TrimSpace(u.Name) == "" {
			continue
		}
		copy := cloneUnit(*u)
		g.Units[copy.Name] = &copy
	}
	return g
}

// AddUnit adds a unit value. It rejects empty and duplicate unit names.
func (g *Graph) AddUnit(u Unit) error {
	if g.Units == nil {
		g.Units = make(map[string]*Unit)
	}
	if strings.TrimSpace(u.Name) == "" {
		return errors.New("systemd: unit name must not be empty")
	}
	if _, exists := g.Units[u.Name]; exists {
		return fmt.Errorf("systemd: duplicate unit %q", u.Name)
	}
	copy := cloneUnit(u)
	g.Units[u.Name] = &copy
	return nil
}

// AddUnitPtr is the pointer form of AddUnit.
func (g *Graph) AddUnitPtr(u *Unit) error {
	if u == nil {
		return errors.New("systemd: nil unit")
	}
	return g.AddUnit(*u)
}

// SetActive updates the active snapshot of an existing unit.
func (g *Graph) SetActive(name string, active bool) error {
	u, ok := g.Units[name]
	if !ok {
		return fmt.Errorf("systemd: unit %q not found", name)
	}
	u.Active = active
	if active {
		u.State = StateActive
	} else if u.State == StateActive || u.State == StateReloading {
		u.State = StateInactive
	}
	return nil
}

// Unit returns a pointer to the named unit.
func (g *Graph) Unit(name string) (*Unit, bool) {
	if g == nil {
		return nil, false
	}
	u, ok := g.Units[name]
	return u, ok
}

// Names returns the unit names in deterministic order.
func (g *Graph) Names() []string {
	if g == nil {
		return nil
	}
	names := make([]string, 0, len(g.Units))
	for name := range g.Units {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Clone returns a deep copy of the graph.
func (g *Graph) Clone() *Graph {
	c := &Graph{Units: make(map[string]*Unit)}
	if g == nil {
		return c
	}
	for name, u := range g.Units {
		if u == nil {
			continue
		}
		copy := cloneUnit(*u)
		c.Units[name] = &copy
	}
	return c
}

func cloneUnit(u Unit) Unit {
	u.Requires = append([]string(nil), u.Requires...)
	u.Requisite = append([]string(nil), u.Requisite...)
	u.Wants = append([]string(nil), u.Wants...)
	u.BindsTo = append([]string(nil), u.BindsTo...)
	u.PartOf = append([]string(nil), u.PartOf...)
	u.Conflicts = append([]string(nil), u.Conflicts...)
	u.Before = append([]string(nil), u.Before...)
	u.After = append([]string(nil), u.After...)
	return u
}

// Action is a requested transaction operation.
type Action string

const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	ActionIsolate Action = "isolate"
)

// TransactionMode controls how start/stop conflicts are handled.
type TransactionMode string

const (
	// ModeFail aborts the whole transaction on a start/stop conflict.
	ModeFail TransactionMode = "fail"
	// ModeReplace may remove one conflicting non-anchor/non-core job.
	ModeReplace TransactionMode = "replace"
	// ModeReplaceIrreversibly applies replace arbitration and marks the
	// retained jobs irreversible for a subsequent caller/scheduler.
	ModeReplaceIrreversibly TransactionMode = "replace-irreversibly"
)

// Short aliases are provided for callers that prefer mode-free names.
const (
	Fail                = ModeFail
	Replace             = ModeReplace
	ReplaceIrreversibly = ModeReplaceIrreversibly
)

// Request describes one requested operation. Action and Type are aliases;
// Action wins when both are set. An empty Mode means ModeReplace.
type Request struct {
	Unit   string
	Action Action
	Type   Action
	Mode   TransactionMode
}

func (r Request) action() Action {
	if r.Action != "" {
		return r.Action
	}
	return r.Type
}

func (r Request) normalized() Request {
	r.Action = r.action()
	r.Type = r.Action
	if r.Mode == "" {
		r.Mode = ModeReplace
	}
	return r
}

// Start, Stop, Restart, and Isolate are request constructors.
func Start(unit string, mode ...TransactionMode) Request {
	return Request{Unit: unit, Action: ActionStart, Mode: requestMode(mode)}
}

func Stop(unit string, mode ...TransactionMode) Request {
	return Request{Unit: unit, Action: ActionStop, Mode: requestMode(mode)}
}

func Restart(unit string, mode ...TransactionMode) Request {
	return Request{Unit: unit, Action: ActionRestart, Mode: requestMode(mode)}
}

func Isolate(unit string, mode ...TransactionMode) Request {
	return Request{Unit: unit, Action: ActionIsolate, Mode: requestMode(mode)}
}

func requestMode(mode []TransactionMode) TransactionMode {
	if len(mode) == 0 || mode[0] == "" {
		return ModeReplace
	}
	return mode[0]
}

// JobType is the operation represented by a retained transaction job.
type JobType string

const (
	JobStart   JobType = "start"
	JobStop    JobType = "stop"
	JobRestart JobType = "restart"
)

// JobKind is an alias for JobType.
type JobKind = JobType

// Job is one retained, merged transaction job. A transaction contains at
// most one job per unit.
type Job struct {
	Unit string
	Type JobType

	// Anchor marks a directly requested job. Anchor jobs are never removed by
	// conflict arbitration in replace modes.
	Anchor bool
	// Core marks a hard requirement (Requires, Requisite, BindsTo) or an
	// anchor. Two conflicting core jobs are irreducible.
	Core bool
	// Irreversible is set for retained jobs in ModeReplaceIrreversibly.
	Irreversible bool

	Reason  string
	Reasons []string
	Origins []string
}

func (j Job) String() string {
	return fmt.Sprintf("%s(%s)", j.Unit, j.Type)
}

// JobRef identifies a unit job.
type JobRef struct {
	Unit string
	Type JobType
}

func (r JobRef) String() string { return fmt.Sprintf("%s(%s)", r.Unit, r.Type) }

// DependencyKind names an expanded dependency edge.
type DependencyKind string

const (
	DependencyRequires  DependencyKind = "Requires"
	DependencyRequisite DependencyKind = "Requisite"
	DependencyWants     DependencyKind = "Wants"
	DependencyBindsTo   DependencyKind = "BindsTo"
	DependencyPartOf    DependencyKind = "PartOf"
	DependencyConflict  DependencyKind = "Conflicts"
)

// Dependency records an expanded dependency edge. SatisfiedByActive means no
// start job was needed because the target was already active.
type Dependency struct {
	From              string
	To                string
	Kind              DependencyKind
	Hard              bool
	SatisfiedByActive bool
}

// JobPhase identifies the ordering phase in which an edge is applied.
type JobPhase string

const (
	PhaseStart JobPhase = "start"
	PhaseStop  JobPhase = "stop"
)

// OrderRelation records the declaration orientation of an ordering edge.
type OrderRelation string

const (
	RelationBefore OrderRelation = "before"
	RelationAfter  OrderRelation = "after"
)

// OrderEdge is a retained directed ordering edge in execution direction.
// From must run before To in the named phase.
type OrderEdge struct {
	From     string        `json:"from"`
	To       string        `json:"to"`
	Phase    JobPhase      `json:"phase"`
	Relation OrderRelation `json:"relation"`
	Source   string        `json:"source,omitempty"`
}

func (e OrderEdge) key() string {
	return e.From + "\x00" + e.To + "\x00" + string(e.Phase)
}

func (e OrderEdge) String() string {
	return fmt.Sprintf("%s -> %s (%s/%s)", e.From, e.To, e.Phase, e.Relation)
}

// Cycle describes one ordering cycle found and broken by the solver.
// Nodes are listed once in cycle order; Edges contains all cycle edges,
// including the closing last->first edge.
type Cycle struct {
	Phase    JobPhase    `json:"phase"`
	Nodes    []string    `json:"nodes"`
	Edges    []OrderEdge `json:"edges"`
	CutEdges []OrderEdge `json:"cut_edges"`
}

// ExecutionStep is an executable stop or start operation. Restart jobs
// produce a stop step followed by a start step.
type ExecutionStep struct {
	Unit      string   `json:"unit"`
	Operation JobType  `json:"operation"`
	Phase     JobPhase `json:"phase"`
}

// Plan is a fully expanded, conflict-resolved execution plan.
type Plan struct {
	Request  Request
	Requests []Request
	Mode     TransactionMode

	Jobs         []Job
	Order        []string
	StartOrder   []string
	StopOrder    []string
	Steps        []ExecutionStep
	Edges        []OrderEdge
	StartEdges   []OrderEdge
	StopEdges    []OrderEdge
	Dependencies []Dependency

	// Cycles lists each ordering cycle used for a cut. CutEdges is the
	// deduplicated union of all removed ordering edges.
	Cycles   []Cycle
	CutEdges []OrderEdge

	RemovedJobs []Job
	Warnings    []string
}

// Job returns the retained job for a unit.
func (p *Plan) Job(unit string) (Job, bool) {
	if p == nil {
		return Job{}, false
	}
	for _, j := range p.Jobs {
		if j.Unit == unit {
			return j, true
		}
	}
	return Job{}, false
}

// HasJob reports whether the plan contains any job for unit.
func (p *Plan) HasJob(unit string) bool {
	_, ok := p.Job(unit)
	return ok
}

// HasEdge reports whether the retained job DAG contains from -> to.
func (p *Plan) HasEdge(from, to string) bool {
	if p == nil {
		return false
	}
	for _, e := range p.Edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

// Adjacency returns the final ordering DAG as an adjacency map.
func (p *Plan) Adjacency() map[string][]string {
	adj := make(map[string][]string)
	if p == nil {
		return adj
	}
	seen := make(map[string]map[string]bool)
	for _, e := range p.Edges {
		if seen[e.From] == nil {
			seen[e.From] = make(map[string]bool)
		}
		if !seen[e.From][e.To] {
			seen[e.From][e.To] = true
			adj[e.From] = append(adj[e.From], e.To)
		}
	}
	for from := range adj {
		sort.Strings(adj[from])
	}
	return adj
}

// ErrorKind classifies transaction failures.
type ErrorKind string

const (
	ErrorInvalidRequest      ErrorKind = "invalid-request"
	ErrorMissingUnit         ErrorKind = "missing-unit"
	ErrorRequisiteInactive   ErrorKind = "requisite-inactive"
	ErrorTransactionConflict ErrorKind = "transaction-conflict"
	ErrorUnsatisfiedConflict ErrorKind = "unsatisfied-conflict"
)

var (
	ErrInvalidRequest      = errors.New("systemd: invalid transaction request")
	ErrMissingUnit         = errors.New("systemd: unit not found")
	ErrRequisiteInactive   = errors.New("systemd: requisite unit is not active")
	ErrTransactionConflict = errors.New("systemd: transaction job conflict")
	ErrUnsatisfiedConflict = errors.New("systemd: conflicting start jobs cannot be satisfied")
)

// TransactionError carries structured details for a rejected transaction.
type TransactionError struct {
	Kind    ErrorKind
	Unit    string
	Related string
	Message string
	Err     error
}

func (e *TransactionError) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := e.Message
	if msg == "" {
		msg = string(e.Kind)
	}
	switch {
	case e.Unit != "" && e.Related != "":
		return fmt.Sprintf("systemd: %s: %s (unit=%s, related=%s)", e.Kind, msg, e.Unit, e.Related)
	case e.Unit != "":
		return fmt.Sprintf("systemd: %s: %s (unit=%s)", e.Kind, msg, e.Unit)
	default:
		return fmt.Sprintf("systemd: %s: %s", e.Kind, msg)
	}
}

func (e *TransactionError) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.Err != nil {
		return e.Err
	}
	switch e.Kind {
	case ErrorMissingUnit:
		return ErrMissingUnit
	case ErrorRequisiteInactive:
		return ErrRequisiteInactive
	case ErrorTransactionConflict:
		return ErrTransactionConflict
	case ErrorUnsatisfiedConflict:
		return ErrUnsatisfiedConflict
	default:
		return ErrInvalidRequest
	}
}

func transactionError(kind ErrorKind, unit, related, format string, args ...any) error {
	return &TransactionError{
		Kind:    kind,
		Unit:    unit,
		Related: related,
		Message: fmt.Sprintf(format, args...),
	}
}
