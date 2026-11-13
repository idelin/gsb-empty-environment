package netsec

import (
	"strings"
	"testing"
)

func requireValidMaps(t *testing.T, fn *Function, maps ...StackMap) {
	t.Helper()
	report := CheckStackMaps(fn, maps)
	if !report.Valid {
		t.Fatalf("stack maps unexpectedly rejected:\n%s", formatIssues(report))
	}
}

func formatIssues(report *StackMapReport) string {
	if report == nil {
		return "<nil report>"
	}
	if len(report.Issues) == 0 {
		return "<no issues>"
	}
	lines := make([]string, len(report.Issues))
	for i, issue := range report.Issues {
		lines[i] = issue.String()
	}
	return strings.Join(lines, "\n")
}

func hasIssue(report *StackMapReport, kind MapIssueKind) bool {
	for _, issue := range report.Issues {
		if issue.Kind == kind {
			return true
		}
	}
	return false
}

func TestMissingRootCausesUseAfterFree(t *testing.T) {
	f := NewFunction("missing-root")
	entry := f.NewBlock("entry")
	obj := entry.Alloc("obj", 16, "alloc obj")
	p := entry.Copy(obj, "copy root")
	sp := entry.Safepoint("safepoint")
	entry.Dereference(p, "read p")
	entry.Return(NoValue, "done")

	report := CheckStackMaps(f, []StackMap{{At: sp}})
	if report.Valid {
		t.Fatal("map without the live pointer was accepted")
	}
	if !hasIssue(report, IssueMissingLivePointer) {
		t.Fatalf("expected missing-live-pointer issue, got %s", formatIssues(report))
	}
	found, ok := report.Analysis.Safepoint(sp)
	if !ok || !found.LivePtrs.Contains(p) {
		t.Fatalf("live set at safepoint = %v, want pointer %d", found.LivePtrs, p)
	}

	run := Execute(f, []StackMap{{At: sp}}, DefaultRunConfig())
	if run.Status != ExecUnsafe {
		t.Fatalf("status = %s, want unsafe; reason=%s", run.Status, run.Reason)
	}
	if run.UseAfterFree == nil {
		t.Fatalf("expected use-after-free, got %+v", run.Violations)
	}
	if run.UseAfterFree.Safepoint != sp {
		t.Fatalf("UAF safepoint = %d, want %d", run.UseAfterFree.Safepoint, sp)
	}
	last := run.UseAfterFree.Path[len(run.UseAfterFree.Path)-1]
	if last.Op != OpDereference {
		t.Fatalf("shortest path ends in %s, want Dereference: %s", last.Op, run.UseAfterFree.PathString())
	}
	if run.UseAfterFree.Steps != 4 {
		t.Fatalf("shortest path steps = %d, want 4: %s", run.UseAfterFree.Steps, run.UseAfterFree.PathString())
	}
}

func TestCorrectRootKeepsObjectAlive(t *testing.T) {
	f := NewFunction("correct-root")
	entry := f.NewBlock("entry")
	obj := entry.Alloc("obj", 16, "alloc obj")
	p := entry.Copy(obj, "copy root")
	sp := entry.Safepoint("safepoint")
	entry.Dereference(p, "read p")
	entry.Return(NoValue, "done")

	maps := []StackMap{{At: sp, Entries: []StackMapEntry{{Location: Slot(0), Value: p}}}}
	requireValidMaps(t, f, maps...)
	run := Execute(f, maps, DefaultRunConfig())
	if run.Status != ExecSafe {
		t.Fatalf("status = %s, want safe, reason=%s, violations=%+v", run.Status, run.Reason, run.Violations)
	}
}

func TestPointerDeadAfterSafepointNeedsNoRoot(t *testing.T) {
	f := NewFunction("dead-pointer")
	entry := f.NewBlock("entry")
	obj := entry.Alloc("obj", 16, "alloc obj")
	p := entry.Copy(obj, "p")
	entry.Dereference(p, "last use before safe point")
	sp := entry.Safepoint("safe point")
	entry.Return(NoValue, "done")

	maps := []StackMap{{At: sp}}
	requireValidMaps(t, f, maps...)
	run := Execute(f, maps, DefaultRunConfig())
	if run.Status != ExecSafe {
		t.Fatalf("status = %s, want safe: %s %+v", run.Status, run.Reason, run.Violations)
	}
}

