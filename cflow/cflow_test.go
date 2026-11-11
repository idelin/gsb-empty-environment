package cflow_test

import (
	"errors"
	"testing"

	"example.org/netsec/uid1234/cflow"
)

// Fixture layout:
//
//	module "core" (gen 1)
//	  main:     direct call site 1 -> worker
//	  worker:   indirect call site 2, allowed types {"handler"}
//	  handlerA: type "handler"
//	  evil:     type "gadget"
//	module "plug" (gen 1, then unloaded/reloaded)
//	  plugin:   type "handler"
func newFixture(t *testing.T) *cflow.Runtime {
	t.Helper()

	rt := cflow.New()
	coreGen := rt.RegisterModule("core")
	plugGen := rt.RegisterModule("plug")
	if coreGen != 1 || plugGen != 1 {
		t.Fatalf("initial generations = %d, %d, want 1, 1", coreGen, plugGen)
	}

	rt.RegisterFunc(cflow.FuncInfo{
		ID: "main", Module: "core", Type: "entry",
		CallSites: []cflow.CallSite{{ID: 1}},
	})
	rt.RegisterFunc(cflow.FuncInfo{
		ID: "worker", Module: "core", Type: "worker",
		CallSites: []cflow.CallSite{
			{ID: 1},
			{
				ID:           2,
				Indirect:     true,
				AllowedTypes: typeSet("handler"),
			}},
	})
	rt.RegisterFunc(cflow.FuncInfo{ID: "handlerA", Module: "core", Type: "handler"})
	rt.RegisterFunc(cflow.FuncInfo{ID: "evil", Module: "core", Type: "gadget"})
	rt.RegisterFunc(cflow.FuncInfo{ID: "plugin", Module: "plug", Type: "handler"})
	return rt
}

func typeSet(types ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(types))
	for _, typ := range types {
		set[typ] = struct{}{}
	}
	return set
}

func entry(thread int, fn string, site int) cflow.Event {
	return cflow.Event{Kind: cflow.EventFuncEntry, Thread: thread, FuncID: fn, CallSite: site}
}

func ret(thread int, fn string) cflow.Event {
	return cflow.Event{Kind: cflow.EventReturn, Thread: thread, FuncID: fn}
}

func indirect(thread int, caller, target string, site int) cflow.Event {
	return cflow.Event{
		Kind: cflow.EventIndirectCall, Thread: thread,
		FuncID: caller, CallSite: site, TargetID: target,
	}
}

func establish(thread, rp int) cflow.Event {
	return cflow.Event{Kind: cflow.EventRecoveryPoint, Thread: thread, RecoveryID: rp}
}

func unwind(thread, rp int) cflow.Event {
	return cflow.Event{Kind: cflow.EventUnwind, Thread: thread, RecoveryID: rp}
}

func assertViolation(t *testing.T, v *cflow.Violation, index int, reason string) {
	t.Helper()
	if v == nil {
		t.Fatalf("expected violation %s at event %d, got nil", reason, index)
	}
	if v.Index != index {
		t.Fatalf("violation index = %d, want %d (%s)", v.Index, index, v.Detail)
	}
	if v.Reason != reason {
		t.Fatalf("violation reason = %q, want %q (%s)", v.Reason, reason, v.Detail)
	}
}

func assertNoViolation(t *testing.T, v *cflow.Violation) {
	t.Helper()
	if v != nil {
		t.Fatalf("unexpected violation at event %d: %s", v.Index, v.Detail)
	}
}

// A plain call/return sequence through a direct and an indirect call must
// be accepted and leave the shadow stack balanced.
func TestHappyPath(t *testing.T) {
	rt := newFixture(t)

	v := rt.Process(
		entry(1, "main", 0),
		indirect(1, "worker", "handlerA", 2),
		entry(1, "worker", 1),
		entry(1, "handlerA", 2),
		ret(1, "handlerA"),
		ret(1, "worker"),
		ret(1, "main"),
	)
	assertNoViolation(t, v)
	if depth := rt.StackDepth(1); depth != 0 {
		t.Fatalf("final stack depth = %d, want 0", depth)
	}
}

// Recursion pushes many frames for the same function; a forged return that
// names the wrong function must be rejected at the exact forged event,
// while an honest recursive unwind is accepted.
func TestRecursionAndTamperedReturn(t *testing.T) {
	rt := newFixture(t)

	events := []cflow.Event{entry(1, "worker", 0)}
	for i := 0; i < 8; i++ {
		events = append(events, entry(1, "worker", 1))
	}
	// Honest returns leave two worker frames on the stack.
	for i := 0; i < 7; i++ {
		events = append(events, ret(1, "worker"))
	}
	if v := rt.Process(events...); v != nil {
		t.Fatalf("recursive setup rejected: %s", v.Detail)
	}
	if depth := rt.StackDepth(1); depth != 2 {
		t.Fatalf("depth after recursion = %d, want 2", depth)
	}

	// The attacker forges a return of a function that is not the top frame.
	forged := []cflow.Event{
		ret(1, "handlerA"),
		ret(1, "worker"),
	}
	v := rt.Process(forged...)
	assertViolation(t, v, 0, cflow.ReasonReturnMismatch)

	// State before the rejected event is untouched.
	if depth := rt.StackDepth(1); depth != 2 {
		t.Fatalf("depth after rejected return = %d, want 2", depth)
	}

	// A return on a thread with an empty shadow stack is the canonical
	// smashed-return-address case.
	if v := rt.Step(ret(9, "main")); !errors.Is(v, cflow.ErrReturnEmpty) {
		t.Fatalf("empty-thread return reason = %v, want ErrReturnEmpty", v)
	}
}

