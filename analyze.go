package netsec

import (
	"fmt"
	"sort"
)

// maxPathLen bounds recorded witness paths so recursive programs cannot
// grow them without limit. Dataflow convergence does not depend on paths.
const maxPathLen = 512

// PathElem is one step of a witness path: a node visited in a procedure.
// A call is represented by the call node followed by the callee's nodes.
type PathElem struct {
	Proc string
	Node NodeID
}

// ErrKind classifies a reported error.
type ErrKind int

const (
	DoubleFree   ErrKind = iota // free of an object that may already be freed
	UseAfterFree                // access (deref/field read/field write) of a maybe-freed object
)

func (k ErrKind) String() string {
	if k == DoubleFree {
		return "double-free"
	}
	return "use-after-free"
}

// Report is a possible error together with a replayable witness path from
// the program entry. Proven is filled in by Check: it is true when the
// bounded concrete interpreter can replay Path and reproduce the error.
type Report struct {
	Kind   ErrKind
	Proc   string
	Node   NodeID
	Object Obj
	Path   []PathElem
	Proven bool
}

func (r *Report) String() string {
	return fmt.Sprintf("%s in %s at node %d: object allocated at %s:%d",
		r.Kind, r.Proc, r.Node, r.Object.Proc, r.Object.Site)
}

// Result is the outcome of Analyze.
type Result struct {
	Reports []*Report
}

type repKey struct {
	kind ErrKind
	proc string
	node NodeID
	obj  Obj
}

// procSummary is the context-insensitive summary of one procedure: the join
// of all observed inputs, and the join of all observed outputs. Recursive
// calls are handled by iterating these summaries to a fixpoint.
type procSummary struct {
	inHeap  *heapState
	inVars  map[string]pts
	inPath  []PathElem // witness path of the call that last grew the input
	outHeap *heapState
	outRet  pts
}

type analyzer struct {
	prog    *Program
	summ    map[string]*procSummary
	callers map[string]map[string]struct{}
	reports []*Report
	seen    map[repKey]bool
}

// Analyze runs the interprocedural may-analysis and returns all possible
// double-free and use-after-free reports with witness paths.
func Analyze(prog *Program) (*Result, error) {
	if err := prog.Validate(); err != nil {
		return nil, err
	}
	a := &analyzer{
		prog:    prog,
		summ:    map[string]*procSummary{},
		callers: map[string]map[string]struct{}{},
		seen:    map[repKey]bool{},
	}
	for name := range prog.Procs {
		a.summ[name] = &procSummary{
			inHeap:  newHeap(),
			inVars:  map[string]pts{},
			outHeap: newHeap(),
			outRet:  pts{},
		}
		a.callers[name] = map[string]struct{}{}
	}
	// Chaotic iteration over procedure summaries until the fixpoint.
	dirty := map[string]bool{prog.Main: true}
	for len(dirty) > 0 {
		names := make([]string, 0, len(dirty))
		for n := range dirty {
			names = append(names, n)
		}
		sort.Strings(names)
		p := names[0]
		delete(dirty, p)
		a.runProc(p, dirty)
	}
	return &Result{Reports: a.reports}, nil
}

func (a *analyzer) report(kind ErrKind, proc string, node NodeID, obj Obj, path []PathElem) {
	k := repKey{kind, proc, node, obj}
	if a.seen[k] {
		return
	}
	a.seen[k] = true
	a.reports = append(a.reports, &Report{
		Kind:   kind,
		Proc:   proc,
		Node:   node,
		Object: obj,
		Path:   append([]PathElem(nil), path...),
	})
}

// runProc re-analyzes one procedure under its current input summary,
// updating its output summary and marking affected procedures dirty.
func (a *analyzer) runProc(name string, dirty map[string]bool) {
	proc := a.prog.Procs[name]
	sum := a.summ[name]

	entry := &state{vars: map[string]pts{}, heap: sum.inHeap.clone()}
	for param, p := range sum.inVars {
		entry.vars[param] = p.clone()
	}
	entry.path = append(entry.path, sum.inPath...)

	at := map[NodeID]*state{}
	var wl []NodeID
	push := func(id NodeID, s *state) {
		if cur, ok := at[id]; ok {
			if cur.join(s) {
				wl = append(wl, id)
			}
			return
		}
		at[id] = s.clone()
		wl = append(wl, id)
	}
	push(proc.Entry, entry)

	for len(wl) > 0 {
		id := wl[0]
		wl = wl[1:]
		a.step(proc, id, at[id], push, dirty)
	}
}

