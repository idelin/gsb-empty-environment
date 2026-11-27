package midend

import (
	"sort"
)

// ParamEscapeClass is the interprocedural escape classification of a
// callee parameter, required by the escape summary contract.
type ParamEscapeClass int

const (
	// NoEscape: the parameter is only read and never leaves the callee.
	NoEscape ParamEscapeClass = iota
	// ReturnEscape: the parameter (or an object reachable from it) is returned.
	ReturnEscape
	// ArgToArg: the parameter is stored into another parameter object.
	ArgToArg
	// GlobalEscape: the parameter reaches a global or an opaque external call.
	GlobalEscape
)

func (c ParamEscapeClass) String() string {
	switch c {
	case ReturnEscape:
		return "ReturnEscape"
	case ArgToArg:
		return "ArgToArg"
	case GlobalEscape:
		return "GlobalEscape"
	default:
		return "NoEscape"
	}
}

// ParamSummary describes one callee parameter.
type ParamSummary struct {
	Class ParamEscapeClass
	// ToArg holds the destination parameter indices for ArgToArg.
	ToArg []int
}

// FuncSummary is the interprocedural escape summary of one function.
type FuncSummary struct {
	Func   *Func
	Params []ParamSummary
}

// locKind identifies the origin of an abstract connection-graph node.
type locKind int

const (
	locAlloc locKind = iota
	locParam
	locGlobal
	locUnknown
)

// loc is an abstract location: an allocation, a parameter, a global object
// or an unknown/opaque location used for conservative handling.
type loc struct {
	kind locKind
	// func+index identify a parameter; func+name identify an allocation;
	// name alone identifies a global; unknown locations carry a unique tag.
	fn   *Func
	idx  int
	name string
	tag  int
	dyn  bool // reached through a dynamic (unknown) offset
}

// EscapeResult exposes the whole-program analysis outcome.
type EscapeResult struct {
	prog      *Program
	summaries map[string]*FuncSummary

	// Per-function final connection graph state.
	pointsTo map[*Func]map[string]map[loc]bool
	escaped  map[*Func]map[loc]bool
	allocLoc map[*Func]map[string]loc
	paramLoc map[*Func]map[string]loc
	locDyn   map[*Func]map[loc]bool
}

// Summaries returns the computed parameter summaries by function name.
func (r *EscapeResult) Summaries() map[string]*FuncSummary { return r.summaries }

// AllocEscaped reports whether a named allocation escapes the function.
func (r *EscapeResult) AllocEscaped(f *Func, name string) bool {
	l, ok := r.allocLoc[f][name]
	if !ok {
		return false
	}
	return r.escaped[f][l]
}

type escapeAnalyzer struct {
	prog      *Program
	summaries map[string]*FuncSummary
	nextTag   int
}

// AnalyzeEscape runs interprocedural escape analysis to a fixpoint. It
// supports direct and mutual recursion through monotonic summary iteration.
func AnalyzeEscape(prog *Program) *EscapeResult {
	az := &escapeAnalyzer{
		prog:      prog,
		summaries: map[string]*FuncSummary{},
	}
	for _, f := range prog.Funcs {
		s := &FuncSummary{Func: f, Params: make([]ParamSummary, len(f.Params))}
		for i := range s.Params {
			s.Params[i] = ParamSummary{Class: NoEscape}
		}
		az.summaries[f.Name] = s
	}

	// Monotonic fixpoint: every round can only raise escape classes.
	for changed := true; changed; {
		changed = false
		// Deterministic iteration order for reproducibility.
		names := make([]string, 0, len(prog.Funcs))
		for _, f := range prog.Funcs {
			names = append(names, f.Name)
		}
		sort.Strings(names)
		for _, name := range names {
			f := prog.Func(name)
			if az.analyzeFunc(f) {
				changed = true
			}
		}
	}

	res := &EscapeResult{
		prog:      prog,
		summaries: az.summaries,
		pointsTo:  map[*Func]map[string]map[loc]bool{},
		escaped:   map[*Func]map[loc]bool{},
		allocLoc:  map[*Func]map[string]loc{},
		paramLoc:  map[*Func]map[string]loc{},
		locDyn:    map[*Func]map[loc]bool{},
	}
	for _, f := range prog.Funcs {
		st := az.analyzeFuncState(f)
		res.pointsTo[f] = st.pointsTo
		res.escaped[f] = st.escaped
		res.allocLoc[f] = st.allocLoc
		res.paramLoc[f] = st.paramLoc
		res.locDyn[f] = st.locDyn
	}
	return res
}