// An indirect target outside the call site's allowed type set is refused
// before any frame is pushed, and sentinel matching via errors.Is works.
func TestIndirectCallTypeEnforcement(t *testing.T) {
	rt := newFixture(t)
	assertNoViolation(t, rt.Step(entry(1, "worker", 0)))

	v := rt.Process(
		indirect(1, "worker", "evil", 2),
		entry(1, "evil", 2),
	)
	assertViolation(t, v, 0, cflow.ReasonTypeMismatch)
	if !errors.Is(v, cflow.ErrTypeMismatch) {
		t.Fatalf("errors.Is mismatch: %v", v)
	}
	if depth := rt.StackDepth(1); depth != 1 {
		t.Fatalf("depth after rejected indirect call = %d, want 1", depth)
	}

	// A handler-typed target from a currently loaded module is accepted.
	assertNoViolation(t, rt.Step(indirect(1, "worker", "handlerA", 2)))
	assertNoViolation(t, rt.Step(indirect(1, "worker", "plugin", 2)))

	// A direct call site cannot be driven as an indirect call.
	if v := rt.Step(indirect(1, "main", "handlerA", 1)); !errors.Is(v, cflow.ErrUnknownCallSite) {
		t.Fatalf("direct-site indirect reason = %v, want ErrUnknownCallSite", v)
	}
}

// Unloading a module retires its generation: indirect targets in the old
// image are refused, and a return through a frame captured under the old
// generation is refused even after the module is reloaded.
func TestModuleUnloadInvalidatesOldTargets(t *testing.T) {
	rt := newFixture(t)

	assertNoViolation(t, rt.Process(
		entry(1, "main", 0),
		entry(1, "plugin", 1),
	))

	rt.UnloadModule("plug")
	if gen := rt.ModuleGeneration("plug"); gen != -1 {
		t.Fatalf("unloaded generation = %d, want -1", gen)
	}

	// Old indirect target resolves into a retired image: earliest rejection
	// is the indirect call itself.
	v := rt.Process(
		indirect(1, "worker", "plugin", 2),
		entry(1, "handlerA", 1),
	)
	assertViolation(t, v, 0, cflow.ReasonModuleUnloaded)

	// Returning through the frame pinned to the old generation is also
	// rejected: the return address can no longer be trusted.
	if v := rt.Step(ret(1, "plugin")); !errors.Is(v, cflow.ErrModuleUnloaded) {
		t.Fatalf("stale return reason = %v, want ErrModuleUnloaded", v)
	}

	// Reload hands out generation 2; the same function ID is valid again but
	// distinct from the frames captured under generation 1.
	if gen := rt.RegisterModule("plug"); gen != 2 {
		t.Fatalf("reloaded generation = %d, want 2", gen)
	}
	assertNoViolation(t, rt.Step(indirect(1, "worker", "plugin", 2)))
}

