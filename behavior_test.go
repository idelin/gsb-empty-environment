package taskpkg

import "testing"

func TestBasicSanitizeThenForward(t *testing.T) {
	san := leafCheck(0, "url", "san", true, EmitOnTrue)
	rule := &Rule{
		Inputs: 1,
		Body: seqStmt(2,
			checkStmt(1, san),
			forwardStmt(3, "fwd", Mark{Field: "url", Name: "san", Trusted: true}),
		),
	}
	prog := mustCompile(t, rule)

	deny, err := Interpret(rule, []bool{false})
	if err != nil {
		t.Fatal(err)
	}
	if deny.Decision != DecisionDenied || len(deny.Marks) != 0 {
		t.Fatalf("want denied with no marks, got %+v", deny)
	}
	if !hasEdge(deny.Edges, EdgeForwardDeny) {
		t.Fatalf("missing forward.deny edge: %+v", deny.Edges)
	}

	allow, err := prog.Evaluate([]bool{true})
	if err != nil {
		t.Fatal(err)
	}
	if allow.Decision != DecisionForwarded || len(allow.Marks) != 1 {
		t.Fatalf("want forwarded with 1 mark, got %+v", allow)
	}
	if !hasEdge(allow.Edges, EdgeForwardAllow) {
		t.Fatalf("missing forward.allow edge: %+v", allow.Edges)
	}
}

func TestUntrustedMarkDoesNotSatisfy(t *testing.T) {
	san := leafCheck(0, "url", "san", false, EmitAlways)
	rule := &Rule{
		Inputs: 1,
		Body: seqStmt(2,
			checkStmt(1, san),
			forwardStmt(3, "fwd", Mark{Field: "url", Name: "san", Trusted: true}),
		),
	}
	tr, err := mustCompile(t, rule).Evaluate([]bool{true})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Decision != DecisionDenied {
		t.Fatalf("untrusted mark must not satisfy trusted requirement, got %s", tr.Decision)
	}
}

