package netsec

import (
	"strings"
	"testing"
)

func testFrame(b *Builder, id FrameID, tenant, secret string) {
	b.FrameTemplate(id, tenant, FrameSlot{Tenant: tenant, Data: secret})
}

func mustAnalyze(t *testing.T, p *Program, opts ...Options) *Report {
	t.Helper()
	r, err := Analyze(p, opts...)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(r.Errors) > 0 {
		t.Fatalf("analysis errors: %v", r.Errors)
	}
	return r
}

func passAsyncUnpromoted() *Program {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0, Mutable: true}}, WithID("closure"))
	b.PassAsync(mk.Result, WithID("async"))
	b.Return("", "tenant-A", WithID("return"))
	return b.Program
}

func TestPassAsyncUnpromotedLeaksOtherTenant(t *testing.T) {
	p := passAsyncUnpromoted()
	r := mustAnalyze(t, p)
	if r.Status != StatusVulnerable {
		t.Fatalf("status = %s, want vulnerable; findings=%+v unknown=%+v", r.Status, r.Findings, r.Unknowns)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(r.Findings), r.Findings)
	}
	f := r.Findings[0]
	if f.Sink != EscapeAsync || f.EscapeAt != "async" {
		t.Fatalf("sink = %s at %s", f.Sink, f.EscapeAt)
	}
	if got, want := joinPath(f.AllocPath), "alloc>capture>closure>async"; got != want {
		t.Fatalf("alloc path = %s, want %s", got, want)
	}
	if len(f.MissingPromotionPaths) == 0 {
		t.Fatalf("missing promotion paths were not reported")
	}
	if got, want := joinPath(f.MissingPromotionPaths[0]), "capture>closure>async"; got != want {
		t.Fatalf("missing path = %s, want %s", got, want)
	}

	lc, err := VerifyFinding(p, f, ExecuteOptions{AttackerTenant: "tenant-B", AttackerData: "B-SECRET"})
	if err != nil {
		t.Fatalf("VerifyFinding: %v", err)
	}
	if !lc.Leaked {
		t.Fatalf("finding did not reproduce a leak: %+v", lc)
	}
	if lc.ObservedTenant != "tenant-B" || lc.ObservedData != "B-SECRET" {
		t.Fatalf("observed %s/%q, want tenant-B/B-SECRET", lc.ObservedTenant, lc.ObservedData)
	}
}

func TestHeapPromoteMakesPassAsyncSafe(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	prom := b.HeapPromote(ref.Result, WithID("promote"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0, Mutable: true}}, WithID("closure"))
	b.PassAsync(mk.Result, WithID("async"))
	b.Return("", "tenant-A", WithID("return"))
	r := mustAnalyze(t, b.Program)
	if r.Status != StatusSafe {
		t.Fatalf("status = %s, want safe; findings=%+v", r.Status, r.Findings)
	}
	if prom.Cell != "promote" {
		t.Fatalf("promoted cell = %q, want promote", prom.Cell)
	}

	ex, err := ExecutePath(b.Program, []OpID{"alloc", "capture", "promote", "closure", "async", "return"})
	if err != nil {
		t.Fatalf("ExecutePath: %v", err)
	}
	if ex.Leaked {
		t.Fatalf("promoted closure leaked: %+v", ex.Reads)
	}
	if len(ex.Reads) == 0 {
		t.Fatalf("expected the async closure to read its promoted cell")
	}
	for _, read := range ex.Reads {
		if read.Cell != prom.Cell {
			t.Fatalf("read cell = %q, want %q", read.Cell, prom.Cell)
		}
		if read.Tenant != "tenant-A" {
			t.Fatalf("read tenant = %q, want tenant-A", read.Tenant)
		}
	}
}

func branchOneSidedPromotionProgram() *Program {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0, Mutable: true}}, WithID("closure"))
	b.Op(&Op{Kind: OpBranch}, WithID("split"), WithSuccs("promote", "skip"))
	b.Break()
	b.Op(&Op{Kind: OpHeapPromote, Inputs: []ValueID{ref.Result}, ResultKind: HeapCellKind}, WithID("promote"))
	b.Break()
	b.Op(&Op{Kind: OpBranch}, WithID("skip"))
	b.Break()
	b.Op(&Op{Kind: OpBranch}, WithID("join"))
	b.Program.Link("promote", "join")
	b.Program.Link("skip", "join")
	b.Program.Link("join", "async")
	b.Break()
	b.Op(&Op{Kind: OpPassAsync, Inputs: []ValueID{mk.Result}}, WithID("async"))
	b.Program.Link("async", "return")
	b.Op(&Op{Kind: OpReturn, Frame: "tenant-A"}, WithID("return"))
	return b.Program
}

