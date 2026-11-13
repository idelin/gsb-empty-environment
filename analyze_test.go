package netsec_test

import (
	"testing"

	netsec "example.org/netsec/q0102"
)

func local(id string, size, align uint64) netsec.Object {
	return netsec.Object{
		ID:      netsec.ObjectID(id),
		Size:    size,
		Align:   align,
		Purpose: netsec.PurposeLocal,
	}
}

func addr(id, obj string) netsec.Value {
	return netsec.Value{
		ID:     netsec.ValueID(id),
		Kind:   netsec.OpAddrOf,
		Object: netsec.ObjectID(obj),
	}
}

func readOnly(id, callee string, args ...string) netsec.Value {
	ids := make([]netsec.ValueID, len(args))
	for i, arg := range args {
		ids[i] = netsec.ValueID(arg)
	}
	return netsec.Value{
		ID:     netsec.ValueID(id),
		Kind:   netsec.OpPassCall,
		Callee: netsec.FunctionID(callee),
		Args:   ids,
		Summary: &netsec.CallSummary{Params: []netsec.ParamSummary{{
			Index:     0,
			NoCapture: true,
			ReadOnly:  true,
			MaxOffset: 7,
		}}},
	}
}

func class(t *testing.T, r *netsec.Report, id string) netsec.StackClass {
	t.Helper()
	return r.ClassOf(netsec.ObjectID(id))
}

func TestSafeReadOnlySlice(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{{
			ID:      "slice",
			Size:    64,
			Align:   8,
			Purpose: netsec.PurposeSliceBacking,
		}},
		Values: []netsec.Value{
			addr("p", "slice"),
			{
				ID:   "field",
				Kind: netsec.OpGEP,
				Base: "p",
				Index: &netsec.IndexRange{
					Min:      0,
					Max:      3,
					ElemSize: 8,
				},
			},
			readOnly("read", "reader", "field"),
		},
	}
	r := netsec.Analyze(g)
	if got := class(t, r, "slice"); got != netsec.ClassSafe {
		t.Fatalf("slice class = %s, want safe; reasons=%v", got, r.Objects["slice"].Reasons)
	}
}

func TestPhiBranchEscape(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("buf", 16, 8)},
		Values: []netsec.Value{
			addr("p1", "buf"),
			addr("p2", "buf"),
			{ID: "phi", Kind: netsec.OpPhi, Inputs: []netsec.ValueID{"p1", "p2"}},
			{ID: "ret", Kind: netsec.OpReturnPtr, Ptr: "p1"},
			readOnly("read", "reader", "phi"),
		},
	}
	r := netsec.Analyze(g)
	if got := class(t, r, "buf"); got != netsec.ClassUnsafe {
		t.Fatalf("buf class = %s, want unsafe", got)
	}
	if !hasReason(r.Objects["buf"].Reasons, netsec.ReasonEscape) {
		t.Fatalf("missing escape reason: %v", r.Objects["buf"].Reasons)
	}
}

func TestNoCaptureButOutOfBoundsSummary(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("buf", 16, 8)},
		Values: []netsec.Value{
			addr("p", "buf"),
			{
				ID:     "call",
				Kind:   netsec.OpPassCall,
				Callee: "reader",
				Args:   []netsec.ValueID{"p"},
				Summary: &netsec.CallSummary{Params: []netsec.ParamSummary{{
					Index:     0,
					NoCapture: true,
					ReadOnly:  true,
					MaxOffset: 16,
				}}},
			},
		},
	}
	r := netsec.Analyze(g)
	if got := class(t, r, "buf"); got != netsec.ClassUnsafe {
		t.Fatalf("buf class = %s, want unsafe", got)
	}
	if !hasReason(r.Objects["buf"].Reasons, netsec.ReasonOutOfBounds) {
		t.Fatalf("missing out-of-bounds reason: %v", r.Objects["buf"].Reasons)
	}
}

func TestCallSummaryFromGraphTable(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("buf", 16, 8)},
		Values: []netsec.Value{
			addr("p", "buf"),
			{ID: "call", Kind: netsec.OpPassCall, Callee: "reader", Args: []netsec.ValueID{"p"}},
		},
		Summaries: map[netsec.FunctionID]netsec.CallSummary{
			"reader": {Params: []netsec.ParamSummary{{
				Index: 0, NoCapture: true, ReadOnly: true, MaxOffset: 7,
			}}},
		},
	}
	r := netsec.Analyze(g)
	if got := class(t, r, "buf"); got != netsec.ClassSafe {
		t.Fatalf("buf class = %s, want safe; reasons=%v", got, r.Objects["buf"].Reasons)
	}
}