func TestShortCircuitEdges(t *testing.T) {
	rule := &Rule{
		Inputs: 2,
		Body: branchStmt(3,
			andExpr(2, checkExpr(1, pureCheck(0, "a")), checkExpr(4, pureCheck(1, "b"))),
			rejectStmt(5, "x"), nil),
	}
	prog := mustCompile(t, rule)

	tr, err := prog.Evaluate([]bool{false, true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Leaves) != 1 {
		t.Fatalf("want 1 leaf after short circuit, got %d: %+v", len(tr.Leaves), tr.Leaves)
	}
	if !hasEdge(tr.Edges, EdgeAndFalse) {
		t.Fatalf("missing and.false short-circuit edge: %+v", tr.Edges)
	}

	tr2, err := prog.Evaluate([]bool{true, false})
	if err != nil {
		t.Fatal(err)
	}
	if !hasEdge(tr2.Edges, EdgeAndTrue) {
		t.Fatalf("missing and.true edge: %+v", tr2.Edges)
	}
	if len(tr2.Leaves) != 2 {
		t.Fatalf("want both leaves evaluated, got %d", len(tr2.Leaves))
	}
}

func TestExceptionCaughtAndUnhandled(t *testing.T) {
	raising := pureCheck(0, "a")
	raising.RaiseOn = boolPtr(true)
	rule := &Rule{
		Inputs: 1,
		Body:   tryStmt(2, checkStmt(1, raising), rejectStmt(3, "caught")),
	}
	prog := mustCompile(t, rule)

	caught, err := prog.Evaluate([]bool{true})
	if err != nil {
		t.Fatal(err)
	}
	if caught.Decision != DecisionRejected || caught.RejectReason != "caught" {
		t.Fatalf("want caught reject, got %+v", caught)
	}
	if !hasEdge(caught.Edges, EdgeLeafRaise) || !hasEdge(caught.Edges, EdgeTryCatch) {
		t.Fatalf("missing raise/catch edges: %+v", caught.Edges)
	}

	ok, err := prog.Evaluate([]bool{false})
	if err != nil {
		t.Fatal(err)
	}
	if ok.Decision != DecisionPassed {
		t.Fatalf("want pass when no raise, got %s", ok.Decision)
	}

	rule2 := &Rule{Inputs: 1, Body: checkStmt(1, raising)}
	esc, err := mustCompile(t, rule2).Evaluate([]bool{true})
	if err != nil {
		t.Fatal(err)
	}
	if esc.Decision != DecisionUnhandled || !hasEdge(esc.Edges, EdgeRaiseEscape) {
		t.Fatalf("want unhandled escape, got %+v", esc)
	}
}

func TestPureCheckSharedButEffectfulNot(t *testing.T) {
	pure := pureCheck(0, "a")
	effect := leafCheck(1, "b", "m", true, EmitAlways)
	rule := &Rule{
		Inputs: 2,
		Body: seqStmt(5,
			seqStmt(4, checkStmt(1, pure), checkStmt(2, pure)),
			seqStmt(6, checkStmt(3, effect), checkStmt(7, effect)),
		),
	}
	prog := mustCompile(t, rule)

	tr, err := prog.Evaluate([]bool{true, true})
	if err != nil {
		t.Fatal(err)
	}
	var cachedPure, execEffect int
	for _, leaf := range tr.Leaves {
		if leaf.Check == 0 && leaf.Cached {
			cachedPure++
		}
		if leaf.Check == 1 && len(leaf.Marks) == 1 {
			execEffect++
		}
	}
	if cachedPure != 1 {
		t.Fatalf("want 1 cached pure occurrence, got %d", cachedPure)
	}
	if execEffect != 2 {
		t.Fatalf("effectful check must execute twice, got %d", execEffect)
	}
	if len(tr.Marks) != 2 {
		t.Fatalf("want 2 marks from two effectful executions, got %d", len(tr.Marks))
	}

	it, err := Interpret(rule, []bool{true, true})
	if err != nil {
		t.Fatal(err)
	}
	for _, leaf := range it.Leaves {
		if leaf.Cached {
			t.Fatalf("interpreter must not cache: %+v", leaf)
		}
	}
}

func TestValidationRejectsTooManyInputs(t *testing.T) {
	rule := &Rule{Inputs: MaxInputs + 1, Body: rejectStmt(1, "x")}
	if _, err := Compile(rule); err == nil {
		t.Fatal("expected validation error for > MaxInputs")
	}
}

func TestForwardThenRejectDecisionPrecedence(t *testing.T) {
	san := leafCheck(0, "url", "san", true, EmitAlways)
	rule := &Rule{
		Inputs: 1,
		Body: seqStmt(4,
			seqStmt(2, checkStmt(1, san),
				forwardStmt(3, "early", Mark{Field: "url", Name: "san", Trusted: true})),
			rejectStmt(5, "later"),
		),
	}
	prog := mustCompile(t, rule)
	tr, err := prog.Evaluate([]bool{true})
	if err != nil {
		t.Fatal(err)
	}
	// The forward allowed and execution continued into the reject.
	if tr.Decision != DecisionRejected || tr.RejectReason != "later" {
		t.Fatalf("reject after an allowed forward must win, got %+v", tr)
	}
	if !hasEdge(tr.Edges, EdgeForwardAllow) || !hasEdge(tr.Edges, EdgeReject) {
		t.Fatalf("expected both forward.allow and reject edges, got %+v", tr.Edges)
	}
}

func TestDenyStopsExecution(t *testing.T) {
	san := leafCheck(0, "url", "san", true, EmitOnTrue)
	rule := &Rule{
		Inputs: 1,
		Body: seqStmt(2,
			seqStmt(3, checkStmt(1, san),
				forwardStmt(4, "fwd", Mark{Field: "url", Name: "san", Trusted: true})),
			rejectStmt(5, "shouldNotRun"),
		),
	}
	prog := mustCompile(t, rule)
	tr, err := prog.Evaluate([]bool{false})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Decision != DecisionDenied || hasEdge(tr.Edges, EdgeReject) {
		t.Fatalf("deny must terminate before the reject, got %+v", tr)
	}
}
