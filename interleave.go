package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrWitnessNotFound reports that the interleaving explorer could not find a
// state satisfying the goal within its limits.
var ErrWitnessNotFound = errors.New("netsec: no witness found")

// Component identifies one independently published metadata component.
type Component uint8

const (
	ComponentCode Component = iota
	ComponentStackMap
	ComponentExceptionTable
	ComponentCallTargets
)

func (c Component) String() string {
	switch c {
	case ComponentCode:
		return "code"
	case ComponentStackMap:
		return "stackMap"
	case ComponentExceptionTable:
		return "exceptionTable"
	case ComponentCallTargets:
		return "callTargets"
	default:
		return fmt.Sprintf("component(%d)", c)
	}
}

// Op is one scheduler action.
type Op uint8

const (
	OpPublishCode Op = iota
	OpPublishStackMap
	OpPublishExceptionTable
	OpPublishCallTargets
	OpSpawnThread
	OpEnter
	OpLookupStackMap
	OpLookupExceptionTable
	OpLookupCallTarget
	OpExecute
)

func (o Op) String() string {
	switch o {
	case OpPublishCode:
		return "publish(code)"
	case OpPublishStackMap:
		return "publish(stackMap)"
	case OpPublishExceptionTable:
		return "publish(exceptionTable)"
	case OpPublishCallTargets:
		return "publish(callTargets)"
	case OpSpawnThread:
		return "spawn"
	case OpEnter:
		return "enter"
	case OpLookupStackMap:
		return "lookup(stackMap)"
	case OpLookupExceptionTable:
		return "lookup(exceptionTable)"
	case OpLookupCallTarget:
		return "lookup(callTarget)"
	case OpExecute:
		return "execute"
	default:
		return fmt.Sprintf("op(%d)", o)
	}
}

func (o Op) component() (Component, bool) {
	switch o {
	case OpPublishCode:
		return ComponentCode, true
	case OpPublishStackMap:
		return ComponentStackMap, true
	case OpPublishExceptionTable:
		return ComponentExceptionTable, true
	case OpPublishCallTargets:
		return ComponentCallTargets, true
	default:
		return 0, false
	}
}

// Action is a deterministic scheduler event.
type Action struct {
	Actor      string
	Op         Op
	UnitID     string
	Generation uint64
	Thread     string
}

// String renders an action for witness logs.
func (a Action) String() string {
	switch a.Op {
	case OpSpawnThread:
		return fmt.Sprintf("spawn(%s)", a.Thread)
	case OpPublishCode, OpPublishStackMap, OpPublishExceptionTable, OpPublishCallTargets:
		return fmt.Sprintf("%s(g%d)", a.Op.String(), a.Generation)
	case OpEnter, OpLookupStackMap, OpLookupExceptionTable, OpLookupCallTarget, OpExecute:
		return fmt.Sprintf("%s:%s", a.Actor, a.Op.String())
	default:
		return fmt.Sprintf("%s:%s", a.Actor, a.Op.String())
	}
}

// ComponentVersions is the visible generation of every component in the staged
// publisher. The components are updated independently; this is the defect the
// audit search exposes.
type ComponentVersions struct {
	Code           uint64
	StackMap       uint64
	ExceptionTable uint64
	CallTargets    uint64
}

func (v ComponentVersions) get(c Component) uint64 {
	switch c {
	case ComponentCode:
		return v.Code
	case ComponentStackMap:
		return v.StackMap
	case ComponentExceptionTable:
		return v.ExceptionTable
	case ComponentCallTargets:
		return v.CallTargets
	default:
		return 0
	}
}

func (v *ComponentVersions) set(c Component, generation uint64) {
	switch c {
	case ComponentCode:
		v.Code = generation
	case ComponentStackMap:
		v.StackMap = generation
	case ComponentExceptionTable:
		v.ExceptionTable = generation
	case ComponentCallTargets:
		v.CallTargets = generation
	}
}

// ComponentLookup is the metadata a model thread observed for one component.
type ComponentLookup struct {
	Component  Component
	Generation uint64
	Digest     Digest
	OK         bool
}

// ModelThread is one simulator thread.
type ModelThread struct {
	ID string

	Entered    bool
	Exited     bool
	Code       *UnitVersion
	Stack      *ComponentLookup
	Exception  *ComponentLookup
	CallTarget *ComponentLookup
	Executed   bool
	Success    bool
}

func (t *ModelThread) clone() *ModelThread {
	cp := *t
	return &cp
}

// StagedPublication is the audit fixture: old and new immutable versions of
// one code unit plus a PC that both old metadata and new code can handle.
type StagedPublication struct {
	UnitID string
	PC     uint64
	Old    *UnitVersion
	New    *UnitVersion
}