func TestBranchWithOnlyOnePromotedSideIsVulnerable(t *testing.T) {
	p := branchOneSidedPromotionProgram()
	r := mustAnalyze(t, p)
	if r.Status != StatusVulnerable {
		t.Fatalf("status = %s, want vulnerable\nprogram:\n%s\nreport: %+v", r.Status, p, r)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(r.Findings), r.Findings)
	}
	f := r.Findings[0]
	if len(f.MissingPromotionPaths) == 0 {
		t.Fatalf("no missing path")
	}
	missing := joinPath(f.MissingPromotionPaths[0])
	if !strings.Contains(missing, "skip") || strings.Contains(missing, "promote") {
		t.Fatalf("missing path should use skip and not promote: %s", missing)
	}
	if !containsOp(f.LeakPath, "skip") {
		t.Fatalf("leak path does not use the unpromoted branch: %v", f.LeakPath)
	}
	lc, err := VerifyFinding(p, f, ExecuteOptions{AttackerTenant: "tenant-B", AttackerData: "B-SECRET"})
	if err != nil {
		t.Fatalf("VerifyFinding: %v", err)
	}
	if !lc.Leaked {
		t.Fatalf("expected leak on unpromoted branch, got %+v", lc)
	}
}

func TestBranchWithBothSidesPromotedIsSafe(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0}}, WithID("closure"))
	b.Op(&Op{Kind: OpBranch}, WithID("split"), WithSuccs("promoteA", "promoteB"))
	b.Break()
	b.Op(&Op{Kind: OpHeapPromote, Inputs: []ValueID{ref.Result}, ResultKind: HeapCellKind}, WithID("promoteA"))
	b.Break()
	b.Op(&Op{Kind: OpHeapPromote, Inputs: []ValueID{ref.Result}, ResultKind: HeapCellKind}, WithID("promoteB"))
	b.Break()
	b.Op(&Op{Kind: OpBranch}, WithID("join"))
	b.Program.Link("promoteA", "join")
	b.Program.Link("promoteB", "join")
	b.Program.Link("join", "async")
	b.Break()
	b.Op(&Op{Kind: OpPassAsync, Inputs: []ValueID{mk.Result}}, WithID("async"))
	b.Program.Link("async", "return")
	b.Op(&Op{Kind: OpReturn, Frame: "tenant-A"}, WithID("return"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusSafe {
		t.Fatalf("status = %s, want safe; findings=%+v", r.Status, r.Findings)
	}
	for _, cell := range []OpID{"promoteA", "promoteB"} {
		path := []OpID{"alloc", "capture", "closure", "split", cell, "join", "async", "return"}
		ex, err := ExecutePath(b.Program, path)
		if err != nil {
			t.Fatalf("ExecutePath(%s): %v", cell, err)
		}
		if ex.Leaked {
			t.Fatalf("path through %s leaked: %+v", cell, ex.Reads)
		}
	}
}

func TestNestedClosureCaptureIsFollowed(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	inner := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0, Mutable: true}}, WithID("inner"))
	outer := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: inner.Result}}, WithID("outer"))
	b.PassAsync(outer.Result, WithID("async"))
	b.Return("", "tenant-A", WithID("return"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusVulnerable || len(r.Findings) != 1 {
		t.Fatalf("status=%s findings=%+v", r.Status, r.Findings)
	}
	f := r.Findings[0]
	if f.Closure != ClosureID(outer.Result) {
		t.Fatalf("outer closure = %s, want %s", f.Closure, outer.Result)
	}
	if f.Capture.Closure != ClosureID(inner.Result) {
		t.Fatalf("inner closure = %s, want %s", f.Capture.Closure, inner.Result)
	}
	lc, err := VerifyFinding(b.Program, f, ExecuteOptions{AttackerTenant: "tenant-B", AttackerData: "B-SECRET"})
	if err != nil {
		t.Fatalf("VerifyFinding: %v", err)
	}
	if !lc.Leaked {
		t.Fatalf("nested closure did not leak: %+v", lc)
	}
}

func TestCaptureByValueIsSafe(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	s := b.Scalar("tenant-A", "A-SECRET", WithID("secret"))
	copied := b.CaptureByValue(s.Result, WithID("copy"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByValue, Source: copied.Result}}, WithID("closure"))
	b.PassAsync(mk.Result, WithID("async"))
	b.Return("", "tenant-A", WithID("return"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusSafe {
		t.Fatalf("status = %s, want safe; findings=%+v", r.Status, r.Findings)
	}
	ex, err := ExecutePath(b.Program, []OpID{"alloc", "secret", "copy", "closure", "async", "return"})
	if err != nil {
		t.Fatalf("ExecutePath: %v", err)
	}
	if ex.Leaked {
		t.Fatalf("by-value capture leaked: %+v", ex.Reads)
	}
}

func TestCaptureByValueFromFrameRefIsSafe(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("ref"))
	copied := b.CaptureByValue(ref.Result, WithID("copy"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByValue, Source: copied.Result}}, WithID("closure"))
	b.PassAsync(mk.Result, WithID("async"))
	b.Return("", "tenant-A", WithID("return"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusSafe {
		t.Fatalf("status = %s, want safe; findings=%+v", r.Status, r.Findings)
	}
	ex, err := ExecutePath(b.Program, []OpID{"alloc", "ref", "copy", "closure", "async", "return"})
	if err != nil {
		t.Fatalf("ExecutePath: %v", err)
	}
	if ex.Leaked {
		t.Fatalf("by-value copy of a stack scalar leaked: %+v", ex.Reads)
	}
}

func TestPromotedClosuresShareOneHeapCell(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	prom := b.HeapPromote(ref.Result, WithID("promote"))
	c1 := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0, Mutable: true}}, WithID("closure1"))
	c2 := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0, Mutable: true}}, WithID("closure2"))
	b.PassAsync(c1.Result, WithID("async1"))
	b.PassAsync(c2.Result, WithID("async2"))
	b.Return("", "tenant-A", WithID("return"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusSafe {
		t.Fatalf("status = %s, want safe; findings=%+v", r.Status, r.Findings)
	}
	ex, err := ExecutePath(b.Program, []OpID{"alloc", "capture", "promote", "closure1", "closure2", "async1", "async2", "return"})
	if err != nil {
		t.Fatalf("ExecutePath: %v", err)
	}
	if ex.Leaked {
		t.Fatalf("promoted shared cell leaked: %+v", ex.Reads)
	}
	if len(ex.Reads) != 2 {
		t.Fatalf("reads = %d, want 2: %+v", len(ex.Reads), ex.Reads)
	}
	for _, read := range ex.Reads {
		if read.Cell != prom.Cell {
			t.Fatalf("read cell = %q, want shared %q", read.Cell, prom.Cell)
		}
		if read.Tenant != "tenant-A" {
			t.Fatalf("read tenant = %q, want tenant-A", read.Tenant)
		}
	}
}

func TestPromotionAfterClosureCreationRewritesCapture(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0}}, WithID("closure"))
	b.HeapPromote(ref.Result, WithID("promoteAfter"))
	b.PassAsync(mk.Result, WithID("async"))
	b.Return("", "tenant-A", WithID("return"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusSafe {
		t.Fatalf("status = %s, want safe; findings=%+v", r.Status, r.Findings)
	}
	ex, err := ExecutePath(b.Program, []OpID{"alloc", "capture", "closure", "promoteAfter", "async", "return"})
	if err != nil {
		t.Fatalf("ExecutePath: %v", err)
	}
	if ex.Leaked {
		t.Fatalf("late promotion did not rewrite closure capture: %+v", ex.Reads)
	}
}

func TestHeapPromoteRewritesAllFrameRefAliases(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref1 := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture1"))
	ref2 := b.CaptureByRefSlot("tenant-A", 0, false, WithID("capture2"))
	b.HeapPromote(ref1.Result, WithID("promote"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref2.Result, Frame: "tenant-A", Slot: 0}}, WithID("closure"))
	b.PassAsync(mk.Result, WithID("async"))
	b.Return("", "tenant-A", WithID("return"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusSafe {
		t.Fatalf("status = %s, want safe (alias %s should have been rewritten); findings=%+v", r.Status, ref2.Result, r.Findings)
	}
	ex, err := ExecutePath(b.Program, []OpID{"alloc", "capture1", "capture2", "promote", "closure", "async", "return"})
	if err != nil {
		t.Fatalf("ExecutePath: %v", err)
	}
	if ex.Leaked {
		t.Fatalf("alias read leaked: %+v", ex.Reads)
	}
}

func TestNewCaptureAfterPromotionUsesHeapCell(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	prom := b.HeapPromote(ref.Result, WithID("promote"))
	lateRef := b.CaptureByRefSlot("tenant-A", 0, false, WithID("lateCapture"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: lateRef.Result, Frame: "tenant-A", Slot: 0}}, WithID("closure"))
	b.PassAsync(mk.Result, WithID("async"))
	b.Return("", "tenant-A", WithID("return"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusSafe {
		t.Fatalf("status = %s, want safe; findings=%+v", r.Status, r.Findings)
	}
	ex, err := ExecutePath(b.Program, []OpID{"alloc", "capture", "promote", "lateCapture", "closure", "async", "return"})
	if err != nil {
		t.Fatalf("ExecutePath: %v", err)
	}
	if ex.Leaked {
		t.Fatalf("capture created after promotion leaked: %+v", ex.Reads)
	}
	if len(ex.Reads) == 0 || ex.Reads[0].Cell != prom.Cell {
		t.Fatalf("late capture reads = %+v, want cell %q", ex.Reads, prom.Cell)
	}
}

func TestMakeClosureAfterReturnOfExistingRefIsReported(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	b.Return("", "tenant-A", WithID("return"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0}}, WithID("closure"))
	b.PassAsync(mk.Result, WithID("async"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusVulnerable || len(r.Findings) != 1 {
		t.Fatalf("status=%s findings=%+v", r.Status, r.Findings)
	}
	if r.Findings[0].Capture.Reason != ReturnedFrame {
		t.Fatalf("reason = %s, want %s", r.Findings[0].Capture.Reason, ReturnedFrame)
	}
}

func TestPromotionDoesNotSanitizeCaptureAfterReturn(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	b.Return("", "tenant-A", WithID("return"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	b.HeapPromote(ref.Result, WithID("promote"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0}}, WithID("closure"))
	b.PassAsync(mk.Result, WithID("async"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusVulnerable || len(r.Findings) != 1 {
		t.Fatalf("status=%s findings=%+v", r.Status, r.Findings)
	}
	if r.Findings[0].Capture.Reason != ReturnedFrame {
		t.Fatalf("reason = %s, want %s (late promotion must not sanitize a dead capture)", r.Findings[0].Capture.Reason, ReturnedFrame)
	}
	lc, err := VerifyFinding(b.Program, r.Findings[0], ExecuteOptions{AttackerTenant: "tenant-B", AttackerData: "B-SECRET"})
	if err != nil {
		t.Fatalf("VerifyFinding: %v", err)
	}
	if !lc.Leaked {
		t.Fatalf("late-promoted dead capture did not leak: %+v", lc)
	}
}

func TestLatePromotionAfterReturnDoesNotSanitizeCapture(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	b.Return("", "tenant-A", WithID("return"))
	b.HeapPromote(ref.Result, WithID("promote"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0}}, WithID("closure"))
	b.PassAsync(mk.Result, WithID("async"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusVulnerable || len(r.Findings) != 1 {
		t.Fatalf("status=%s findings=%+v", r.Status, r.Findings)
	}
	if r.Findings[0].Capture.Reason != ReturnedFrame {
		t.Fatalf("reason = %s, want %s", r.Findings[0].Capture.Reason, ReturnedFrame)
	}
	lc, err := VerifyFinding(b.Program, r.Findings[0], ExecuteOptions{AttackerTenant: "tenant-B", AttackerData: "B-SECRET"})
	if err != nil {
		t.Fatalf("VerifyFinding: %v", err)
	}
	if !lc.Leaked {
		t.Fatalf("promotion after return copied attacker bytes but no leak was observed: %+v", lc)
	}
}

func TestCallNowIsSynchronousAndSafe(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0, Mutable: true}}, WithID("closure"))
	b.CallNow(mk.Result, WithID("call"))
	b.Return("", "tenant-A", WithID("return"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusSafe {
		t.Fatalf("status = %s, want safe; findings=%+v", r.Status, r.Findings)
	}
	ex, err := ExecutePath(b.Program, []OpID{"alloc", "capture", "closure", "call", "return"})
	if err != nil {
		t.Fatalf("ExecutePath: %v", err)
	}
	if ex.Leaked {
		t.Fatalf("CallNow leaked: %+v", ex.Reads)
	}
}

func TestCaptureAfterReturnIsReported(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	b.Return("", "tenant-A", WithID("return"))
	ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0, Mutable: true}}, WithID("closure"))
	b.PassAsync(mk.Result, WithID("async"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusVulnerable || len(r.Findings) != 1 {
		t.Fatalf("status=%s findings=%+v", r.Status, r.Findings)
	}
	if r.Findings[0].Capture.Reason != ReturnedFrame {
		t.Fatalf("reason = %s, want %s", r.Findings[0].Capture.Reason, ReturnedFrame)
	}
	lc, err := VerifyFinding(b.Program, r.Findings[0], ExecuteOptions{AttackerTenant: "tenant-B", AttackerData: "B-SECRET"})
	if err != nil {
		t.Fatalf("VerifyFinding: %v", err)
	}
	if !lc.Leaked {
		t.Fatalf("capture after return did not leak: %+v", lc)
	}
}

func TestStoreGlobalReturnAndHeapStoreAreSinks(t *testing.T) {
	t.Run("global", func(t *testing.T) {
		b := NewBuilder()
		testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
		b.FrameAlloc("tenant-A", 1, WithID("alloc"))
		ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
		mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0}}, WithID("closure"))
		b.StoreGlobal("plugin.registry", mk.Result, WithID("store"))
		b.Return("", "tenant-A", WithID("return"))
		r := mustAnalyze(t, b.Program)
		if r.Status != StatusVulnerable || len(r.Findings) != 1 || r.Findings[0].Sink != EscapeGlobal {
			t.Fatalf("status=%s findings=%+v", r.Status, r.Findings)
		}
	})

	t.Run("return", func(t *testing.T) {
		b := NewBuilder()
		testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
		b.FrameAlloc("tenant-A", 1, WithID("alloc"))
		ref := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
		mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: ref.Result, Frame: "tenant-A", Slot: 0}}, WithID("closure"))
		b.Return(mk.Result, "tenant-A", WithID("return"))
		r := mustAnalyze(t, b.Program)
		if r.Status != StatusVulnerable || len(r.Findings) != 1 || r.Findings[0].Sink != EscapeReturn {
			t.Fatalf("status=%s findings=%+v", r.Status, r.Findings)
		}
	})

	t.Run("heap", func(t *testing.T) {
		b := NewBuilder()
		b.FrameTemplate("tenant-A", "tenant-A",
			FrameSlot{Tenant: "tenant-A", Data: "A-SECRET"},
			FrameSlot{Tenant: "tenant-A", Data: "drop"})
		b.FrameAlloc("tenant-A", 2, WithID("alloc"))
		secret := b.CaptureByRefSlot("tenant-A", 0, true, WithID("capture"))
		drop := b.CaptureByRefSlot("tenant-A", 1, false, WithID("captureDrop"))
		cell := b.HeapPromote(drop.Result, WithID("promoteDrop"))
		mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: secret.Result, Frame: "tenant-A", Slot: 0}}, WithID("closure"))
		b.StoreHeap(cell.Result, mk.Result, WithID("store"))
		b.Return("", "tenant-A", WithID("return"))
		r := mustAnalyze(t, b.Program)
		if r.Status != StatusVulnerable || len(r.Findings) != 1 || r.Findings[0].Sink != EscapeHeap {
			t.Fatalf("status=%s findings=%+v", r.Status, r.Findings)
		}
	})
}