func TestPointerStoreThenReload(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("container", 8, 8), local("target", 8, 8)},
		Values: []netsec.Value{
			addr("c", "container"),
			addr("b", "target"),
			{ID: "store", Kind: netsec.OpStorePtr, Dest: "c", Src: "b"},
			{ID: "load", Kind: netsec.OpLoadPtr, Ptr: "c"},
			readOnly("read", "reader", "load"),
		},
	}
	r := netsec.Analyze(g)
	for _, id := range []string{"container", "target"} {
		if got := class(t, r, id); got != netsec.ClassSafe {
			t.Fatalf("%s class = %s, want safe; reasons=%v", id, got, r.Objects[netsec.ObjectID(id)].Reasons)
		}
	}
}

func TestGraphPointerSizeControlsDefaultWidth(t *testing.T) {
	g := netsec.Graph{
		PointerSize: 4,
		Objects:     []netsec.Object{local("container", 4, 4), local("target", 4, 4)},
		Values: []netsec.Value{
			addr("c", "container"),
			addr("b", "target"),
			{ID: "store", Kind: netsec.OpStorePtr, Dest: "c", Src: "b"},
			{ID: "load", Kind: netsec.OpLoadPtr, Ptr: "c"},
			readOnly("read", "reader", "load"),
		},
		Summaries: map[netsec.FunctionID]netsec.CallSummary{
			"reader": {Params: []netsec.ParamSummary{{
				Index: 0, NoCapture: true, ReadOnly: true, MaxOffset: 3,
			}}},
		},
	}
	g.Values[4] = netsec.Value{
		ID:     "read",
		Kind:   netsec.OpPassCall,
		Callee: "reader",
		Args:   []netsec.ValueID{"load"},
	}
	r := netsec.Analyze(g)
	for _, id := range []string{"container", "target"} {
		if got := class(t, r, id); got != netsec.ClassSafe {
			t.Fatalf("%s class = %s, want safe; reasons=%v", id, got, r.Objects[netsec.ObjectID(id)].Reasons)
		}
	}
}

func TestReloadBeforeStoreInValueOrder(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("container", 8, 8), local("target", 8, 8)},
		Values: []netsec.Value{
			{ID: "load", Kind: netsec.OpLoadPtr, Ptr: "c"},
			readOnly("read", "reader", "load"),
			addr("c", "container"),
			addr("b", "target"),
			{ID: "store", Kind: netsec.OpStorePtr, Dest: "c", Src: "b"},
		},
	}
	r := netsec.Analyze(g)
	for _, id := range []string{"container", "target"} {
		if got := class(t, r, id); got != netsec.ClassSafe {
			t.Fatalf("%s class = %s, want safe; reasons=%v", id, got, r.Objects[netsec.ObjectID(id)].Reasons)
		}
	}
}

func TestPointerInEscapedContainerEscapes(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("container", 8, 8), local("target", 8, 8)},
		Values: []netsec.Value{
			addr("c", "container"),
			addr("b", "target"),
			{ID: "store", Kind: netsec.OpStorePtr, Dest: "c", Src: "b"},
			{ID: "ret", Kind: netsec.OpReturnPtr, Ptr: "c"},
		},
	}
	r := netsec.Analyze(g)
	for _, id := range []string{"container", "target"} {
		if got := class(t, r, id); got != netsec.ClassUnsafe {
			t.Fatalf("%s class = %s, want unsafe; reasons=%v", id, got, r.Objects[netsec.ObjectID(id)].Reasons)
		}
	}
}

func TestIntegerRoundTrip(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("buf", 8, 8)},
		Values: []netsec.Value{
			addr("p", "buf"),
			{ID: "toint", Kind: netsec.OpPtrToInt, Ptr: "p"},
			{ID: "toptr", Kind: netsec.OpIntToPtr, Src: "toint"},
			readOnly("read", "reader", "toptr"),
		},
	}
	r := netsec.Analyze(g)
	if got := class(t, r, "buf"); got != netsec.ClassUnsafe {
		t.Fatalf("buf class = %s, want unsafe", got)
	}
	if !hasReason(r.Objects["buf"].Reasons, netsec.ReasonPointerInteger) {
		t.Fatalf("missing pointer-integer reason: %v", r.Objects["buf"].Reasons)
	}
}

