package netsec

import "testing"

// --- node builders -------------------------------------------------------

func alloc(id NodeID, dst string, next NodeID) *Node {
	return &Node{ID: id, Kind: KindAlloc, Dst: dst, Next: []NodeID{next}}
}

func cp(id NodeID, dst, src string, next NodeID) *Node {
	return &Node{ID: id, Kind: KindCopy, Dst: dst, Src: src, Next: []NodeID{next}}
}

func load(id NodeID, dst, base, field string, next NodeID) *Node {
	return &Node{ID: id, Kind: KindLoad, Dst: dst, Base: base, Field: field, Next: []NodeID{next}}
}

func store(id NodeID, base, field, src string, next NodeID) *Node {
	return &Node{ID: id, Kind: KindStore, Base: base, Field: field, Src: src, Next: []NodeID{next}}
}

func freeN(id NodeID, src string, next NodeID) *Node {
	return &Node{ID: id, Kind: KindFree, Src: src, Next: []NodeID{next}}
}

func deref(id NodeID, src string, next NodeID) *Node {
	return &Node{ID: id, Kind: KindDeref, Src: src, Next: []NodeID{next}}
}

func branch(id NodeID, then, els NodeID) *Node {
	return &Node{ID: id, Kind: KindBranch, Next: []NodeID{then, els}}
}

func call(id NodeID, dst, callee string, args []string, next NodeID) *Node {
	return &Node{ID: id, Kind: KindCall, Dst: dst, Callee: callee, Args: args, Next: []NodeID{next}}
}

func ret(id NodeID) *Node {
	return &Node{ID: id, Kind: KindReturn}
}

func mkProc(name, result string, params []string, entry NodeID, nodes ...*Node) *Proc {
	p := &Proc{Name: name, Params: params, Result: result, Entry: entry, Nodes: map[NodeID]*Node{}}
	for _, n := range nodes {
		p.Nodes[n.ID] = n
	}
	return p
}

func mkProgram(main string, procs ...*Proc) *Program {
	p := &Program{Main: main, Procs: map[string]*Proc{}}
	for _, proc := range procs {
		p.Procs[proc.Name] = proc
	}
	return p
}

// --- helpers -------------------------------------------------------------

func findReport(res *Result, kind ErrKind, proc string, node NodeID, obj Obj) *Report {
	for _, r := range res.Reports {
		if r.Kind == kind && r.Proc == proc && r.Node == node && r.Object == obj {
			return r
		}
	}
	return nil
}

func analyzeOK(t *testing.T, prog *Program) *Result {
	t.Helper()
	res, err := Analyze(prog)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return res
}

func checkCoversAll(t *testing.T, prog *Program, res *Result) *CheckResult {
	t.Helper()
	cr := Check(prog, res, DefaultBounds)
	if len(cr.Missing) > 0 {
		t.Fatalf("abstract reports miss real errors: %v", cr.Missing)
	}
	return cr
}

// --- programs ------------------------------------------------------------

// x = new; free x; use *x
func progBasicUAF() *Program {
	return mkProgram("main", mkProc("main", "", nil, 0,
		alloc(0, "x", 1),
		freeN(1, "x", 2),
		deref(2, "x", 3),
		ret(3),
	))
}

// x = new; y = x; free x; free y
func progDoubleFreeAlias() *Program {
	return mkProgram("main", mkProc("main", "", nil, 0,
		alloc(0, "x", 1),
		cp(1, "y", "x", 2),
		freeN(2, "x", 3),
		freeN(3, "y", 4),
		ret(4),
	))
}

// x = new; y = new; if * { y = x } else { z = y }; free x; free y
// The y==x alias exists on only one branch and must be kept.
func progMaybeAlias() *Program {
	return mkProgram("main", mkProc("main", "", nil, 0,
		alloc(0, "x", 1),
		alloc(1, "y", 2),
		branch(2, 3, 4),
		cp(3, "y", "x", 5),
		cp(4, "z", "y", 5),
		freeN(5, "x", 6),
		freeN(6, "y", 7),
		ret(7),
	))
}

// o = new; x = new; o.f = x; free x; y = o.f; use *y
func progFieldUAF() *Program {
	return mkProgram("main", mkProc("main", "", nil, 0,
		alloc(0, "o", 1),
		alloc(1, "x", 2),
		store(2, "o", "f", "x", 3),
		freeN(3, "x", 4),
		load(4, "y", "o", "f", 5),
		deref(5, "y", 6),
		ret(6),
	))
}

