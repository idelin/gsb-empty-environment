package netsec

import (
	"reflect"
	"testing"
)

func testState(t *testing.T, threads int, opts Options) *State {
	t.Helper()
	s, err := NewStateWithOptions(threads, opts)
	if err != nil {
		t.Fatalf("NewStateWithOptions: %v", err)
	}
	return s
}

func applyOK(t *testing.T, s *State, thread ThreadID, ev Event) Outcome {
	t.Helper()
	out, err := s.Apply(thread, ev)
	if err != nil {
		t.Fatalf("Apply(%s): %v", ev, err)
	}
	return out
}

func applyBad(t *testing.T, s *State, thread ThreadID, ev Event) error {
	t.Helper()
	before := s.key()
	_, err := s.Apply(thread, ev)
	if err == nil {
		t.Fatalf("Apply(%s) unexpectedly succeeded", ev)
	}
	if after := s.key(); after != before {
		t.Fatalf("invalid event %s mutated state", ev)
	}
	return err
}

func TestTryAdvanceGatingAndEpochCycle(t *testing.T) {
	s := testState(t, 3, Options{AutoReclaim: false})

	out := applyOK(t, s, UpdaterThread, TryAdvance())
	if !out.Advanced || s.Epoch() != 1 || s.Advances() != 1 {
		t.Fatalf("empty advance: advanced=%t epoch=%d advances=%d",
			out.Advanced, s.Epoch(), s.Advances())
	}

	applyOK(t, s, Thread0, Enter())
	if thread, _ := s.Thread(Thread0); !thread.Active || thread.Published != 1 {
		t.Fatalf("Enter did not publish E1: %+v", thread)
	}
	if out = applyOK(t, s, UpdaterThread, TryAdvance()); !out.Advanced || s.Epoch() != 2 {
		t.Fatalf("advance with current publisher: %+v epoch=%d", out, s.Epoch())
	}
	if out = applyOK(t, s, UpdaterThread, TryAdvance()); out.Advanced {
		t.Fatal("advance succeeded even though active thread published a stale epoch")
	}
	if s.Epoch() != 2 || s.Advances() != 2 {
		t.Fatalf("failed advance changed clock: epoch=%d advances=%d", s.Epoch(), s.Advances())
	}

	applyOK(t, s, Thread0, Exit())
	for i := 0; i < 6; i++ {
		applyOK(t, s, UpdaterThread, TryAdvance())
	}
	if s.Epoch() != 0 || s.Advances() != 8 {
		t.Fatalf("epoch did not wrap as expected: epoch=%d advances=%d", s.Epoch(), s.Advances())
	}
}

func TestSleepingActiveReaderBlocksGracePeriod(t *testing.T) {
	s := testState(t, 3, Options{AutoReclaim: false})
	ref := ObjectRef{ID: 1, Gen: 0}

	applyOK(t, s, Thread0, Enter())
	applyOK(t, s, UpdaterThread, ReplaceRoot(1))
	applyOK(t, s, Thread0, LoadRoot())
	applyOK(t, s, UpdaterThread, RetireObject(1))

	// The first advance is permitted because the active thread published the
	// current epoch, but the object was held during that advance, so it does
	// not count as a safe advance for this generation.
	if out := applyOK(t, s, UpdaterThread, TryAdvance()); !out.Advanced {
		t.Fatal("first advance should be permitted")
	}
	for i := 0; i < 4; i++ {
		if out := applyOK(t, s, UpdaterThread, TryAdvance()); out.Advanced {
			t.Fatal("sleeping active thread must block further advances")
		}
	}
	info, ok := s.Retirement(ref)
	if !ok || info.SafeAdvances != 0 || info.Reclaimed || s.CanReclaim(ref) {
		t.Fatalf("sleeping active holder made object reclaimable: %+v", info)
	}
	if obj, _ := s.Object(ref); obj.Status != ObjectRetired {
		t.Fatalf("object status after blocked advances = %s", obj.Status)
	}

	applyOK(t, s, Thread0, Exit())
	if out := applyOK(t, s, UpdaterThread, TryAdvance()); !out.Advanced {
		t.Fatal("advance after normal Exit should succeed")
	}
	if s.CanReclaim(ref) {
		t.Fatal("object became reclaimable after only one safe advance")
	}
	if out := applyOK(t, s, UpdaterThread, TryAdvance()); !out.Advanced {
		t.Fatal("second advance after Exit should succeed")
	}
	info, _ = s.Retirement(ref)
	if info.SuccessfulAdvances != 3 || info.SafeAdvances != 2 {
		t.Fatalf("unexpected grace counters: %+v", info)
	}
	if !s.CanReclaim(ref) {
		t.Fatalf("object should be reclaimable after two safe advances: %+v", info)
	}
	if got := s.Settle(); !reflect.DeepEqual(got, []ObjectRef{ref}) {
		t.Fatalf("Settle = %v, want %v", got, ref)
	}
}