func TestInteriorPointerRequiresBaseAndBounds(t *testing.T) {
	f := NewFunction("interior")
	entry := f.NewBlock("entry")
	obj := entry.Alloc("obj", 16, "alloc obj")
	field := entry.Field(obj, 8, "field at 8")
	sp := entry.Safepoint("safepoint")
	entry.Dereference(field, "read field")
	entry.Return(NoValue, "done")

	good := StackMap{At: sp, Entries: []StackMapEntry{{
		Location: Register(1), Value: field, DerivedBase: obj,
	}}}
	requireValidMaps(t, f, good)
	run := Execute(f, []StackMap{good}, DefaultRunConfig())
	if run.Status != ExecSafe {
		t.Fatalf("status = %s, want safe: %s %+v", run.Status, run.Reason, run.Violations)
	}

	missingBase := StackMap{At: sp, Entries: []StackMapEntry{{
		Location: Register(1), Value: field,
	}}}
	report := CheckStackMaps(f, []StackMap{missingBase})
	if !hasIssue(report, IssueMissingDerivedBase) {
		t.Fatalf("expected missing-derived-base, got %s", formatIssues(report))
	}

	bad := NewFunction("interior-oob")
	badEntry := bad.NewBlock("entry")
	badObj := badEntry.Alloc("bad", 16, "alloc bad")
	badEntry.Field(badObj, 16, "one past end")
	badEntry.Return(NoValue, "done")
	if err := Validate(bad); err == nil || !strings.Contains(err.Error(), "outside object") {
		t.Fatalf("out-of-bounds interior pointer error = %v", err)
	}
}

func TestSameSlotReuseAcrossSafepoints(t *testing.T) {
	f := NewFunction("slot-reuse")
	entry := f.NewBlock("entry")
	obj1 := entry.Alloc("obj1", 8, "alloc one")
	p1 := entry.Copy(obj1, "p1")
	sp1 := entry.Safepoint("safe one")
	entry.Dereference(p1, "read p1")
	obj2 := entry.Alloc("obj2", 8, "alloc two")
	p2 := entry.Copy(obj2, "p2")
	sp2 := entry.Safepoint("safe two")
	entry.Dereference(p2, "read p2")
	entry.Return(NoValue, "done")

	maps := []StackMap{
		{At: sp1, Entries: []StackMapEntry{{Location: Slot(0), Value: p1}}},
		{At: sp2, Entries: []StackMapEntry{{Location: Slot(0), Value: p2}}},
	}
	requireValidMaps(t, f, maps...)
	run := Execute(f, maps, DefaultRunConfig())
	if run.Status != ExecSafe {
		t.Fatalf("status = %s, want safe: %s %+v", run.Status, run.Reason, run.Violations)
	}
}

func TestCallExceptionEdgeKeepsPointerLive(t *testing.T) {
	f := NewFunction("call-exception")
	entry := f.NewBlock("entry")
	handler := f.NewBlock("handler")

	obj := entry.Alloc("obj", 16, "alloc obj")
	p := entry.Copy(obj, "p")
	sp := entry.Safepoint("before call")
	entry.Call("may_throw", CallSummary{
		MayThrow: true, Exception: handler.ID,
		Return: ReturnSpec{Kind: RetNone},
	}, p)
	entry.Return(NoValue, "normal")

	handler.Dereference(p, "handler uses p")
	handler.Return(NoValue, "caught")

	analysis, err := Analyze(f)
	if err != nil {
		t.Fatal(err)
	}
	info, ok := analysis.Safepoint(sp)
	if !ok || !info.LivePtrs.Contains(p) {
		t.Fatalf("exception edge did not keep %d live: %v", p, info.LivePtrs)
	}

	missing := CheckStackMaps(f, []StackMap{{At: sp}})
	if !hasIssue(missing, IssueMissingLivePointer) {
		t.Fatalf("expected missing live pointer on exception edge: %s", formatIssues(missing))
	}
	maps := []StackMap{{At: sp, Entries: []StackMapEntry{{Location: Slot(2), Value: p}}}}
	requireValidMaps(t, f, maps...)
	run := Execute(f, maps, DefaultRunConfig())
	if run.Status != ExecSafe {
		t.Fatalf("status = %s, want safe: %s %+v", run.Status, run.Reason, run.Violations)
	}
}