func TestVariableLayoutShare(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{
			{ID: "fixed", Size: 8, Align: 8, Purpose: netsec.PurposeLocal, LayoutGroup: "g"},
			{ID: "variadic", Size: 16, Align: 8, Purpose: netsec.PurposeVariableLength, LayoutGroup: "g"},
		},
	}
	r := netsec.Analyze(g)
	for _, id := range []string{"fixed", "variadic"} {
		if got := class(t, r, id); got != netsec.ClassUnsafe {
			t.Fatalf("%s class = %s, want unsafe", id, got)
		}
	}
}

func TestUnknownCallCapturesAndAccessesArbitrarily(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("buf", 8, 8)},
		Values: []netsec.Value{
			addr("p", "buf"),
			{ID: "call", Kind: netsec.OpPassCall, Callee: "unknown", Args: []netsec.ValueID{"p"}},
		},
	}
	r := netsec.Analyze(g)
	if got := class(t, r, "buf"); got != netsec.ClassUnsafe {
		t.Fatalf("buf class = %s, want unsafe", got)
	}
	if !hasReason(r.Objects["buf"].Reasons, netsec.ReasonEscape) ||
		!hasReason(r.Objects["buf"].Reasons, netsec.ReasonOutOfBounds) {
		t.Fatalf("missing conservative call reasons: %v", r.Objects["buf"].Reasons)
	}
}

func TestFunctionDeclarationWithoutBodyStaysConservative(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("buf", 8, 8)},
		Values: []netsec.Value{
			addr("p", "buf"),
			{ID: "call", Kind: netsec.OpPassCall, Callee: "external", Args: []netsec.ValueID{"p"}},
		},
		Functions: []netsec.Function{{ID: "external", Params: []netsec.ValueID{"p0"}}},
	}
	r := netsec.Analyze(g)
	if got := class(t, r, "buf"); got != netsec.ClassUnsafe {
		t.Fatalf("buf class = %s, want unsafe", got)
	}
}

func TestFieldSensitivePointerReload(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{
			local("container", 16, 8),
			local("field0", 8, 8),
			local("field8", 8, 8),
		},
		Values: []netsec.Value{
			addr("c", "container"),
			{ID: "c8", Kind: netsec.OpGEP, Base: "c", Offset: 8},
			addr("b0", "field0"),
			addr("b8", "field8"),
			{ID: "store0", Kind: netsec.OpStorePtr, Dest: "c", Src: "b0"},
			{ID: "store8", Kind: netsec.OpStorePtr, Dest: "c8", Src: "b8"},
			{ID: "load0", Kind: netsec.OpLoadPtr, Ptr: "c"},
			{ID: "ret", Kind: netsec.OpReturnPtr, Ptr: "load0"},
		},
	}
	r := netsec.Analyze(g)
	if got := class(t, r, "field0"); got != netsec.ClassUnsafe {
		t.Fatalf("field0 class = %s, want unsafe", got)
	}
	if got := class(t, r, "field8"); got != netsec.ClassSafe {
		t.Fatalf("field8 class = %s, want safe; reasons=%v", got, r.Objects["field8"].Reasons)
	}
}

func TestBudgetExhaustionIsUnsafe(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("buf", 8, 8)},
		Values: []netsec.Value{
			{ID: "phi", Kind: netsec.OpPhi, Inputs: []netsec.ValueID{"p"}},
			addr("p", "buf"),
		},
	}
	r := netsec.Analyze(g, netsec.WithBudget(1, 100))
	if !r.BudgetExhausted {
		t.Fatalf("expected budget exhaustion")
	}
	if got := class(t, r, "buf"); got != netsec.ClassUnsafe {
		t.Fatalf("buf class = %s, want unsafe", got)
	}
	if !hasReason(r.Objects["buf"].Reasons, netsec.ReasonBudgetExhausted) {
		t.Fatalf("missing budget reason: %v", r.Objects["buf"].Reasons)
	}
}

func TestControlMetadataIsUnmanaged(t *testing.T) {
	g := netsec.Graph{Objects: []netsec.Object{
		{ID: "ra", Size: 8, Align: 8, Purpose: netsec.PurposeReturnAddress},
		local("buf", 8, 8),
	}}
	r := netsec.Analyze(g)
	if got := class(t, r, "ra"); got != netsec.ClassUnmanaged {
		t.Fatalf("ra class = %s, want unmanaged", got)
	}
	if got := class(t, r, "buf"); got != netsec.ClassSafe {
		t.Fatalf("buf class = %s, want safe", got)
	}
}

