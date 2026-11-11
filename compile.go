package netsec

import (
	"fmt"
	"sort"
)

// maxLocks is the supported limit of mutexes.
const maxLocks = 6

// cGuard is the compiled, assignment-bit indexed form of a Guard.
type cGuard interface {
	eval(assign uint32) bool
}

type gVar struct{ bit uint }
type gNot struct{ inner cGuard }
type gAnd struct{ lhs, rhs cGuard }
type gOr struct{ lhs, rhs cGuard }

func (v gVar) eval(a uint32) bool { return a&(1<<v.bit) != 0 }
func (n gNot) eval(a uint32) bool { return !n.inner.eval(a) }
func (a gAnd) eval(v uint32) bool { return a.lhs.eval(v) && a.rhs.eval(v) }
func (o gOr) eval(v uint32) bool  { return o.lhs.eval(v) || o.rhs.eval(v) }

type cEdge struct {
	name  string
	from  int
	to    int
	op    Op
	lock  int // -1 for OpNone
	guard cGuard
}

type cThread struct {
	name   string
	start  int
	states []string
	edges  []cEdge
	out    map[int][]int // state index -> ordered edge indices
}

type cSpec struct {
	threads   [2]cThread
	lockNames []string
	varNames  []string
}

// Analyze searches for a reachable mutex deadlock of two threads.
//
// Each Boolean condition variable is global and keeps one fixed truth value
// for a whole execution. Analyze enumerates every assignment of the variables
// appearing on edges and, for each assignment, performs a breadth-first walk
// of the concrete product graph, so the first deadlock found lies on a
// shortest possible interleaving. Acquisition orders observed on mutually
// exclusive branches are never spliced together: only edges enabled under the
// same fixed assignment and reachable in one concrete state are traversed.
//
// An error is returned for malformed inputs or for an execution that unlocks
// a mutex the executing thread does not hold.
func Analyze(spec Spec) (*Result, error) {
	compiled, err := compile(spec)
	if err != nil {
		return nil, err
	}

	for _, assign := range assignments(len(compiled.varNames)) {
		res, err := bfs(compiled, assign)
		if err != nil {
			return nil, err
		}
		if res != nil {
			return res, nil
		}
	}
	return &Result{Reachable: false, Kind: DeadlockNone}, nil
}

func compile(spec Spec) (*cSpec, error) {
	if len(spec.Locks) < 1 || len(spec.Locks) > maxLocks {
		return nil, fmt.Errorf("netsec: require between 1 and %d mutexes, got %d", maxLocks, len(spec.Locks))
	}
	lockIndex := make(map[string]int, len(spec.Locks))
	for i, name := range spec.Locks {
		if name == "" {
			return nil, fmt.Errorf("netsec: lock %d has an empty name", i)
		}
		if _, dup := lockIndex[name]; dup {
			return nil, fmt.Errorf("netsec: duplicate lock name %q", name)
		}
		lockIndex[name] = i
	}

	out := &cSpec{lockNames: append([]string(nil), spec.Locks...)}
	varSet := map[string]struct{}{}

	for ti := range spec.Threads {
		ct, err := compileThread(spec.Threads[ti], ti, lockIndex)
		if err != nil {
			return nil, err
		}
		out.threads[ti] = ct
		for _, e := range spec.Threads[ti].Edges {
			if e.Guard != nil {
				e.Guard.vars(varSet)
			}
		}
	}

	out.varNames = make([]string, 0, len(varSet))
	for name := range varSet {
		if name == "" {
			return nil, fmt.Errorf("netsec: condition variable has an empty name")
		}
		out.varNames = append(out.varNames, name)
	}
	sort.Strings(out.varNames)
	// Reassign bits in name order so assignment enumeration is deterministic.
	varIndex := map[string]uint{}
	for i, name := range out.varNames {
		varIndex[name] = uint(i)
	}
	for ti := range out.threads {
		for ei := range out.threads[ti].edges {
			g, err := compileGuard(spec.Threads[ti].Edges[ei].Guard, varIndex)
			if err != nil {
				return nil, err
			}
			out.threads[ti].edges[ei].guard = g
		}
	}
	return out, nil
}