func TestLoopBackedgeKeepsPhiLive(t *testing.T) {
	f := NewFunction("loop")
	entry := f.NewBlock("entry")
	header := f.NewBlock("header")
	body := f.NewBlock("body")
	exit := f.NewBlock("exit")

	obj := entry.Alloc("obj", 16, "alloc obj")
	entry.Jump(header.ID, "enter loop")

	phi := header.PhiTyped(
		PtrType(NewObjectSet("obj"), OffsetRange{Start: 0, End: 1}),
		PhiIn{Pred: entry.ID, Value: obj},
		PhiIn{Pred: body.ID, Value: NoValue}, // fixed after the body is built
	)
	sp := header.Safepoint("loop safepoint")
	header.Jump(body.ID, "body")

	carried := body.Copy(phi, "carried pointer")
	body.Dereference(carried, "use carried pointer")
	one := body.Int(1, "leave loop")
	body.CondJump(one, exit.ID, header.ID, "branch")
	header.Instrs[0].Phi[1].Value = carried

	exit.Return(NoValue, "done")

	analysis, err := Analyze(f)
	if err != nil {
		t.Fatal(err)
	}
	info, ok := analysis.Safepoint(sp)
	if !ok || !info.LivePtrs.Contains(phi) {
		t.Fatalf("loop backedge did not keep Phi %d live: %v", phi, info.LivePtrs)
	}
	maps := []StackMap{{At: sp, Entries: []StackMapEntry{{Location: Register(0), Value: phi}}}}
	requireValidMaps(t, f, maps...)
	run := Execute(f, maps, DefaultRunConfig())
	if run.Status != ExecSafe {
		t.Fatalf("status = %s, want safe: %s %+v", run.Status, run.Reason, run.Violations)
	}
}

func TestPhiWithOnePointerArmAndMislabeledInteger(t *testing.T) {
	f := NewFunction("phi-one-pointer")
	entry := f.NewBlock("entry")
	left := f.NewBlock("left")
	right := f.NewBlock("right")
	merge := f.NewBlock("merge")

	obj := entry.Alloc("obj", 16, "alloc obj")
	raw := entry.Int(DefaultHeapBase, "integer equal to obj address")
	chooseRight := entry.Int(0, "take right")
	entry.CondJump(chooseRight, left.ID, right.ID, "branch")
	left.Jump(merge.ID, "left to merge")
	right.Jump(merge.ID, "right to merge")

	phi := merge.Phi(
		PhiIn{Pred: left.ID, Value: obj},
		PhiIn{Pred: right.ID, Value: raw},
	)
	sp := merge.Safepoint("merge safepoint")
	merge.Dereference(phi, "use phi")
	merge.Return(NoValue, "done")

	maps := []StackMap{{At: sp, Entries: []StackMapEntry{{Location: Slot(1), Value: phi}}}}
	requireValidMaps(t, f, maps...)
	run := Execute(f, maps, DefaultRunConfig())
	if run.Status != ExecUnsafe {
		t.Fatalf("status = %s, want unsafe: %s %+v", run.Status, run.Reason, run.Violations)
	}
	if run.Mislabeled == nil {
		t.Fatalf("expected separately reported mislabeled integer, got %+v", run.Violations)
	}
	if run.Mislabeled.Object != "obj" || run.Mislabeled.Generation != 1 {
		t.Fatalf("mislabeled object metadata = %q/%d, want obj/1", run.Mislabeled.Object, run.Mislabeled.Generation)
	}
}