func TestRecursiveSummaryFixedPoint(t *testing.T) {
	g := netsec.Graph{
		Values: []netsec.Value{
			{ID: "p", Kind: netsec.OpParam, Param: 0, Function: "walk"},
			{ID: "recurse", Kind: netsec.OpPassCall, Callee: "walk", Args: []netsec.ValueID{"p"}, Function: "walk"},
		},
		Functions: []netsec.Function{{
			ID:     "walk",
			Params: []netsec.ValueID{"p"},
			Values: []netsec.ValueID{"p", "recurse"},
		}},
	}
	s := netsec.Summarize(g)
	sum, ok := s["walk"]
	if !ok || len(sum.Params) != 1 {
		t.Fatalf("missing recursive summary: %#v", s)
	}
	if !sum.Params[0].NoCapture || !sum.Params[0].ReadOnly || sum.Params[0].MaxOffset != 0 {
		t.Fatalf("recursive summary did not reach optimistic fixed point: %#v", sum)
	}
}

func TestInferredRecursiveCapture(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("buf", 8, 8)},
		Values: []netsec.Value{
			addr("p", "buf"),
			{ID: "p0", Kind: netsec.OpParam, Param: 0, Function: "leak"},
			{ID: "ret", Kind: netsec.OpReturnPtr, Ptr: "p0", Function: "leak"},
			{ID: "call", Kind: netsec.OpPassCall, Callee: "leak", Args: []netsec.ValueID{"p"}},
		},
		Functions: []netsec.Function{{
			ID:     "leak",
			Params: []netsec.ValueID{"p0"},
			Values: []netsec.ValueID{"p0", "ret"},
		}},
	}
	r := netsec.Analyze(g)
	if got := class(t, r, "buf"); got != netsec.ClassUnsafe {
		t.Fatalf("buf class = %s, want unsafe", got)
	}
}

func TestSummaryInferenceFromFunctionTags(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("buf", 8, 8)},
		Values: []netsec.Value{
			{ID: "taggedRet", Kind: netsec.OpParam, Param: 0, Function: "taggedLeak"},
			{ID: "taggedReturn", Kind: netsec.OpReturnPtr, Ptr: "taggedRet", Function: "taggedLeak"},
			addr("arg", "buf"),
			{ID: "call", Kind: netsec.OpPassCall, Callee: "taggedLeak", Args: []netsec.ValueID{"arg"}},
		},
		Functions: []netsec.Function{{ID: "taggedLeak"}},
	}
	summary := netsec.Summarize(g)["taggedLeak"]
	if len(summary.Params) != 1 || summary.Params[0].NoCapture {
		t.Fatalf("tagged function summary = %#v", summary)
	}
	r := netsec.Analyze(g)
	if got := class(t, r, "buf"); got != netsec.ClassUnsafe {
		t.Fatalf("buf class = %s, want unsafe", got)
	}
}

func TestInferredCaptureThroughEscapedLocalMemory(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("callerBuf", 8, 8), local("calleeBox", 8, 8)},
		Values: []netsec.Value{
			addr("arg", "callerBuf"),
			{ID: "p0", Kind: netsec.OpParam, Param: 0, Function: "leakThroughBox"},
			{ID: "box", Kind: netsec.OpAddrOf, Object: "calleeBox", Function: "leakThroughBox"},
			{ID: "store", Kind: netsec.OpStorePtr, Dest: "box", Src: "p0", Function: "leakThroughBox"},
			{ID: "retBox", Kind: netsec.OpReturnPtr, Ptr: "box", Function: "leakThroughBox"},
			{ID: "call", Kind: netsec.OpPassCall, Callee: "leakThroughBox", Args: []netsec.ValueID{"arg"}},
		},
		Functions: []netsec.Function{{
			ID:     "leakThroughBox",
			Params: []netsec.ValueID{"p0"},
			Values: []netsec.ValueID{"p0", "box", "store", "retBox"},
		}},
	}
	summary := netsec.Summarize(g)["leakThroughBox"]
	if len(summary.Params) != 1 || summary.Params[0].NoCapture {
		t.Fatalf("expected capture through escaped local memory, got %#v", summary)
	}
	r := netsec.Analyze(g)
	if got := class(t, r, "callerBuf"); got != netsec.ClassUnsafe {
		t.Fatalf("callerBuf class = %s, want unsafe", got)
	}
}

