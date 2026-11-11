package netsec

import "sort"

// Bounds limits the concrete interpreter so enumeration of small programs
// always terminates.
type Bounds struct {
	MaxSteps int // total node executions across all enumerated runs
	MaxDepth int // maximum call-stack depth; deeper calls are skipped
}

// DefaultBounds is a reasonable budget for small test programs.
var DefaultBounds = Bounds{MaxSteps: 100000, MaxDepth: 8}

// ConcError is an error observed by the concrete interpreter. The object is
// identified by its allocation site so it can be matched against abstract
// reports.
type ConcError struct {
	Kind ErrKind
	Proc string
	Node NodeID
	Site Obj
}

// cobj is a concrete heap object.
type cobj struct {
	site   Obj
	fields map[string]int
	freed  bool
}

// cframe is one concrete call-stack frame. Variable value 0 means null
// (uninitialized); concrete object ids are 1-based heap indices.
type cframe struct {
	proc *Proc
	node NodeID
	vars map[string]int
}

type cstate struct {
	heap  []*cobj
	stack []cframe
}

func (c *cstate) clone() *cstate {
	nh := make([]*cobj, len(c.heap))
	for i, o := range c.heap {
		nf := make(map[string]int, len(o.fields))
		for f, v := range o.fields {
			nf[f] = v
		}
		nh[i] = &cobj{site: o.site, fields: nf, freed: o.freed}
	}
	ns := make([]cframe, len(c.stack))
	for i, f := range c.stack {
		nv := make(map[string]int, len(f.vars))
		for k, v := range f.vars {
			nv[k] = v
		}
		ns[i] = cframe{proc: f.proc, node: f.node, vars: nv}
	}
	return &cstate{heap: nh, stack: ns}
}

// stepResult holds the outcomes of executing one concrete node.
type stepResult struct {
	next []*cstate // successor states (2 for a branch, 0 after an error)
	err  *ConcError
}

// stepConcrete executes the node at the top of c's stack.
func stepConcrete(prog *Program, c *cstate, b Bounds) stepResult {
	fr := &c.stack[len(c.stack)-1]
	n := fr.proc.Nodes[fr.node]
	site := Obj{fr.proc.Name, n.ID}

	switch n.Kind {
	case KindAlloc:
		c.heap = append(c.heap, &cobj{site: site, fields: map[string]int{}})
		fr.vars[n.Dst] = len(c.heap)
		fr.node = n.Next[0]

	case KindCopy:
		fr.vars[n.Dst] = fr.vars[n.Src]
		fr.node = n.Next[0]

	case KindLoad:
		base := fr.vars[n.Base]
		if base != 0 {
			if c.heap[base-1].freed {
				return stepResult{err: &ConcError{UseAfterFree, fr.proc.Name, n.ID, c.heap[base-1].site}}
			}
			fr.vars[n.Dst] = c.heap[base-1].fields[n.Field]
		}
		fr.node = n.Next[0]

	case KindStore:
		base := fr.vars[n.Base]
		if base != 0 {
			if c.heap[base-1].freed {
				return stepResult{err: &ConcError{UseAfterFree, fr.proc.Name, n.ID, c.heap[base-1].site}}
			}
			c.heap[base-1].fields[n.Field] = fr.vars[n.Src]
		}
		fr.node = n.Next[0]

	case KindFree:
		v := fr.vars[n.Src]
		if v != 0 {
			if c.heap[v-1].freed {
				return stepResult{err: &ConcError{DoubleFree, fr.proc.Name, n.ID, c.heap[v-1].site}}
			}
			c.heap[v-1].freed = true
		}
		fr.node = n.Next[0]

	case KindDeref:
		v := fr.vars[n.Src]
		if v != 0 && c.heap[v-1].freed {
			return stepResult{err: &ConcError{UseAfterFree, fr.proc.Name, n.ID, c.heap[v-1].site}}
		}
		fr.node = n.Next[0]

	case KindBranch:
		then := c.clone()
		then.stack[len(then.stack)-1].node = n.Next[0]
		els := c
		els.stack[len(els.stack)-1].node = n.Next[1]
		return stepResult{next: []*cstate{then, els}}

	case KindCall:
		callee := prog.Procs[n.Callee]
		if len(c.stack) >= b.MaxDepth {
			// Depth budget exhausted: treat the call as a no-op.
			fr.vars[n.Dst] = 0
			fr.node = n.Next[0]
			break
		}
		nf := cframe{proc: callee, node: callee.Entry, vars: map[string]int{}}
		for i, param := range callee.Params {
			nf.vars[param] = fr.vars[n.Args[i]]
		}
		c.stack = append(c.stack, nf)
		// The caller frame stays on the call node; it advances when the
		// callee returns.

	case KindReturn:
		ret := fr.vars[fr.proc.Result]
		c.stack = c.stack[:len(c.stack)-1]
		if len(c.stack) == 0 {
			return stepResult{} // execution finished
		}
		caller := &c.stack[len(c.stack)-1]
		callNode := caller.proc.Nodes[caller.node]
		caller.vars[callNode.Dst] = ret
		caller.node = callNode.Next[0]
	}
	return stepResult{next: []*cstate{c}}
}