type funcState struct {
	fn       *Func
	az       *escapeAnalyzer
	types    map[string]Type
	pointsTo map[string]map[loc]bool
	escaped  map[loc]bool
	allocLoc map[string]loc
	paramLoc map[string]loc
	locDyn   map[loc]bool
	// fieldEdges model the points-to/connection graph between abstract
	// objects: edge (base, field) -> target.
	fieldEdges map[edgeKey]map[loc]bool
	// worklist of locations whose escape flag or edges must be propagated.
	escQueue []loc
	// returns records locations that flow (directly or transitively) into a
	// return value; globalLocs records locations reaching globals/ext calls;
	// argEdges records parameter-to-parameter storage relationships.
	returns    map[loc]bool
	globalLocs map[loc]bool
	argEdges   map[loc]map[loc]bool
}

type edgeKey struct {
	base  loc
	field int // -1 stands for a dynamic/unknown field
}

func (az *escapeAnalyzer) newState(f *Func) *funcState {
	st := &funcState{
		fn:         f,
		az:         az,
		types:      regTypes(f),
		pointsTo:   map[string]map[loc]bool{},
		escaped:    map[loc]bool{},
		allocLoc:   map[string]loc{},
		paramLoc:   map[string]loc{},
		locDyn:     map[loc]bool{},
		fieldEdges: map[edgeKey]map[loc]bool{},
		returns:    map[loc]bool{},
		globalLocs: map[loc]bool{},
		argEdges:   map[loc]map[loc]bool{},
	}
	for i, p := range f.Params {
		l := loc{kind: locParam, fn: f, idx: i, name: p.Name}
		st.paramLoc[p.Name] = l
		st.bind(p.Name, l)
	}
	return st
}

func (st *funcState) bind(reg string, l loc) {
	set := st.pointsTo[reg]
	if set == nil {
		set = map[loc]bool{}
		st.pointsTo[reg] = set
	}
	if !set[l] {
		set[l] = true
	}
}

func (st *funcState) pts(reg string) map[loc]bool {
	return st.pointsTo[reg]
}

func (st *funcState) markEscape(l loc) {
	if !st.escaped[l] {
		st.escaped[l] = true
		st.escQueue = append(st.escQueue, l)
	}
}

func (st *funcState) markPtsEscape(set map[loc]bool) {
	for l := range set {
		st.markEscape(l)
	}
}

func (st *funcState) addFieldEdge(base loc, field int, target loc) {
	k := edgeKey{base: base, field: field}
	set := st.fieldEdges[k]
	if set == nil {
		set = map[loc]bool{}
		st.fieldEdges[k] = set
	}
	if !set[target] {
		set[target] = true
		// A newly discovered edge out of an already-escaping object makes
		// the target escape as well.
		if st.escaped[base] {
			st.markEscape(target)
		}
	}
}

// propagate closes escape flags across the connection graph: escaping an
// object escapes every object reachable through its fields, including across
// dynamic-offset edges.
func (st *funcState) propagate() {
	for len(st.escQueue) > 0 {
		l := st.escQueue[0]
		st.escQueue = st.escQueue[1:]
		for k, targets := range st.fieldEdges {
			if k.base != l {
				continue
			}
			for t := range targets {
				st.markEscape(t)
			}
		}
	}
}

func (st *funcState) unknownLoc() loc {
	st.az.nextTag++
	return loc{kind: locUnknown, tag: st.az.nextTag}
}