func TestCallReturnProvenance(t *testing.T) {
	t.Run("alias argument", func(t *testing.T) {
		f := NewFunction("call-alias")
		entry := f.NewBlock("entry")
		obj := entry.Alloc("obj", 16, "alloc")
		sp1 := entry.Safepoint("before call")
		p := entry.Call("alias", CallSummary{
			LiveArgs: []int{0},
			Return:   ReturnSpec{Kind: RetArg, Arg: 0},
		}, obj)
		sp2 := entry.Safepoint("after call")
		entry.Dereference(p, "read returned pointer")
		entry.Return(NoValue, "done")

		maps := []StackMap{
			{At: sp1, Entries: []StackMapEntry{{Location: Register(0), Value: obj}}},
			{At: sp2, Entries: []StackMapEntry{{Location: Register(0), Value: p}}},
		}
		requireValidMaps(t, f, maps...)
		run := Execute(f, maps, DefaultRunConfig())
		if run.Status != ExecSafe {
			t.Fatalf("status = %s, want safe: %s %+v", run.Status, run.Reason, run.Violations)
		}
	})

	t.Run("fresh return", func(t *testing.T) {
		f := NewFunction("call-fresh")
		entry := f.NewBlock("entry")
		p := entry.Call("make", CallSummary{
			Return: ReturnSpec{Kind: RetFresh, Object: "fresh", Size: 8},
		})
		sp := entry.Safepoint("after call")
		entry.Dereference(p, "read fresh")
		entry.Return(NoValue, "done")
		maps := []StackMap{{At: sp, Entries: []StackMapEntry{{Location: Register(2), Value: p}}}}
		requireValidMaps(t, f, maps...)
		run := Execute(f, maps, DefaultRunConfig())
		if run.Status != ExecSafe {
			t.Fatalf("status = %s, want safe: %s %+v", run.Status, run.Reason, run.Violations)
		}
	})
}

func TestDuplicateLocationRejected(t *testing.T) {
	f := NewFunction("duplicate")
	entry := f.NewBlock("entry")
	a := entry.Alloc("a", 8, "a")
	b := entry.Alloc("b", 8, "b")
	sp := entry.Safepoint("safe")
	entry.Dereference(a, "a")
	entry.Dereference(b, "b")
	entry.Return(NoValue, "done")

	report := CheckStackMaps(f, []StackMap{{
		At: sp,
		Entries: []StackMapEntry{
			{Location: Slot(0), Value: a},
			{Location: Slot(0), Value: b},
		},
	}})
	if !hasIssue(report, IssueDuplicateLocation) {
		t.Fatalf("expected duplicate-location issue, got %s", formatIssues(report))
	}
}

func TestNonPointerLocationRejected(t *testing.T) {
	f := NewFunction("nonptr")
	entry := f.NewBlock("entry")
	n := entry.Int(7, "seven")
	sp := entry.Safepoint("safe")
	entry.Return(NoValue, "done")

	report := CheckStackMaps(f, []StackMap{{
		At: sp, Entries: []StackMapEntry{{Location: Register(0), Value: n}},
	}})
	if !hasIssue(report, IssueNonPointer) {
		t.Fatalf("expected non-pointer-mapped issue, got %s", formatIssues(report))
	}
}

func TestIntegerMislabelAliasesLiveObject(t *testing.T) {
	f := NewFunction("mislabel-direct")
	entry := f.NewBlock("entry")
	entry.Alloc("obj", 16, "alloc obj")
	raw := entry.Int(DefaultHeapBase, "raw address bits")
	sp := entry.Safepoint("safe")
	entry.Return(NoValue, "done")

	maps := []StackMap{{
		At: sp, Entries: []StackMapEntry{{Location: Register(3), Value: raw}},
	}}
	report := CheckStackMaps(f, maps)
	if !hasIssue(report, IssueNonPointer) {
		t.Fatalf("static non-pointer map was not rejected: %s", formatIssues(report))
	}
	run := Execute(f, maps, DefaultRunConfig())
	if run.Mislabeled == nil {
		t.Fatalf("expected a separate mislabeled-integer finding, got %+v", run.Violations)
	}
	if run.Mislabeled.Object != "obj" || run.Mislabeled.Generation != 1 || run.Mislabeled.Address != DefaultHeapBase {
		t.Fatalf("mislabel metadata = %+v", run.Mislabeled)
	}
}