// NewStagedPublication constructs an audit fixture. It requires distinct
// generations and verifies that new code and every old metadata component are
// structurally usable at pc, which is exactly what lets a torn read "succeed"
// without a validation failure.
func NewStagedPublication(old, new *UnitVersion, pc uint64) (*StagedPublication, error) {
	if old == nil || new == nil {
		return nil, fmt.Errorf("%w: nil staged version", ErrInvalidUnit)
	}
	if old.UnitID() != new.UnitID() {
		return nil, fmt.Errorf("%w: unit IDs differ", ErrInvalidUnit)
	}
	if old.Generation() == new.Generation() {
		return nil, fmt.Errorf("%w: staged generations must differ", ErrInvalidUnit)
	}
	if old.ContentRoot() == new.ContentRoot() {
		return nil, fmt.Errorf("%w: staged versions are identical", ErrInvalidUnit)
	}
	if !new.HasInstructionAt(pc) {
		return nil, fmt.Errorf("%w: new code has no instruction at pc %#x", ErrInvalidUnit, pc)
	}
	if _, ok := old.stackMapAt(pc); !ok {
		return nil, fmt.Errorf("%w: old stack map does not cover pc %#x", ErrInvalidUnit, pc)
	}
	if _, ok := old.exceptionAt(pc); !ok {
		return nil, fmt.Errorf("%w: old exception table does not cover pc %#x", ErrInvalidUnit, pc)
	}
	if _, ok := old.callTargetAt(pc); !ok {
		return nil, fmt.Errorf("%w: old call target table has no entry at pc %#x", ErrInvalidUnit, pc)
	}
	return &StagedPublication{
		UnitID: old.UnitID(),
		PC:     pc,
		Old:    old,
		New:    new,
	}, nil
}

func (s *StagedPublication) version(generation uint64) (*UnitVersion, bool) {
	switch generation {
	case s.Old.Generation():
		return s.Old, true
	case s.New.Generation():
		return s.New, true
	default:
		return nil, false
	}
}

// InitialWorld creates the audit world. If no thread IDs are supplied, one
// auditor thread named "t0" is created.
func (s *StagedPublication) InitialWorld(threadIDs ...string) *StagedWorld {
	if len(threadIDs) == 0 {
		threadIDs = []string{"t0"}
	}
	w := &StagedWorld{
		Publication: s,
		Visible: ComponentVersions{
			Code:           s.Old.Generation(),
			StackMap:       s.Old.Generation(),
			ExceptionTable: s.Old.Generation(),
			CallTargets:    s.Old.Generation(),
		},
		Published: make(map[Component]bool),
		Threads:   make(map[string]*ModelThread, len(threadIDs)),
	}
	for _, id := range threadIDs {
		if id == "" {
			continue
		}
		w.Threads[id] = &ModelThread{ID: id}
	}
	return w
}

// StagedWorld is one scheduler state.
type StagedWorld struct {
	Publication *StagedPublication
	Visible     ComponentVersions
	Published   map[Component]bool
	Threads     map[string]*ModelThread
	Unspawned   []string
}

func (w *StagedWorld) clone() *StagedWorld {
	cp := &StagedWorld{
		Publication: w.Publication,
		Visible:     w.Visible,
		Published:   make(map[Component]bool, len(w.Published)),
		Threads:     make(map[string]*ModelThread, len(w.Threads)),
		Unspawned:   append([]string(nil), w.Unspawned...),
	}
	for c, ok := range w.Published {
		cp.Published[c] = ok
	}
	for id, t := range w.Threads {
		cp.Threads[id] = t.clone()
	}
	return cp
}

// AddUnspawnedThread schedules a thread that may join at any interleaving.
func (w *StagedWorld) AddUnspawnedThread(id string) {
	if id == "" {
		return
	}
	if _, exists := w.Threads[id]; exists {
		return
	}
	for _, existing := range w.Unspawned {
		if existing == id {
			return
		}
	}
	w.Unspawned = append(w.Unspawned, id)
	sort.Strings(w.Unspawned)
}