func TestQuiescentRequiresInactiveAndCannotReplaceExit(t *testing.T) {
	s := testState(t, 3, Options{AutoReclaim: false})

	applyOK(t, s, Thread0, Enter())
	if err := applyBad(t, s, Thread0, Quiescent()); err == nil {
		t.Fatal("expected Quiescent error")
	}
	if thread, _ := s.Thread(Thread0); !thread.Active {
		t.Fatal("rejected Quiescent deactivated the thread")
	}

	applyOK(t, s, Thread0, Exit())
	applyOK(t, s, Thread0, Quiescent())
	thread, _ := s.Thread(Thread0)
	if thread.Active || !thread.Quiescent {
		t.Fatalf("inactive Quiescent state = %+v", thread)
	}

	applyOK(t, s, Thread0, Enter())
	thread, _ = s.Thread(Thread0)
	if !thread.Active || thread.Quiescent {
		t.Fatalf("Enter after Quiescent = %+v", thread)
	}
}

func TestLoadRootPreconditionIsConfigurable(t *testing.T) {
	s := testState(t, 1, DefaultOptions())
	applyOK(t, s, UpdaterThread, ReplaceRoot(11))
	applyOK(t, s, Thread0, LoadRoot())
	if thread, _ := s.Thread(Thread0); len(thread.Held) != 1 || thread.Active {
		t.Fatalf("base model did not record inactive LoadRoot: %+v", thread)
	}

	strict := DefaultOptions()
	strict.RequireActiveForLoadRoot = true
	s = testState(t, 1, strict)
	applyOK(t, s, UpdaterThread, ReplaceRoot(11))
	applyBad(t, s, Thread0, LoadRoot())
}

func TestRetireUsesAdvanceCountNotEpochDelta(t *testing.T) {
	s := testState(t, 0, Options{AutoReclaim: false})
	for i := 0; i < 3; i++ {
		applyOK(t, s, UpdaterThread, TryAdvance())
	}
	applyOK(t, s, UpdaterThread, ReplaceRoot(9))
	ref := ObjectRef{ID: 9, Gen: 0}
	applyOK(t, s, UpdaterThread, RetireGen(ref))

	applyOK(t, s, UpdaterThread, TryAdvance())
	if s.CanReclaim(ref) {
		t.Fatal("reclaimed after one advance")
	}
	applyOK(t, s, UpdaterThread, TryAdvance())
	info, _ := s.Retirement(ref)
	if info.EpochDelta != 2 || info.SuccessfulAdvances != 2 || info.SafeAdvances != 2 {
		t.Fatalf("unexpected accounting: %+v", info)
	}
	if !s.CanReclaim(ref) {
		t.Fatalf("two advances should permit reclamation: %+v", info)
	}

	// Four advances bring the numeric epoch back to the retirement epoch.
	// Eligibility must still be true because a successful-advance counter,
	// not the numeric epoch difference, records the elapsed grace periods.
	s2 := testState(t, 0, Options{AutoReclaim: false})
	applyOK(t, s2, UpdaterThread, ReplaceRoot(3))
	ref2 := ObjectRef{ID: 3, Gen: 0}
	applyOK(t, s2, UpdaterThread, RetireGen(ref2))
	for i := 0; i < 4; i++ {
		applyOK(t, s2, UpdaterThread, TryAdvance())
	}
	info2, _ := s2.Retirement(ref2)
	if info2.EpochDelta != 0 || info2.SuccessfulAdvances != 4 || info2.SafeAdvances != 4 {
		t.Fatalf("unexpected wraparound accounting: %+v", info2)
	}
	if !s2.CanReclaim(ref2) {
		t.Fatalf("epoch-relative check incorrectly rejected wraparound: %+v", info2)
	}
}