func compileThread(t Thread, ti int, lockIndex map[string]int) (cThread, error) {
	if len(t.States) == 0 {
		return cThread{}, fmt.Errorf("netsec: thread %d has no states", ti)
	}
	name := t.Name
	if name == "" {
		name = fmt.Sprintf("T%d", ti)
	}
	stateIndex := make(map[string]int, len(t.States))
	for i, s := range t.States {
		if s == "" {
			return cThread{}, fmt.Errorf("netsec: thread %q has an empty state name", name)
		}
		if _, dup := stateIndex[s]; dup {
			return cThread{}, fmt.Errorf("netsec: thread %q has duplicate state %q", name, s)
		}
		stateIndex[s] = i
	}
	start, ok := stateIndex[t.Start]
	if !ok {
		return cThread{}, fmt.Errorf("netsec: thread %q start state %q is not in States", name, t.Start)
	}

	ct := cThread{
		name:   name,
		start:  start,
		states: append([]string(nil), t.States...),
		out:    map[int][]int{},
	}
	for ei, e := range t.Edges {
		from, ok := stateIndex[e.From]
		if !ok {
			return cThread{}, fmt.Errorf("netsec: thread %q edge %d: unknown source state %q", name, ei, e.From)
		}
		to, ok := stateIndex[e.To]
		if !ok {
			return cThread{}, fmt.Errorf("netsec: thread %q edge %d: unknown target state %q", name, ei, e.To)
		}
		lock := -1
		switch e.Op {
		case OpNone:
			if e.Lock != "" {
				return cThread{}, fmt.Errorf("netsec: thread %q edge %d: OpNone edge must not name a lock", name, ei)
			}
		case OpLock, OpUnlock:
			if e.Lock == "" {
				return cThread{}, fmt.Errorf("netsec: thread %q edge %d: lock operation requires a lock name", name, ei)
			}
			idx, ok := lockIndex[e.Lock]
			if !ok {
				return cThread{}, fmt.Errorf("netsec: thread %q edge %d: unknown lock %q", name, ei, e.Lock)
			}
			lock = idx
		default:
			return cThread{}, fmt.Errorf("netsec: thread %q edge %d: unknown operation %d", name, ei, e.Op)
		}
		edgeName := e.Name
		if edgeName == "" {
			edgeName = fmt.Sprintf("%s.e%d", name, ei)
		}
		ce := cEdge{name: edgeName, from: from, to: to, op: e.Op, lock: lock}
		idx := len(ct.edges)
		ct.edges = append(ct.edges, ce)
		ct.out[from] = append(ct.out[from], idx)
	}
	return ct, nil
}

func compileGuard(g Guard, varIndex map[string]uint) (cGuard, error) {
	if g == nil {
		return nil, nil
	}
	switch v := g.(type) {
	case Var:
		if v.Name == "" {
			return nil, fmt.Errorf("condition variable has an empty name")
		}
		bit, ok := varIndex[v.Name]
		if !ok {
			bit = uint(len(varIndex))
			varIndex[v.Name] = bit
		}
		return gVar{bit: bit}, nil
	case Not:
		if v.Inner == nil {
			return nil, fmt.Errorf("Not has a nil operand")
		}
		inner, err := compileGuard(v.Inner, varIndex)
		if err != nil {
			return nil, err
		}
		return gNot{inner: inner}, nil
	case And:
		if v.Lhs == nil || v.Rhs == nil {
			return nil, fmt.Errorf("And has a nil operand")
		}
		lhs, err := compileGuard(v.Lhs, varIndex)
		if err != nil {
			return nil, err
		}
		rhs, err := compileGuard(v.Rhs, varIndex)
		if err != nil {
			return nil, err
		}
		return gAnd{lhs: lhs, rhs: rhs}, nil
	case Or:
		if v.Lhs == nil || v.Rhs == nil {
			return nil, fmt.Errorf("Or has a nil operand")
		}
		lhs, err := compileGuard(v.Lhs, varIndex)
		if err != nil {
			return nil, err
		}
		rhs, err := compileGuard(v.Rhs, varIndex)
		if err != nil {
			return nil, err
		}
		return gOr{lhs: lhs, rhs: rhs}, nil
	default:
		return nil, fmt.Errorf("unsupported guard type %T", g)
	}
}

// assignments enumerates all fixed truth assignments for n condition
// variables as bit masks in ascending order.
func assignments(n int) []uint32 {
	total := uint32(1) << uint(n)
	out := make([]uint32, total)
	for i := uint32(0); i < total; i++ {
		out[i] = i
	}
	return out
}