func TestMergePreservesCaptureOrigins(t *testing.T) {
	b := NewBuilder()
	testFrame(b, "tenant-A", "tenant-A", "A-SECRET")
	b.FrameAlloc("tenant-A", 1, WithID("alloc"))
	b.Op(&Op{Kind: OpBranch}, WithID("split"), WithSuccs("captureA", "captureB"))
	b.Break()
	a := b.Op(&Op{Kind: OpCaptureByRef, Frame: "tenant-A", Slot: 0, ResultKind: FrameRefKind}, WithID("captureA"))
	b.Break()
	c := b.Op(&Op{Kind: OpCaptureByRef, Frame: "tenant-A", Slot: 0, ResultKind: FrameRefKind}, WithID("captureB"))
	b.Break()
	b.Op(&Op{Kind: OpBranch}, WithID("join"))
	b.Program.Link("captureA", "join")
	b.Program.Link("captureB", "join")
	b.Program.Link("join", "phi")
	phi := b.Op(&Op{Kind: OpPhi, Inputs: []ValueID{a.Result, c.Result}, ResultKind: FrameRefKind}, WithID("phi"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: phi.Result, Frame: "tenant-A", Slot: 0}}, WithID("closure"))
	b.PassAsync(mk.Result, WithID("async"))
	b.Return("", "tenant-A", WithID("return"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusVulnerable || len(r.Findings) != 1 {
		t.Fatalf("status=%s findings=%+v", r.Status, r.Findings)
	}
	origins := r.Findings[0].Capture.Origins
	if !hasOp(origins, "captureA") || !hasOp(origins, "captureB") {
		t.Fatalf("origins = %v, want both branch captures", origins)
	}
}

func TestBudgetExhaustionIsUnknown(t *testing.T) {
	r := mustAnalyze(t, passAsyncUnpromoted(), Options{Budget: 1})
	if r.Status != StatusUnknown {
		t.Fatalf("status = %s, want unknown", r.Status)
	}
	if !r.Stats.Exhausted {
		t.Fatalf("stats = %+v, want exhausted", r.Stats)
	}
	if len(r.Unknowns) == 0 {
		t.Fatalf("expected an unknown reason")
	}
}

func TestRecursiveInstancesAreSeparatedByCallSiteAndFrameID(t *testing.T) {
	b := NewBuilder()
	b.FrameTemplate("worker", "tenant-A", FrameSlot{Tenant: "tenant-A", Data: "A-SECRET"})
	b.FrameAlloc("worker", 1, WithID("alloc1"), WithCallSite("call-1"))
	r1 := b.CaptureByRefSlot("worker", 0, true, WithID("capture1"), WithCallSite("call-1"))
	b.HeapPromote(r1.Result, WithID("promote1"))
	b.FrameAlloc("worker", 1, WithID("alloc2"), WithCallSite("call-2"))
	r2 := b.CaptureByRefSlot("worker", 0, true, WithID("capture2"), WithCallSite("call-2"))
	mk := b.MakeClosureWith([]Capture{{Kind: CaptureByRef, Source: r2.Result, Frame: "worker", Slot: 0, CallSite: "call-2"}}, WithID("closure2"))
	b.PassAsync(mk.Result, WithID("async2"))
	b.Return("", "worker", WithID("return2"), WithCallSite("call-2"))

	r := mustAnalyze(t, b.Program)
	if r.Status != StatusVulnerable || len(r.Findings) != 1 {
		t.Fatalf("status=%s findings=%+v", r.Status, r.Findings)
	}
	if r.Findings[0].Capture.CallSite != "call-2" {
		t.Fatalf("callsite = %q, want call-2", r.Findings[0].Capture.CallSite)
	}
	if !containsOp(r.Findings[0].AllocPath, "alloc2") || containsOp(r.Findings[0].AllocPath, "alloc1") {
		t.Fatalf("evidence path used the wrong recursive instance: %v", r.Findings[0].AllocPath)
	}
}

func containsOp(path []OpID, id OpID) bool {
	for _, x := range path {
		if x == id {
			return true
		}
	}
	return false
}

func hasOp(path []OpID, id OpID) bool { return containsOp(path, id) }

func joinPath(path []OpID) string {
	parts := make([]string, len(path))
	for i, id := range path {
		parts[i] = string(id)
	}
	return strings.Join(parts, ">")
}