func TestPerObjectGraceClocksAreIndependent(t *testing.T) {
	s := testState(t, 0, Options{AutoReclaim: false})
	first := ObjectRef{ID: 1, Gen: 0}
	second := ObjectRef{ID: 2, Gen: 0}

	applyOK(t, s, UpdaterThread, ReplaceRoot(1))
	applyOK(t, s, UpdaterThread, RetireGen(first))
	applyOK(t, s, UpdaterThread, TryAdvance())

	applyOK(t, s, UpdaterThread, ReplaceRoot(2))
	applyOK(t, s, UpdaterThread, RetireGen(second))
	applyOK(t, s, UpdaterThread, TryAdvance())

	if !s.CanReclaim(first) {
		t.Fatalf("first object should have two safe advances")
	}
	if s.CanReclaim(second) {
		t.Fatalf("second object should have only one safe advance")
	}
	applyOK(t, s, UpdaterThread, TryAdvance())
	if !s.CanReclaim(second) {
		t.Fatal("second object should be eligible after its second safe advance")
	}
}

func TestRetirePreviousRootAfterReplacement(t *testing.T) {
	s := testState(t, 0, Options{AutoReclaim: false})
	oldRule := ObjectRef{ID: 1, Gen: 0}
	newRule := ObjectRef{ID: 2, Gen: 0}

	applyOK(t, s, UpdaterThread, ReplaceRoot(1))
	applyOK(t, s, UpdaterThread, ReplaceRoot(2))
	applyOK(t, s, UpdaterThread, RetireObject(1))
	if root := s.Root(); root == nil || *root != newRule {
		t.Fatalf("retiring the old rule changed the current root: %v", root)
	}
	if obj, _ := s.Object(oldRule); obj.Status != ObjectRetired {
		t.Fatalf("old rule status = %s", obj.Status)
	}
	applyOK(t, s, UpdaterThread, Retire())
	if root := s.Root(); root != nil {
		t.Fatalf("retiring the current root left root=%v", root)
	}
}

func TestAddressReuseCreatesNewGeneration(t *testing.T) {
	s := testState(t, 1, DefaultOptions())
	old := ObjectRef{ID: 7, Gen: 0}

	applyOK(t, s, UpdaterThread, ReplaceRoot(7))
	if root := s.Root(); root == nil || *root != old {
		t.Fatalf("initial root = %v, want %v", root, old)
	}
	applyOK(t, s, UpdaterThread, RetireObject(7))
	applyOK(t, s, UpdaterThread, TryAdvance())
	out := applyOK(t, s, UpdaterThread, TryAdvance())
	if !reflect.DeepEqual(out.Reclaimed, []ObjectRef{old}) {
		t.Fatalf("reclaimed = %v, want %v", out.Reclaimed, old)
	}

	applyOK(t, s, UpdaterThread, ReplaceRoot(7))
	newRoot := ObjectRef{ID: 7, Gen: 1}
	if root := s.Root(); root == nil || *root != newRoot {
		t.Fatalf("reused root = %v, want %v", root, newRoot)
	}
	if err := applyBad(t, s, UpdaterThread, ReplaceRootGen(old)); err == nil {
		t.Fatal("expected retired/reclaimed old generation to be rejected")
	}

	applyOK(t, s, UpdaterThread, RetireObject(7))
	if err := applyBad(t, s, UpdaterThread, ReplaceRoot(7)); err == nil {
		t.Fatal("address was reused before the retired generation was reclaimed")
	}
	applyOK(t, s, UpdaterThread, TryAdvance())
	applyOK(t, s, UpdaterThread, TryAdvance())
	applyOK(t, s, UpdaterThread, ReplaceRoot(7))
	newest := ObjectRef{ID: 7, Gen: 2}
	if root := s.Root(); root == nil || *root != newest {
		t.Fatalf("second reuse = %v, want %v", root, newest)
	}
}

func TestDereferenceOldGenerationAfterAddressReuse(t *testing.T) {
	s := testState(t, 1, DefaultOptions())
	old := ObjectRef{ID: 4, Gen: 0}

	applyOK(t, s, Thread0, Enter())
	applyOK(t, s, UpdaterThread, ReplaceRoot(4))
	applyOK(t, s, Thread0, LoadRoot())
	applyOK(t, s, Thread0, Exit())
	applyOK(t, s, UpdaterThread, RetireObject(4))
	applyOK(t, s, UpdaterThread, TryAdvance())
	applyOK(t, s, UpdaterThread, TryAdvance())

	// Reuse the same address with a new generation. The old generation is
	// still reclaimed; a stale local must not be confused with the new root.
	applyOK(t, s, UpdaterThread, ReplaceRoot(4))
	applyOK(t, s, Thread0, Enter())
	out := applyOK(t, s, Thread0, Dereference())
	if out.Violation == nil {
		t.Fatal("expected use-after-free of the old generation")
	}
	if out.Violation.Ref != old || out.Violation.Kind != ViolationUseAfterFree {
		t.Fatalf("violation = %+v, want UAF of %s", out.Violation, old)
	}
}