// Recovery points support reuse: an unwind kills points owned by popped
// frames, after which the same ID can be established again. Establishing a
// duplicate while the point is alive is rejected immediately.
func TestRecoveryPointReuseAndLifecycle(t *testing.T) {
	rt := newFixture(t)

	assertNoViolation(t, rt.Process(
		entry(1, "main", 0),
		establish(1, 7),
		entry(1, "worker", 1),
		entry(1, "handlerA", 2),
	))

	if depth, ok := rt.RecoveryDepth(1, 7); !ok || depth != 0 {
		t.Fatalf("recovery depth = %d (ok=%v), want 0", depth, ok)
	}

	// Duplicate establishment while alive is the earliest rejection.
	v := rt.Process(
		establish(1, 7),
		unwind(1, 7),
	)
	assertViolation(t, v, 0, cflow.ReasonRecoveryExists)

	// A legitimate unwind pops both frames above the main-frame landing pad.
	assertNoViolation(t, rt.Step(unwind(1, 7)))
	if depth := rt.StackDepth(1); depth != 1 {
		t.Fatalf("depth after unwind = %d, want 1", depth)
	}
	// The landing pad lives in the surviving main frame, so it stays alive
	// and may catch another exception (recovery point reuse on the same
	// frame).
	if !rt.RecoveryAlive(1, 7) {
		t.Fatal("recovery point 7 should stay alive while its frame survives")
	}
	assertNoViolation(t, rt.Process(
		entry(1, "worker", 1),
		entry(1, "handlerA", 2),
		unwind(1, 7),
	))
	if depth := rt.StackDepth(1); depth != 1 {
		t.Fatalf("depth after second unwind = %d, want 1", depth)
	}

	// Returning through the owning frame kills the point, after which the ID
	// is reusable on a fresh frame.
	assertNoViolation(t, rt.Step(ret(1, "main")))
	if rt.RecoveryAlive(1, 7) {
		t.Fatal("recovery point 7 should be dead after main returned")
	}
	assertNoViolation(t, rt.Process(
		entry(1, "main", 0),
		establish(1, 7),
	))
	if !rt.RecoveryAlive(1, 7) {
		t.Fatal("recovery point 7 should be alive after ID reuse")
	}

	// An unwind that pops nothing is not an unwind.
	v = rt.Step(unwind(1, 7))
	assertViolation(t, v, 0, cflow.ReasonUnwindTop)

	// Unwinding to a never-registered point fails with not-found.
	if v := rt.Step(unwind(1, 99)); !errors.Is(v, cflow.ErrRecoveryNotFound) {
		t.Fatalf("unknown unwind reason = %v, want ErrRecoveryNotFound", v)
	}
}

// A recovery point is thread-local. A forged unwind from another thread
// must fail at the forged event even though the point is alive elsewhere.
func TestCrossThreadForgedUnwind(t *testing.T) {
	rt := newFixture(t)

	assertNoViolation(t, rt.Process(
		entry(1, "main", 0),
		establish(1, 7),
		entry(1, "worker", 1),
	))

	// Thread 2 builds an unrelated stack and tries to jump to thread 1's
	// landing pad. The lookup happens in thread 2's own map, so the event
	// is rejected before any stack mutation.
	assertNoViolation(t, rt.Step(entry(2, "main", 0)))
	v := rt.Process(
		unwind(2, 7),
		ret(2, "main"),
	)
	assertViolation(t, v, 0, cflow.ReasonRecoveryNotFound)
	if depth := rt.StackDepth(2); depth != 1 {
		t.Fatalf("thread 2 depth after forged unwind = %d, want 1", depth)
	}

	// The genuine owner can still unwind using the same point.
	assertNoViolation(t, rt.Step(unwind(1, 7)))
	if depth := rt.StackDepth(1); depth != 1 {
		t.Fatalf("thread 1 depth after real unwind = %d, want 1", depth)
	}
}

// Normal returns kill recovery points owned by the returning frame; a later
// unwind to the dead point is rejected rather than redirected.
func TestReturnKillsRecoveryPoint(t *testing.T) {
	rt := newFixture(t)

	assertNoViolation(t, rt.Process(
		entry(1, "main", 0),
		entry(1, "worker", 1),
		establish(1, 3),
		entry(1, "handlerA", 2),
		ret(1, "handlerA"),
		ret(1, "worker"),
	))
	if rt.RecoveryAlive(1, 3) {
		t.Fatal("recovery point 3 should be dead after worker returned")
	}

	v := rt.Process(
		unwind(1, 3),
		entry(1, "handlerA", 2),
	)
	assertViolation(t, v, 0, cflow.ReasonRecoveryNotFound)
	if depth := rt.StackDepth(1); depth != 1 {
		t.Fatalf("depth after rejected unwind = %d, want 1", depth)
	}
}

// Entry through an unknown call site and entry into a function of an
// unloaded module are both refused at the offending event.
func TestEntryValidation(t *testing.T) {
	rt := newFixture(t)
	assertNoViolation(t, rt.Step(entry(1, "main", 0)))

	v := rt.Step(entry(1, "worker", 42))
	assertViolation(t, v, 0, cflow.ReasonUnknownCallSite)

	rt.UnloadModule("plug")
	v = rt.Step(entry(1, "plugin", 1))
	assertViolation(t, v, 0, cflow.ReasonModuleUnloaded)

	if v := rt.Step(entry(1, "ghost", 1)); !errors.Is(v, cflow.ErrUnknownFunc) {
		t.Fatalf("unknown func reason = %v, want ErrUnknownFunc", v)
	}
}

// Shadow-stack frames retain the call site and generation captured at entry,
// giving the host enough data to localize a tampered return address.
func TestShadowStackRecordsCallSiteAndGeneration(t *testing.T) {
	rt := newFixture(t)
	assertNoViolation(t, rt.Process(
		entry(1, "main", 0),
		entry(1, "worker", 1),
		entry(1, "handlerA", 2),
	))

	got := rt.Stack(1)
	want := []cflow.FrameView{
		{FuncID: "main", CallSite: 0, Generation: 1},
		{FuncID: "worker", CallSite: 1, Generation: 1},
		{FuncID: "handlerA", CallSite: 2, Generation: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("stack length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