// withPath returns a copy of s extended by one visited node.
func withPath(s *state, proc string, node NodeID) *state {
	ns := &state{vars: s.vars, heap: s.heap, path: s.path}
	if len(ns.path) < maxPathLen {
		p := make([]PathElem, len(s.path)+1)
		copy(p, s.path)
		p[len(s.path)] = PathElem{proc, node}
		ns.path = p
	}
	return ns
}

func (a *analyzer) step(proc *Proc, id NodeID, s *state, push func(NodeID, *state), dirty map[string]bool) {
	n := proc.Nodes[id]
	sp := withPath(s, proc.Name, n.ID)

	switch n.Kind {
	case KindAlloc:
		ns := sp.clone()
		ns.vars[n.Dst] = pts{Obj{proc.Name, n.ID}: {}}
		push(n.Next[0], ns)

	case KindCopy:
		ns := sp.clone()
		ns.vars[n.Dst] = sp.vars[n.Src].clone()
		push(n.Next[0], ns)

	case KindLoad:
		for _, o := range sp.vars[n.Base].sorted() {
			if _, freed := sp.heap.Freed[o]; freed {
				a.report(UseAfterFree, proc.Name, n.ID, o, sp.path)
			}
		}
		ns := sp.clone()
		r := pts{}
		for o := range sp.vars[n.Base] {
			unionInto(r, sp.heap.Fields[o][n.Field])
		}
		ns.vars[n.Dst] = r
		push(n.Next[0], ns)

	case KindStore:
		for _, o := range sp.vars[n.Base].sorted() {
			if _, freed := sp.heap.Freed[o]; freed {
				a.report(UseAfterFree, proc.Name, n.ID, o, sp.path)
			}
		}
		ns := sp.clone()
		for o := range sp.vars[n.Base] {
			fm := ns.heap.Fields[o]
			if fm == nil {
				fm = map[string]pts{}
				ns.heap.Fields[o] = fm
			}
			cur := fm[n.Field]
			if cur == nil {
				cur = pts{}
				fm[n.Field] = cur
			}
			// Weak update: p may point to several objects, so the old
			// contents of the field must be kept.
			unionInto(cur, sp.vars[n.Src])
		}
		push(n.Next[0], ns)

	case KindFree:
		ns := sp.clone()
		for _, o := range sp.vars[n.Src].sorted() {
			if _, freed := sp.heap.Freed[o]; freed {
				a.report(DoubleFree, proc.Name, n.ID, o, sp.path)
			}
			ns.heap.Freed[o] = struct{}{}
		}
		push(n.Next[0], ns)

	case KindDeref:
		for _, o := range sp.vars[n.Src].sorted() {
			if _, freed := sp.heap.Freed[o]; freed {
				a.report(UseAfterFree, proc.Name, n.ID, o, sp.path)
			}
		}
		push(n.Next[0], sp)

	case KindBranch:
		push(n.Next[0], sp)
		push(n.Next[1], sp)

	case KindCall:
		callee := a.prog.Procs[n.Callee]
		cs := a.summ[n.Callee]
		grew := false
		for i, param := range callee.Params {
			dst := cs.inVars[param]
			if dst == nil {
				dst = pts{}
				cs.inVars[param] = dst
			}
			if unionInto(dst, sp.vars[n.Args[i]]) {
				grew = true
			}
		}
		if cs.inHeap.join(sp.heap) {
			grew = true
		}
		if grew {
			dirty[n.Callee] = true
			cs.inPath = append(cs.inPath[:0], sp.path...)
		}
		a.callers[n.Callee][proc.Name] = struct{}{}
		// Apply the callee's current summary; if it is still provisional
		// the caller is re-run when the summary grows (see KindReturn).
		ns := sp.clone()
		ns.heap.join(cs.outHeap)
		ns.vars[n.Dst] = cs.outRet.clone()
		push(n.Next[0], ns)

	case KindReturn:
		sum := a.summ[proc.Name]
		changed := sum.outHeap.join(sp.heap)
		if unionInto(sum.outRet, sp.vars[proc.Result]) {
			changed = true
		}
		if changed {
			for caller := range a.callers[proc.Name] {
				dirty[caller] = true
			}
		}
	}
}