func TestLayoutSimulatorOnlyReportsSeriousImpacts(t *testing.T) {
	layout := netsec.Layout{Slots: []netsec.LayoutSlot{
		{Object: "source", Offset: 16, Size: 16, Kind: netsec.SlotUnsafe},
		{Object: "neighbor", Offset: 32, Size: 16, Kind: netsec.SlotSafe},
		{ID: "ra", Offset: 48, Size: 8, Kind: netsec.SlotControl},
	}}
	classes := map[netsec.ObjectID]netsec.StackClass{
		"source":   netsec.ClassUnsafe,
		"neighbor": netsec.ClassSafe,
	}
	inside := netsec.SimulateLayout(layout, classes, []netsec.UnsafeWrite{{
		Object: "source", Offset: 0, Size: 8,
	}})
	if len(inside) != 0 {
		t.Fatalf("inside write reported as impact: %#v", inside)
	}
	neighbor := netsec.SimulateLayout(layout, classes, []netsec.UnsafeWrite{{
		Object: "source", Offset: 16, Size: 8,
	}})
	if len(neighbor) != 1 || len(neighbor[0].SafeObjects) != 1 || neighbor[0].SafeObjects[0] != "neighbor" {
		t.Fatalf("neighbor impact missing: %#v", neighbor)
	}
	control := netsec.SimulateLayout(layout, classes, []netsec.UnsafeWrite{{
		Object: "source", Offset: 32, Size: 8,
	}})
	if len(control) != 1 || !control[0].Control {
		t.Fatalf("control impact missing: %#v", control)
	}
}

func TestAuditFindsShortestPointerStorePath(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("container", 8, 8), local("target", 8, 8)},
		Values: []netsec.Value{
			addr("c", "container"),
			addr("b", "target"),
			{ID: "store", Kind: netsec.OpStorePtr, Dest: "c", Src: "b"},
			{ID: "load", Kind: netsec.OpLoadPtr, Ptr: "c"},
			{ID: "ret", Kind: netsec.OpReturnPtr, Ptr: "load"},
		},
	}
	audit := netsec.Audit(g, netsec.PendingClassifier(func(id netsec.ObjectID, _ netsec.Object) netsec.StackClass {
		return netsec.ClassSafe
	}))
	if len(audit.FalseSafe) != 1 {
		t.Fatalf("false-safe findings = %#v", audit.FalseSafe)
	}
	m := audit.FalseSafe[0]
	if m.Object != "target" {
		t.Fatalf("false-safe object = %s, want target", m.Object)
	}
	if len(m.Path) == 0 || m.Distance == 0 {
		t.Fatalf("missing provenance path: %#v", m)
	}
	var sawStore, sawLoad bool
	for _, step := range m.Path {
		if step.Kind == netsec.NodeOperation && step.Operation == netsec.OpStorePtr {
			sawStore = true
		}
		if step.Operation == netsec.OpLoadPtr {
			sawLoad = true
		}
	}
	if !sawStore || !sawLoad {
		t.Fatalf("path does not traverse store/load: %#v", m.Path)
	}
}

func TestAuditIntegratesLayoutImpact(t *testing.T) {
	g := netsec.Graph{
		Objects: []netsec.Object{local("buf", 16, 8)},
		Values: []netsec.Value{
			addr("p", "buf"),
			{
				ID:     "call",
				Kind:   netsec.OpPassCall,
				Callee: "clobber",
				Args:   []netsec.ValueID{"p"},
				Summary: &netsec.CallSummary{Params: []netsec.ParamSummary{{
					Index:     0,
					NoCapture: true,
					ReadOnly:  true,
					MaxOffset: 16,
				}}},
			},
		},
	}
	layout := netsec.Layout{Slots: []netsec.LayoutSlot{
		{Object: "buf", Offset: 0, Size: 16, Kind: netsec.SlotUnsafe},
		{ID: "shadow", Offset: 16, Size: 8, Kind: netsec.SlotControl},
	}}
	audit := netsec.Audit(g, func(id netsec.ObjectID) bool { return true }, netsec.WithLayout(layout))
	if len(audit.FalseSafe) != 1 {
		t.Fatalf("false-safe = %#v", audit.FalseSafe)
	}
	if len(audit.Impacts) != 1 || !audit.Impacts[0].Control {
		t.Fatalf("control impact missing: %#v", audit.Impacts)
	}
}

func hasReason(reasons []netsec.Reason, code netsec.ReasonCode) bool {
	for _, r := range reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}