// Field sensitivity: storing x into o.g must not taint o.f.
func progFieldNegative() *Program {
	return mkProgram("main", mkProc("main", "", nil, 0,
		alloc(0, "o", 1),
		alloc(1, "x", 2),
		store(2, "o", "g", "x", 3),
		freeN(3, "x", 4),
		load(4, "y", "o", "f", 5),
		deref(5, "y", 6),
		ret(6),
	))
}

// Weak updates: o.f overwritten by y must keep the old value x.
// o = new; x = new; y = new; o.f = x; o.f = y; free x; z = o.f; use *z
func progWeakUpdate() *Program {
	return mkProgram("main", mkProc("main", "", nil, 0,
		alloc(0, "o", 1),
		alloc(1, "x", 2),
		alloc(2, "y", 3),
		store(3, "o", "f", "x", 4),
		store(4, "o", "f", "y", 5),
		freeN(5, "x", 6),
		load(6, "z", "o", "f", 7),
		deref(7, "z", 8),
		ret(8),
	))
}

// kill(p) frees p; main allocates x, kills it, then dereferences it.
func progInterprocUAF() *Program {
	kill := mkProc("kill", "", []string{"p"}, 0,
		freeN(0, "p", 1),
		ret(1),
	)
	main := mkProc("main", "", nil, 0,
		alloc(0, "x", 1),
		call(1, "r", "kill", []string{"x"}, 2),
		deref(2, "x", 3),
		ret(3),
	)
	return mkProgram("main", main, kill)
}

// kill(p) frees p; main kills x twice.
func progInterprocDoubleFree() *Program {
	kill := mkProc("kill", "", []string{"p"}, 0,
		freeN(0, "p", 1),
		ret(1),
	)
	main := mkProc("main", "", nil, 0,
		alloc(0, "x", 1),
		call(1, "r", "kill", []string{"x"}, 2),
		call(2, "r", "kill", []string{"x"}, 3),
		ret(3),
	)
	return mkProgram("main", main, kill)
}

// rec(p): if * { rec(p) }; free p  — recursive double free via summaries.
func progRecursion() *Program {
	rec := mkProc("rec", "", []string{"p"}, 0,
		branch(0, 1, 2),
		call(1, "r", "rec", []string{"p"}, 2),
		freeN(2, "p", 3),
		ret(3),
	)
	main := mkProc("main", "", nil, 0,
		alloc(0, "x", 1),
		call(1, "r", "rec", []string{"x"}, 2),
		ret(2),
	)
	return mkProgram("main", main, rec)
}

// x = new; y = new; if * { p = y } else { p = x }; free x; use *p
// The recorded witness for the x-report goes through the p = y branch,
// which cannot reproduce the error concretely: it must be flagged unproven
// even though the error itself is real (via the else branch).
func progUnprovenPath() *Program {
	return mkProgram("main", mkProc("main", "", nil, 0,
		alloc(0, "x", 1),
		alloc(1, "y", 2),
		branch(2, 3, 4),
		cp(3, "p", "y", 5),
		cp(4, "p", "x", 5),
		freeN(5, "x", 6),
		deref(6, "p", 7),
		ret(7),
	))
}

// --- tests ---------------------------------------------------------------

func TestBasicUseAfterFree(t *testing.T) {
	res := analyzeOK(t, progBasicUAF())
	if len(res.Reports) != 1 {
		t.Fatalf("want 1 report, got %d: %v", len(res.Reports), res.Reports)
	}
	r := res.Reports[0]
	if r.Kind != UseAfterFree || r.Node != 2 || r.Object != (Obj{"main", 0}) {
		t.Fatalf("unexpected report: %v", r)
	}
	cr := checkCoversAll(t, progBasicUAF(), res)
	if !r.Proven {
		t.Fatalf("report should be proven, unproven: %v", cr.Unproven)
	}
}

func TestDoubleFreeThroughAlias(t *testing.T) {
	res := analyzeOK(t, progDoubleFreeAlias())
	r := findReport(res, DoubleFree, "main", 3, Obj{"main", 0})
	if r == nil {
		t.Fatalf("no double-free report: %v", res.Reports)
	}
	checkCoversAll(t, progDoubleFreeAlias(), res)
	if !r.Proven {
		t.Fatal("double-free report should be proven")
	}
}