func TestStaleFieldPointerFailsAtDereference(t *testing.T) {
	f := NewFunction("stale-field")
	entry := f.NewBlock("entry")
	obj := entry.Alloc("obj", 16, "alloc obj")
	p := entry.Copy(obj, "root")
	sp := entry.Safepoint("reclaim obj")
	field := entry.Field(p, 8, "compute stale field")
	entry.Dereference(field, "read stale field")
	entry.Return(NoValue, "done")

	run := Execute(f, []StackMap{{At: sp}}, DefaultRunConfig())
	if run.UseAfterFree == nil {
		t.Fatalf("expected UAF, got %+v", run.Violations)
	}
	last := run.UseAfterFree.Path[len(run.UseAfterFree.Path)-1]
	if last.Op != OpDereference {
		t.Fatalf("UAF was reported at %s instead of Dereference: %s", last.Op, run.UseAfterFree.PathString())
	}
}

func TestLaterMapCannotResurrectOlderGeneration(t *testing.T) {
	f := NewFunction("late-map")
	entry := f.NewBlock("entry")
	obj := entry.Alloc("obj", 16, "alloc obj")
	p := entry.Copy(obj, "root")
	first := entry.Safepoint("first, map omitted")
	second := entry.Safepoint("second, pointer listed")
	entry.Dereference(p, "read after two safe points")
	entry.Return(NoValue, "done")

	maps := []StackMap{
		{At: first},
		{At: second, Entries: []StackMapEntry{{Location: Register(0), Value: p}}},
	}
	report := CheckStackMaps(f, maps)
	if !hasIssue(report, IssueMissingLivePointer) {
		t.Fatalf("expected first safepoint to miss its live pointer: %s", formatIssues(report))
	}
	run := Execute(f, maps, DefaultRunConfig())
	if run.UseAfterFree == nil {
		t.Fatalf("late map resurrected the reclaimed generation: %+v", run.Violations)
	}
	if run.UseAfterFree.Safepoint != first {
		t.Fatalf("reclaim safepoint = %d, want first safepoint %d", run.UseAfterFree.Safepoint, first)
	}
}

func TestBudgetExhaustionRejectsMap(t *testing.T) {
	f := NewFunction("budget-loop")
	entry := f.NewBlock("entry")
	loop := f.NewBlock("loop")

	entry.Alloc("obj", 8, "initial")
	sp1 := entry.Safepoint("before loop")
	entry.Jump(loop.ID, "loop")

	loop.Alloc("loopobj", 8, "fresh each iteration")
	sp2 := loop.Safepoint("in loop")
	loop.Jump(loop.ID, "again")

	maps := []StackMap{{At: sp1}, {At: sp2}}
	requireValidMaps(t, f, maps...)
	cfg := DefaultRunConfig()
	cfg.MaxSteps = 50
	cfg.MaxStates = 100
	cfg.MaxObjects = 6
	cfg.MaxAllocations = 6
	run := Execute(f, maps, cfg)
	if run.Status != ExecRejected {
		t.Fatalf("status = %s, want rejected; violations=%+v", run.Status, run.Violations)
	}
	if !run.BudgetExhausted {
		t.Fatalf("expected budget exhaustion, reason=%s", run.Reason)
	}
}

func TestInvalidDerivedBaseObjectMismatch(t *testing.T) {
	f := NewFunction("bad-base")
	entry := f.NewBlock("entry")
	a := entry.Alloc("a", 16, "a")
	b := entry.Alloc("b", 16, "b")
	field := entry.Field(a, 8, "a field")
	sp := entry.Safepoint("safe")
	entry.Dereference(field, "read")
	entry.Return(NoValue, "done")

	report := CheckStackMaps(f, []StackMap{{
		At: sp,
		Entries: []StackMapEntry{{
			Location: Slot(0), Value: field, DerivedBase: b,
		}},
	}})
	if !hasIssue(report, IssueInvalidDerivedBase) {
		t.Fatalf("expected invalid-derived-base, got %s", formatIssues(report))
	}
}
