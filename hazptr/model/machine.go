package model

import "fmt"

// TraceEntry is one scheduled event inside a witness.
type TraceEntry struct {
	Step   int
	Thread int
	Op     Op
}

// Trace is an ordered schedule of explicit visibility events.
type Trace []TraceEntry

// object records one generation that ever occupied an address.
type object struct {
	addr     string
	identity int
	freed    bool
	// freedByScan is the scan generation that reclaimed it, if any.
	freedByScan int
}

// hpView is one entry of a thread's visible hazard-pointer array.
type hpView struct {
	addr     string
	identity int
	valid    bool
}

// regState tracks a reader's local register and its validation status.
type regState struct {
	addr       string
	identity   int
	verified   bool
	verifiedAt int
}

type thread struct {
	id int

	// staged state, not visible to other threads until flushed.
	stagedRoots map[string]string
	stagedHP    map[int]hpView

	// visible (published) state.
	visibleRoots map[string]string
	visibleHP    map[int]hpView

	regs    map[string]*regState
	aborted bool
}

type retiredEntry struct {
	addr      string
	identity  int
	retiredAt int
}

// Machine is the explicit-visibility hazard-pointer state machine.
type Machine struct {
	cfg Config

	threads map[int]*thread
	// objects maps address -> all generations; a live generation has
	// freed == false.
	objects map[string][]*object
	// retired is the set awaiting reclamation, keyed by address.
	retired map[string]retiredEntry

	step      int
	scanGen   int
	nextIdent int
	trace     Trace
}

// NewMachine creates a machine with the given thread ids and initial roots.
// An initial root address is registered as a live, generation-1 object.
func NewMachine(threadIDs []int, initialRoots map[string]string, cfg Config) *Machine {
	m := &Machine{
		cfg:     cfg,
		threads: map[int]*thread{},
		objects: map[string][]*object{},
		retired: map[string]retiredEntry{},
	}
	for _, id := range threadIDs {
		m.threads[id] = &thread{
			id:           id,
			stagedRoots:  map[string]string{},
			stagedHP:     map[int]hpView{},
			visibleRoots: map[string]string{},
			visibleHP:    map[int]hpView{},
			regs:         map[string]*regState{},
		}
	}
	for root, addr := range initialRoots {
		for _, t := range m.threads {
			t.visibleRoots[root] = addr
		}
		if addr != "" {
			m.touchObject(addr, 1)
			if m.nextIdent < 1 {
				m.nextIdent = 1
			}
		}
	}
	return m
}

func (m *Machine) touchObject(addr string, hintIdent int) *object {
	gens := m.objects[addr]
	if hintIdent > 0 {
		for _, o := range gens {
			if o.identity == hintIdent {
				return o
			}
		}
	}
	if len(gens) > 0 && !gens[len(gens)-1].freed {
		return gens[len(gens)-1]
	}
	ident := hintIdent
	if ident == 0 {
		m.nextIdent++
		ident = m.nextIdent
	}
	o := &object{addr: addr, identity: ident}
	m.objects[addr] = append(gens, o)
	return o
}

func (m *Machine) liveObject(addr string) *object {
	if addr == "" {
		return nil
	}
	gens := m.objects[addr]
	if len(gens) == 0 {
		return nil
	}
	o := gens[len(gens)-1]
	if o.freed {
		return nil
	}
	return o
}

func (m *Machine) reg(t *thread, name string) *regState {
	r := t.regs[name]
	if r == nil {
		r = &regState{}
		t.regs[name] = r
	}
	return r
}

func infeasible(op Op, format string, args ...any) Violation {
	return Violation{
		Kind:   ErrInfeasible,
		Thread: op.Thread,
		Op:     op,
		Detail: fmt.Sprintf(format, args...),
	}
}

func (m *Machine) fail(kind ViolationKind, op Op, addr string, identity int, format string, args ...any) Violation {
	return Violation{
		Kind:     kind,
		Thread:   op.Thread,
		Op:       op,
		Addr:     addr,
		Identity: identity,
		Detail:   fmt.Sprintf(format, args...),
	}
}

// Trace returns a copy of the schedule executed so far.
func (m *Machine) Trace() Trace {
	out := make(Trace, len(m.trace))
	copy(out, m.trace)
	return out
}

// StepCount returns the number of accepted events.
func (m *Machine) StepCount() int { return m.step }

// ScanGeneration returns the current scan generation counter.
func (m *Machine) ScanGeneration() int { return m.scanGen }

// Step applies one explicit event. A non-zero violation means the step is
// refused and the machine is left unchanged. ErrInfeasible marks branches
// that cannot occur in a real implementation and are pruned by enumeration.
func (m *Machine) Step(op Op) Violation {
	t := m.threads[op.Thread]
	if t == nil {
		return infeasible(op, "unknown thread %d", op.Thread)
	}
	if t.aborted {
		return infeasible(op, "thread T%d already aborted", op.Thread)
	}

	var v Violation
	switch op.Kind {
	case OpSetRoot:
		v = m.stepSetRoot(t, op)
	case OpFlushRoot:
		v = m.stepFlushRoot(t, op)
	case OpLoadRoot:
		v = m.stepLoadRoot(t, op)
	case OpProtect:
		v = m.stepProtect(t, op)
	case OpFlushHP:
		v = m.stepFlushHP(t, op)
	case OpClearHP:
		v = m.stepClearHP(t, op)
	case OpVerifyRoot:
		v = m.stepVerifyRoot(t, op)
	case OpDeref:
		v = m.stepDeref(t, op)
	case OpRetire:
		v = m.stepRetire(t, op)
	case OpScan:
		v = m.stepScan(t, op)
	case OpAlloc:
		v = m.stepAlloc(t, op)
	default:
		v = infeasible(op, "unknown op kind %v", op.Kind)
	}
	if v.Kind != ViolationNone {
		return v
	}

	m.step++
	m.trace = append(m.trace, TraceEntry{Step: m.step, Thread: op.Thread, Op: op})
	return Violation{}
}

// Aborted reports whether the thread gave up after a failed root re-read.
func (m *Machine) Aborted(threadID int) bool {
	t := m.threads[threadID]
	return t != nil && t.aborted
}