// analyzeFunc runs one intra-procedural analysis pass and recomputes the
// function summary; it reports whether the summary changed.
func (az *escapeAnalyzer) analyzeFunc(f *Func) bool {
	st := az.analyzeFuncState(f)
	sum := az.summaries[f.Name]
	changed := false
	for i := range f.Params {
		pl := st.paramLoc[f.Params[i].Name]
		newClass := NoEscape
		var toArg []int
		switch {
		case st.globalLocs[pl]:
			newClass = GlobalEscape
		case st.returns[pl]:
			newClass = ReturnEscape
		default:
			for j := range f.Params {
				if j == i {
					continue
				}
				target := st.paramLoc[f.Params[j].Name]
				if st.reachesParam(pl, target) {
					toArg = append(toArg, j)
				}
			}
			if len(toArg) > 0 {
				newClass = ArgToArg
			}
		}
		old := sum.Params[i]
		merged := mergeClass(old.Class, newClass)
		if merged != old.Class || !sameInts(old.ToArg, toArg) {
			changed = true
		}
		sum.Params[i] = ParamSummary{Class: merged, ToArg: toArg}
	}
	return changed
}

func mergeClass(a, b ParamEscapeClass) ParamEscapeClass {
	if a == GlobalEscape || b == GlobalEscape {
		return GlobalEscape
	}
	if a == ReturnEscape || b == ReturnEscape {
		return ReturnEscape
	}
	if a == ArgToArg || b == ArgToArg {
		return ArgToArg
	}
	return NoEscape
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// analyzeFuncState performs one context-insensitive intra-procedural pass,
// applying current callee summaries at call sites.
func (az *escapeAnalyzer) analyzeFuncState(f *Func) *funcState {
	st := az.newState(f)

	for _, b := range f.Blocks {
		for _, ins := range b.Insts {
			switch x := ins.(type) {
			case *Alloc:
				l := loc{kind: locAlloc, fn: f, name: x.Name}
				st.allocLoc[x.Name] = l
				st.bind(x.Name, l)
			case *GlobalAddr:
				l := loc{kind: locGlobal, name: x.G.Name}
				st.bind(x.Name, l)
			case *FieldAddr:
				st.analyzeField(x)
			case *AddrAdd:
				// Dynamic offset: keep points-to but flag uncertainty.
				for l := range st.pts(x.Ptr.Reg) {
					dyn := l
					dyn.dyn = true
					st.bind(x.Name, dyn)
					st.locDyn[dyn] = true
				}
			case *Load:
				st.analyzeLoad(x)
			case *Store:
				st.analyzeStore(x)
			case *Phi:
				for _, e := range x.Incoming {
					if e.Val.IsReg() {
						for l := range st.pts(e.Val.Reg) {
							st.bind(x.Name, l)
						}
					}
				}
			case *BinOp:
				// Integer-only; no pointer facts.
			case *Call:
				st.analyzeCall(x)
			case *ExtCall:
				st.analyzeExtCall(x)
			case *PrintInst:
				// Integers only; no pointer effect.
			}
		}
		switch t := b.Term.(type) {
		case *Return:
			if t.Val.IsReg() {
				for l := range st.pts(t.Val.Reg) {
					st.returns[l] = true
					st.markEscape(l)
					if l.kind == locUnknown {
						st.markEscape(l)
					}
				}
			}
		}
	}

	st.propagate()

	return st
}

func (st *funcState) analyzeField(x *FieldAddr) {
	if !x.Ptr.IsReg() {
		return
	}
	for base := range st.pts(x.Ptr.Reg) {
		// The field-address register points to the same abstract object (its
		// points-to set); field edges are added on stores.
		st.bind(x.Name, base)
	}
}

// fieldIndexOf returns the struct field index addressed by a register. A
// FieldAddr result names a static field; anything else yields -1 (dynamic).
func (st *funcState) fieldIndexOf(reg string) int {
	for _, b := range st.fn.Blocks {
		for _, ins := range b.Insts {
			if fa, ok := ins.(*FieldAddr); ok && fa.Name == reg {
				return fa.Field
			}
		}
	}
	return -1
}

func (st *funcState) analyzeCall(x *Call) {
	callee := st.az.prog.Func(x.Callee)
	if callee == nil {
		// Unknown callee: conservatively escape every pointer argument.
		for _, a := range x.Args {
			if a.IsReg() {
				st.markPtsEscape(st.pts(a.Reg))
			}
		}
		return
	}
	sum := st.az.summaries[x.Callee]
	argLocs := make([]map[loc]bool, len(x.Args))
	for i, a := range x.Args {
		if a.IsReg() {
			argLocs[i] = st.pts(a.Reg)
		} else {
			argLocs[i] = map[loc]bool{}
		}
	}

	for i, ps := range sum.Params {
		if i >= len(argLocs) {
			break
		}
		for src := range argLocs[i] {
			switch ps.Class {
			case NoEscape:
				// Nothing.
			case ReturnEscape:
				st.bind(x.Name, src)
			case GlobalEscape:
				st.globalLocs[src] = true
				st.markEscape(src)
			case ArgToArg:
				for _, j := range ps.ToArg {
					if j < len(argLocs) {
						// Argument i (source) was stored inside argument j
						// (destination): connect source -> destination field.
						for dst := range argLocs[j] {
							st.addFieldEdge(dst, -1, src)
							if dst.kind == locParam && src.kind == locParam {
								st.addArgEdge(src, dst)
							}
							if dst.kind == locGlobal {
								st.globalLocs[src] = true
								st.markEscape(src)
							}
						}
					}
				}
			}
		}
	}

	// If the callee's return value is unknown (e.g. it loads from an escaped
	// object), bind a fresh unknown location conservatively.
	if len(st.pts(x.Name)) == 0 && x.Name != "" {
		u := st.unknownLoc()
		st.bind(x.Name, u)
	}
}

func (st *funcState) analyzeExtCall(x *ExtCall) {
	for _, a := range x.Args {
		if a.IsReg() {
			for l := range st.pts(a.Reg) {
				st.globalLocs[l] = true
				st.markEscape(l)
			}
		}
	}
	if x.Name != "" {
		u := st.unknownLoc()
		st.bind(x.Name, u)
	}
}

func (st *funcState) analyzeLoad(x *Load) {
	if !x.Ptr.IsReg() {
		return
	}
	for base := range st.pts(x.Ptr.Reg) {
		// Try every field index: a field pointer may name a specific field,
		// while dynamic-offset pointers require conservative unknown edges.
		dyn := base.dyn || st.locDyn[base]
		if dyn {
			for k, targets := range st.fieldEdges {
				if k.base == base {
					for t := range targets {
						st.bind(x.Name, t)
					}
				}
			}
			continue
		}
		// Determine which field this pointer addresses by matching offsets.
		fi := st.fieldIndexOf(x.Ptr.Reg)
		k := edgeKey{base: base, field: fi}
		for t := range st.fieldEdges[k] {
			st.bind(x.Name, t)
		}
	}
}

func (st *funcState) analyzeStore(x *Store) {
	if !x.Ptr.IsReg() {
		return
	}
	valSet := map[loc]bool{}
	if x.Val.IsReg() {
		for l := range st.pts(x.Val.Reg) {
			valSet[l] = true
		}
	} else if x.Val.IsConst() && x.Val.Int == 0 {
		return
	}
	for base := range st.pts(x.Ptr.Reg) {
		dyn := base.dyn || st.locDyn[base]
		fi := st.fieldIndexOf(x.Ptr.Reg)
		if dyn {
			fi = -1
		}
		for vl := range valSet {
			st.addFieldEdge(base, fi, vl)
			// Track parameter-to-parameter storage for ArgToArg summaries.
			if base.kind == locParam && vl.kind == locParam {
				// vl (source) is stored into base (destination): source -> dest.
				st.addArgEdge(vl, base)
			}
			// Storing into a global means global escape.
			if base.kind == locGlobal {
				st.globalLocs[vl] = true
				st.markEscape(vl)
			}
		}
	}
}

func (st *funcState) addArgEdge(from, to loc) {
	set := st.argEdges[from]
	if set == nil {
		set = map[loc]bool{}
		st.argEdges[from] = set
	}
	set[to] = true
}

// reachesParam reports whether from reaches parameter target through
// parameter-to-parameter storage edges (transitive ArgToArg nesting).
func (st *funcState) reachesParam(from, target loc) bool {
	seen := map[loc]bool{}
	var walk func(loc) bool
	walk = func(l loc) bool {
		if l == target {
			return true
		}
		if seen[l] {
			return false
		}
		seen[l] = true
		for n := range st.argEdges[l] {
			if walk(n) {
				return true
			}
		}
		return false
	}
	return walk(from)
}