func TestMaybeAliasIsKept(t *testing.T) {
	prog := progMaybeAlias()
	res := analyzeOK(t, prog)
	r := findReport(res, DoubleFree, "main", 6, Obj{"main", 0})
	if r == nil {
		t.Fatalf("uncertain alias was dropped; reports: %v", res.Reports)
	}
	cr := checkCoversAll(t, prog, res)
	if !r.Proven {
		t.Fatalf("alias branch is concretely reachable, report should be proven; unproven: %v", cr.Unproven)
	}
}

func TestFieldSensitive(t *testing.T) {
	res := analyzeOK(t, progFieldUAF())
	r := findReport(res, UseAfterFree, "main", 5, Obj{"main", 1})
	if r == nil {
		t.Fatalf("no field UAF report: %v", res.Reports)
	}
	checkCoversAll(t, progFieldUAF(), res)

	neg := analyzeOK(t, progFieldNegative())
	if len(neg.Reports) != 0 {
		t.Fatalf("field-insensitive false positive: %v", neg.Reports)
	}
}

func TestWeakUpdateKeepsOldValue(t *testing.T) {
	prog := progWeakUpdate()
	res := analyzeOK(t, prog)
	if findReport(res, UseAfterFree, "main", 7, Obj{"main", 1}) == nil {
		t.Fatalf("weak update lost the overwritten value; reports: %v", res.Reports)
	}
	checkCoversAll(t, prog, res)
}

func TestInterprocedural(t *testing.T) {
	uaf := progInterprocUAF()
	res := analyzeOK(t, uaf)
	if findReport(res, UseAfterFree, "main", 2, Obj{"main", 0}) == nil {
		t.Fatalf("no interprocedural UAF: %v", res.Reports)
	}
	checkCoversAll(t, uaf, res)

	df := progInterprocDoubleFree()
	res2 := analyzeOK(t, df)
	r := findReport(res2, DoubleFree, "kill", 0, Obj{"main", 0})
	if r == nil {
		t.Fatalf("no interprocedural double free: %v", res2.Reports)
	}
	checkCoversAll(t, df, res2)
	if !r.Proven {
		t.Fatal("second call kills the same object; report should be proven")
	}
	// The witness must cross the call boundary: main nodes, then kill node.
	if len(r.Path) == 0 || r.Path[len(r.Path)-1].Proc != "kill" {
		t.Fatalf("witness path does not enter callee: %v", r.Path)
	}
}

func TestRecursiveSummaryFixpoint(t *testing.T) {
	prog := progRecursion()
	res := analyzeOK(t, prog)
	if findReport(res, DoubleFree, "rec", 2, Obj{"main", 0}) == nil {
		t.Fatalf("recursive double free not found: %v", res.Reports)
	}
	// The concrete interpreter (depth-bounded) sees the same real error.
	checkCoversAll(t, prog, res)
}

func TestUnprovenPathIsFlagged(t *testing.T) {
	prog := progUnprovenPath()
	res := analyzeOK(t, prog)
	r := findReport(res, UseAfterFree, "main", 6, Obj{"main", 0})
	if r == nil {
		t.Fatalf("no UAF report: %v", res.Reports)
	}
	cr := checkCoversAll(t, prog, res)
	if r.Proven {
		t.Fatalf("witness %v goes through the p=y branch and cannot reproduce the error", r.Path)
	}
	found := false
	for _, u := range cr.Unproven {
		if u == r {
			found = true
		}
	}
	if !found {
		t.Fatal("report with unreplayable path must appear in Unproven")
	}
}

func TestCoverageAcrossPrograms(t *testing.T) {
	progs := map[string]*Program{
		"basic-uaf":      progBasicUAF(),
		"double-free":    progDoubleFreeAlias(),
		"maybe-alias":    progMaybeAlias(),
		"field-uaf":      progFieldUAF(),
		"field-negative": progFieldNegative(),
		"weak-update":    progWeakUpdate(),
		"interproc-uaf":  progInterprocUAF(),
		"interproc-df":   progInterprocDoubleFree(),
		"recursion":      progRecursion(),
		"unproven-path":  progUnprovenPath(),
	}
	for name, prog := range progs {
		t.Run(name, func(t *testing.T) {
			res := analyzeOK(t, prog)
			checkCoversAll(t, prog, res)
		})
	}
}

func TestValidateRejectsBadPrograms(t *testing.T) {
	bad := &Program{Main: "main", Procs: map[string]*Proc{}}
	if _, err := Analyze(bad); err == nil {
		t.Fatal("expected validation error for missing main")
	}
}