func (w *StagedWorld) threadIDs() []string {
	ids := make([]string, 0, len(w.Threads))
	for id := range w.Threads {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Actions returns every action enabled in this state in deterministic order.
func (w *StagedWorld) Actions() []Action {
	var out []Action
	for _, op := range []Op{OpPublishCode, OpPublishStackMap, OpPublishExceptionTable, OpPublishCallTargets} {
		comp, _ := op.component()
		if w.Published[comp] {
			continue
		}
		out = append(out, Action{
			Actor:      "publisher",
			Op:         op,
			UnitID:     w.Publication.UnitID,
			Generation: w.Publication.New.Generation(),
		})
	}
	for _, id := range w.Unspawned {
		if _, exists := w.Threads[id]; exists {
			continue
		}
		out = append(out, Action{Actor: "publisher", Op: OpSpawnThread, Thread: id})
	}
	for _, id := range w.threadIDs() {
		t := w.Threads[id]
		if !t.Entered {
			out = append(out, Action{Actor: id, Op: OpEnter, Thread: id})
			continue
		}
		if t.Exited || t.Executed {
			continue
		}
		if t.Stack == nil {
			out = append(out, Action{Actor: id, Op: OpLookupStackMap, Thread: id})
		}
		if t.Exception == nil {
			out = append(out, Action{Actor: id, Op: OpLookupExceptionTable, Thread: id})
		}
		if t.CallTarget == nil {
			out = append(out, Action{Actor: id, Op: OpLookupCallTarget, Thread: id})
		}
		if t.Stack != nil && t.Exception != nil && t.CallTarget != nil {
			out = append(out, Action{Actor: id, Op: OpExecute, Thread: id})
		}
	}
	return out
}

// Step applies one action and returns a new world. The receiver is not mutated.
func (w *StagedWorld) Step(a Action) (*StagedWorld, bool) {
	next := w.clone()
	pub := next.Publication
	switch a.Op {
	case OpPublishCode, OpPublishStackMap, OpPublishExceptionTable, OpPublishCallTargets:
		comp, _ := a.Op.component()
		if next.Published[comp] || a.Generation != pub.New.Generation() {
			return nil, false
		}
		next.Visible.set(comp, pub.New.Generation())
		next.Published[comp] = true
	case OpSpawnThread:
		idx := -1
		for i, id := range next.Unspawned {
			if id == a.Thread {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, false
		}
		if _, exists := next.Threads[a.Thread]; exists {
			return nil, false
		}
		next.Unspawned = append(next.Unspawned[:idx], next.Unspawned[idx+1:]...)
		next.Threads[a.Thread] = &ModelThread{ID: a.Thread}
	case OpEnter:
		t := next.Threads[a.Thread]
		if t == nil || t.Entered || t.Exited {
			return nil, false
		}
		u, ok := pub.version(next.Visible.Code)
		if !ok {
			return nil, false
		}
		t.Code = u
		t.Entered = true
	case OpLookupStackMap:
		t := next.Threads[a.Thread]
		if t == nil || !t.Entered || t.Exited || t.Stack != nil {
			return nil, false
		}
		u, ok := pub.version(next.Visible.StackMap)
		if !ok {
			return nil, false
		}
		e, found := u.stackMapAt(pub.PC)
		t.Stack = &ComponentLookup{Component: ComponentStackMap, Generation: u.Generation(), Digest: u.Digests().StackMap, OK: found && e.PC == pub.PC}
	case OpLookupExceptionTable:
		t := next.Threads[a.Thread]
		if t == nil || !t.Entered || t.Exited || t.Exception != nil {
			return nil, false
		}
		u, ok := pub.version(next.Visible.ExceptionTable)
		if !ok {
			return nil, false
		}
		_, found := u.exceptionAt(pub.PC)
		t.Exception = &ComponentLookup{Component: ComponentExceptionTable, Generation: u.Generation(), Digest: u.Digests().ExceptionTable, OK: found}
	case OpLookupCallTarget:
		t := next.Threads[a.Thread]
		if t == nil || !t.Entered || t.Exited || t.CallTarget != nil {
			return nil, false
		}
		u, ok := pub.version(next.Visible.CallTargets)
		if !ok {
			return nil, false
		}
		_, found := u.callTargetAt(pub.PC)
		t.CallTarget = &ComponentLookup{Component: ComponentCallTargets, Generation: u.Generation(), Digest: u.Digests().CallTargets, OK: found}
	case OpExecute:
		t := next.Threads[a.Thread]
		if t == nil || !t.Entered || t.Exited || t.Executed {
			return nil, false
		}
		if t.Code == nil || t.Stack == nil || t.Exception == nil || t.CallTarget == nil {
			return nil, false
		}
		t.Executed = true
		t.Success = t.Code.HasInstructionAt(pub.PC) &&
			t.Stack.OK && t.Exception.OK && t.CallTarget.OK
	default:
		return nil, false
	}
	return next, true
}

// TornGoal reports whether some thread executed new code with old stack map,
// exception table and call-target metadata, and the execution was structurally
// accepted.
func (w *StagedWorld) TornGoal() bool {
	t, ok := w.TornThread()
	return ok && t.Success
}

// TornThread returns the first thread in a torn-but-successful state.
func (w *StagedWorld) TornThread() (*ModelThread, bool) {
	pub := w.Publication
	for _, id := range w.threadIDs() {
		t := w.Threads[id]
		if !t.Executed || !t.Success || t.Code == nil ||
			t.Stack == nil || t.Exception == nil || t.CallTarget == nil {
			continue
		}
		if t.Code.ContentRoot() != pub.New.ContentRoot() {
			continue
		}
		if t.Stack.Generation == pub.Old.Generation() &&
			t.Stack.Digest == pub.Old.Digests().StackMap &&
			t.Exception.Generation == pub.Old.Generation() &&
			t.Exception.Digest == pub.Old.Digests().ExceptionTable &&
			t.CallTarget.Generation == pub.Old.Generation() &&
			t.CallTarget.Digest == pub.Old.Digests().CallTargets {
			return t, true
		}
	}
	return nil, false
}

// Key returns a canonical state key used for BFS deduplication.
func (w *StagedWorld) Key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "vis=%d/%d/%d/%d;pub=",
		w.Visible.Code, w.Visible.StackMap, w.Visible.ExceptionTable, w.Visible.CallTargets)
	for _, c := range []Component{ComponentCode, ComponentStackMap, ComponentExceptionTable, ComponentCallTargets} {
		if w.Published[c] {
			fmt.Fprintf(&b, "%d,", c)
		}
	}
	fmt.Fprintf(&b, ";unspawned=%s;", strings.Join(w.Unspawned, ","))
	for _, id := range w.threadIDs() {
		t := w.Threads[id]
		fmt.Fprintf(&b, "%s:%t/%t/%t:", id, t.Entered, t.Executed, t.Success)
		if t.Code != nil {
			fmt.Fprintf(&b, "c%s/", t.Code.ContentRoot())
		} else {
			b.WriteString("c-/")
		}
		writeLookup := func(l *ComponentLookup) {
			if l == nil {
				b.WriteString("-/")
			} else {
				fmt.Fprintf(&b, "%d%s%t/", l.Generation, l.Digest, l.OK)
			}
		}
		writeLookup(t.Stack)
		writeLookup(t.Exception)
		writeLookup(t.CallTarget)
		b.WriteByte(';')
	}
	return b.String()
}

// Replay applies a controlled schedule and returns the final state.
func (w *StagedWorld) Replay(actions []Action) (*StagedWorld, error) {
	cur := w.clone()
	for i, a := range actions {
		next, ok := cur.Step(a)
		if !ok {
			return nil, fmt.Errorf("%w: action %d (%s) is not enabled", ErrWitnessNotFound, i, a)
		}
		cur = next
	}
	return cur, nil
}

// Witness is a shortest interleaving plus the resulting state.
type Witness struct {
	Steps []Action
	Final *StagedWorld
}

// String renders the witness as one action per line.
func (w *Witness) String() string {
	if w == nil {
		return "<nil witness>"
	}
	var b strings.Builder
	for i, a := range w.Steps {
		fmt.Fprintf(&b, "%d: %s\n", i, a)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// Explorer controls bounded breadth-first interleaving search.
type Explorer struct {
	MaxDepth  int
	MaxStates int
}

// ShortestTornWitness searches for the shortest schedule that reaches a
// successful torn execution.
func (w *StagedWorld) ShortestTornWitness(e Explorer) (*Witness, error) {
	maxDepth := e.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 16
	}
	maxStates := e.MaxStates
	if maxStates <= 0 {
		maxStates = 200000
	}
	type node struct {
		world *StagedWorld
		steps []Action
	}
	start := w.clone()
	queue := []node{{world: start}}
	visited := map[string]struct{}{start.Key(): {}}
	for head := 0; head < len(queue); head++ {
		n := queue[head]
		if t, ok := n.world.TornThread(); ok && t.Success {
			return &Witness{Steps: append([]Action(nil), n.steps...), Final: n.world}, nil
		}
		if len(n.steps) >= maxDepth {
			continue
		}
		for _, a := range n.world.Actions() {
			next, ok := n.world.Step(a)
			if !ok {
				continue
			}
			key := next.Key()
			if _, seen := visited[key]; seen {
				continue
			}
			if len(visited) >= maxStates {
				return nil, fmt.Errorf("%w: state limit %d reached", ErrWitnessNotFound, maxStates)
			}
			visited[key] = struct{}{}
			steps := append(append([]Action(nil), n.steps...), a)
			queue = append(queue, node{world: next, steps: steps})
		}
	}
	return nil, fmt.Errorf("%w: depth %d, states %d", ErrWitnessNotFound, maxDepth, len(visited))
}

// FindShortestTornWitness is the convenient audit entry point.
func FindShortestTornWitness(old, new *UnitVersion, pc uint64) (*Witness, error) {
	pub, err := NewStagedPublication(old, new, pc)
	if err != nil {
		return nil, err
	}
	return pub.InitialWorld().ShortestTornWitness(Explorer{})
}