// Enumerate runs the bounded concrete interpreter over all branch choices
// and returns the deduplicated set of real errors it observes.
func Enumerate(prog *Program, b Bounds) []ConcError {
	if b.MaxSteps <= 0 {
		b.MaxSteps = DefaultBounds.MaxSteps
	}
	if b.MaxDepth <= 0 {
		b.MaxDepth = DefaultBounds.MaxDepth
	}
	main := prog.Procs[prog.Main]
	start := &cstate{stack: []cframe{{proc: main, node: main.Entry, vars: map[string]int{}}}}
	work := []*cstate{start}
	seen := map[ConcError]bool{}
	var errs []ConcError
	steps := 0
	for len(work) > 0 && steps < b.MaxSteps {
		c := work[len(work)-1]
		work = work[:len(work)-1]
		steps++
		res := stepConcrete(prog, c, b)
		if res.err != nil {
			if !seen[*res.err] {
				seen[*res.err] = true
				errs = append(errs, *res.err)
			}
			continue
		}
		work = append(work, res.next...)
	}
	sort.Slice(errs, func(i, j int) bool {
		if errs[i].Proc != errs[j].Proc {
			return errs[i].Proc < errs[j].Proc
		}
		if errs[i].Node != errs[j].Node {
			return errs[i].Node < errs[j].Node
		}
		return errs[i].Kind < errs[j].Kind
	})
	return errs
}

// CheckResult compares abstract reports against concrete executions.
type CheckResult struct {
	ConcreteErrors []ConcError // all real errors found within the bounds
	Missing        []ConcError // real errors with no matching report (must be empty)
	Unproven       []*Report   // reports whose witness path cannot be replayed concretely
}

// Check verifies that the reports cover every real error found by bounded
// concrete enumeration, and annotates each report: Report.Proven is set
// when the witness path replays concretely and reproduces the error;
// otherwise the report is listed in Unproven.
func Check(prog *Program, res *Result, b Bounds) *CheckResult {
	errs := Enumerate(prog, b)
	reported := map[ConcError]bool{}
	for _, r := range res.Reports {
		reported[ConcError{r.Kind, r.Proc, r.Node, r.Object}] = true
	}
	cr := &CheckResult{ConcreteErrors: errs}
	for _, e := range errs {
		if !reported[e] {
			cr.Missing = append(cr.Missing, e)
		}
	}
	for _, r := range res.Reports {
		if replay(prog, r) {
			r.Proven = true
		} else {
			cr.Unproven = append(cr.Unproven, r)
		}
	}
	return cr
}

// replay checks whether a report's witness path can be confirmed by a
// concrete execution. The path is followed strictly with respect to
// control-flow choices: whenever the current node is the next path element,
// it is consumed and a branch must take the successor the path dictates.
// Extra steps are permitted only for call/return bookkeeping, because
// context-insensitive summary paths omit earlier callee executions. The
// search is bounded; if no concrete execution consistent with the path
// reproduces the claimed error, the path is unproven.
func replay(prog *Program, rep *Report) bool {
	path := rep.Path
	if len(path) == 0 {
		return false
	}
	main := prog.Procs[prog.Main]
	start := &cstate{stack: []cframe{{proc: main, node: main.Entry, vars: map[string]int{}}}}
	want := ConcError{rep.Kind, rep.Proc, rep.Node, rep.Object}
	b := DefaultBounds

	type item struct {
		c *cstate
		i int
	}
	work := []item{{start, 0}}
	steps := 0
	for len(work) > 0 && steps < b.MaxSteps {
		it := work[len(work)-1]
		work = work[:len(work)-1]
		steps++
		if len(it.c.stack) == 0 {
			continue
		}
		fr := &it.c.stack[len(it.c.stack)-1]
		n := fr.proc.Nodes[fr.node]
		match := it.i < len(path) &&
			fr.proc.Name == path[it.i].Proc && fr.node == path[it.i].Node

		if match && n.Kind == KindBranch {
			// The path must dictate the successor.
			if it.i+1 >= len(path) || path[it.i+1].Proc != fr.proc.Name {
				continue
			}
			nx := path[it.i+1].Node
			if nx != n.Next[0] && nx != n.Next[1] {
				continue
			}
			fr.node = nx
			work = append(work, item{it.c, it.i + 1})
			continue
		}
		if !match && n.Kind == KindBranch {
			// Skipped territory (e.g. an earlier callee execution):
			// explore both directions.
			for _, succ := range n.Next {
				nc := it.c.clone()
				nc.stack[len(nc.stack)-1].node = succ
				work = append(work, item{nc, it.i})
			}
			continue
		}
		advance := it.i
		if match {
			advance = it.i + 1
		}
		res := stepConcrete(prog, it.c, b)
		if res.err != nil {
			if match && it.i == len(path)-1 && *res.err == want {
				return true
			}
			continue
		}
		for _, nc := range res.next {
			work = append(work, item{nc, advance})
		}
	}
	return false
}
