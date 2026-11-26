package stlf

import (
	"testing"
	"time"
)

func TestArchitecturalLoadUsesFullAddress(t *testing.T) {
	h := ExampleHost()
	h.PublicMemory[0x00a] = 0x55
	code := []Instruction{
		Load(1, 0x405),
		Store(0x40a, 1),
		Load(2, 0x00a),
		Probe(2),
	}
	in := Input{PublicLow6: 0, Secret: 0x42}
	arch := RunArchitectural(h, code, in)
	spec := RunSpeculative(h, code, in)
	if got, want := arch.ArchTrace.String(), "[5]"; got != want {
		t.Fatalf("architectural load forwarded an alias: got %s want %s", got, want)
	}
	if got, want := spec.SpecTrace.String(), "[2]"; got != want {
		t.Fatalf("speculative load did not partial-forward: got %s want %s", got, want)
	}
	if !spec.PartialForward {
		t.Fatal("expected a partial forwarding event")
	}
}

func TestPublicLow6IndexedAddress(t *testing.T) {
	h := ExampleHost()
	code := []Instruction{
		LoadIndexed(1, 0x400, 0), // effective address = host high6 + public low6
		Store(0x40a, 1),
		Load(2, 0x00a),
		Probe(2),
	}
	in := Input{PublicLow6: 5, Secret: 0x37}
	arch := RunArchitectural(h, code, in)
	spec := RunSpeculative(h, code, in)
	if got, want := arch.ArchTrace.String(), "[0]"; got != want {
		t.Fatalf("arch trace=%s want %s", got, want)
	}
	if got, want := spec.SpecTrace.String(), "[7]"; got != want {
		t.Fatalf("spec trace=%s want %s", got, want)
	}
	if len(spec.Forwards) == 0 || spec.Forwards[0].LoadAddr != 0x00a || spec.Forwards[0].StoreAddr != 0x40a {
		t.Fatalf("unexpected forwarding: %+v", spec.Forwards)
	}
}

func TestNestedMispredictDepthIsTwo(t *testing.T) {
	h := ExampleHost()
	code := []Instruction{
		CmpBranchImm(0, 0x00, 2, true), // depth 1
		Probe(3),
		CmpBranchImm(0, 0x00, 3, true), // depth 2
		CmpBranchImm(0, 0x00, 4, true), // third wrong branch: stop
		Probe(4),
	}
	in := Input{PublicLow6: 1, Secret: 0x01}
	ex := RunSpeculative(h, code, in)
	if ex.MaxMispredictDepth != 2 {
		t.Fatalf("max depth = %d, want 2", ex.MaxMispredictDepth)
	}
	if ex.Mispredictions != 3 {
		t.Fatalf("mispredictions = %d, want 3 observed", ex.Mispredictions)
	}
	if len(ex.SpecTrace) != 0 {
		t.Fatalf("speculation should stop before the third wrong path, trace=%s", ex.SpecTrace)
	}
	if ex.RollbackReason != "mispredict-depth" {
		t.Fatalf("rollback reason = %q", ex.RollbackReason)
	}
}

func TestPartialForwardWindow(t *testing.T) {
	h := ExampleHost()
	makeCode := func(fillers int) []Instruction {
		code := []Instruction{
			Load(1, 0x405),
			Store(0x40a, 1),
			Load(2, 0x00a), // cycle 3, visible through cycle 7
		}
		for i := 0; i < fillers; i++ {
			code = append(code, XorImm(6, 0))
		}
		code = append(code, Probe(2))
		return code
	}
	in := Input{PublicLow6: 0, Secret: 0x4c}

	inside := RunSpeculative(h, makeCode(3), in) // probe at cycle 7
	if got, want := inside.SpecTrace.String(), "[c]"; got != want {
		t.Fatalf("probe inside four-cycle window: got %s want %s", got, want)
	}
	outside := RunSpeculative(h, makeCode(4), in) // probe at cycle 8
	if len(outside.SpecTrace) != 0 {
		t.Fatalf("probe after four-cycle window should be discarded, got %s", outside.SpecTrace)
	}
	if outside.RollbackReason != "stlf-window" {
		t.Fatalf("rollback reason = %q", outside.RollbackReason)
	}
}

func TestBarrierDrainsPendingSpeculation(t *testing.T) {
	h := ExampleHost()
	code := []Instruction{
		Load(1, 0x405),
		Store(0x40a, 1),
		Load(2, 0x00a),
		Barrier(),
		Probe(2),
	}
	ex := RunSpeculative(h, code, Input{PublicLow6: 0, Secret: 0x7a})
	if len(ex.SpecTrace) != 0 {
		t.Fatalf("barrier did not drain speculation, trace=%s", ex.SpecTrace)
	}
	if ex.RollbackReason != "barrier" {
		t.Fatalf("rollback reason = %q", ex.RollbackReason)
	}
	if !ex.PartialForward {
		t.Fatal("expected the partial forward to have happened before the barrier")
	}
}

func TestStoreBufferIsBounded(t *testing.T) {
	h := ExampleHost()
	code := []Instruction{
		StoreImm(0x401, 1),
		StoreImm(0x402, 2),
		StoreImm(0x403, 3),
		StoreImm(0x404, 4),
		StoreImm(0x406, 5),
		Load(1, 0x401),
		Probe(1),
	}
	in := Input{PublicLow6: 0, Secret: 0}
	arch := RunArchitectural(h, code, in)
	if arch.MaxStoreOccupancy != StoreBufferSize {
		t.Fatalf("architectural store occupancy=%d, want %d", arch.MaxStoreOccupancy, StoreBufferSize)
	}
	if got, want := arch.ArchTrace.String(), "[1]"; got != want {
		t.Fatalf("oldest store should have committed to memory: %s", got)
	}
	spec := RunSpeculative(h, code, in)
	if spec.MaxStoreOccupancy > StoreBufferSize {
		t.Fatalf("speculative store occupancy=%d exceeds %d", spec.MaxStoreOccupancy, StoreBufferSize)
	}
}

func TestDemoFindsSpeculativeWitness(t *testing.T) {
	sc := ExampleScenario()
	res := FindWitness(sc.Host, sc.Code, SearchOptions{
		PublicValues: []uint8{0, 1},
		SecretValues: []uint8{0, 1, 2, 3},
		Timeout:      5 * time.Second,
	})
	if res.Status != StatusWitness || res.Witness == nil {
		t.Fatalf("status=%s message=%s", res.Status, res.Message)
	}
	w := res.Witness
	if w.PublicLow6 != 0 || w.SecretA == w.SecretB {
		t.Fatalf("unexpected witness metadata: %+v", w)
	}
	if !w.A.ArchTrace.Equal(w.B.ArchTrace) {
		t.Fatalf("architectural traces differ: %s vs %s", w.A.ArchTrace, w.B.ArchTrace)
	}
	if w.A.SpecTrace.Equal(w.B.SpecTrace) {
		t.Fatalf("speculative traces are equal: %s", w.A.SpecTrace)
	}
	if !w.UsesPartialForward {
		t.Fatal("demo witness should exercise partial STLF")
	}
	for _, s := range []byte{w.SecretA, w.SecretB} {
		if int(s&0x0f) >= len(w.A.SpecTrace)+len(w.B.SpecTrace) {
			t.Fatalf("unexpected trace for secret %#x", s)
		}
	}

	nested := FindWitness(sc.Host, sc.Code, SearchOptions{
		PublicValues: []uint8{1},
		SecretValues: []uint8{0, 1},
		Timeout:      5 * time.Second,
		Metric:       CostFirstDivergence,
	})
	if nested.Witness == nil || nested.Witness.A.MaxMispredictDepth != 2 {
		t.Fatalf("public-low 1 should exercise the depth-2 wrong path: %+v", nested.Witness)
	}
}

func TestArchitecturalRollbackRestoresPublicState(t *testing.T) {
	sc := ExampleScenario()
	inA := Input{PublicLow6: 0, Secret: 0x41}
	inB := Input{PublicLow6: 0, Secret: 0x42}
	a := Execute(sc.Host, sc.Code, inA)
	b := Execute(sc.Host, sc.Code, inB)
	if a.FinalRegs != b.FinalRegs {
		t.Fatalf("final registers leak: %v vs %v", a.FinalRegs, b.FinalRegs)
	}
	if !a.ArchTrace.Equal(b.ArchTrace) {
		t.Fatalf("committed traces differ: %s vs %s", a.ArchTrace, b.ArchTrace)
	}
	if a.SpecTrace.Equal(b.SpecTrace) {
		t.Fatalf("speculative traces should differ: %s vs %s", a.SpecTrace, b.SpecTrace)
	}
	ra, err := ReferenceRun(sc.Host, sc.Code, inA)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := ReferenceRun(sc.Host, sc.Code, inB)
	if err != nil {
		t.Fatal(err)
	}
	// The secret input slot itself is initialized differently; every other
	// byte, especially the speculative store target 0x40a, must be restored.
	for i := range ra.Memory {
		if i == int(sc.Host.SecretSlot) {
			continue
		}
		if ra.Memory[i] != rb.Memory[i] {
			t.Fatalf("final memory byte %#x differs: %#x vs %#x", i, ra.Memory[i], rb.Memory[i])
		}
	}
	if ra.Memory[0x40a] != 0 {
		t.Fatalf("speculative store survived rollback at 0x40a: %#x", ra.Memory[0x40a])
	}
}

func TestNativeArchitecturalLeakIsSeparate(t *testing.T) {
	h := ExampleHost()
	code := []Instruction{
		Load(1, 0x405),
		Probe(1),
	}
	res := FindWitness(h, code, SearchOptions{
		PublicValues: []uint8{0},
		SecretValues: []uint8{0, 1, 2, 3},
		Timeout:      5 * time.Second,
	})
	if res.Status != StatusNativeLeak {
		t.Fatalf("status=%s, want %s", res.Status, StatusNativeLeak)
	}
	if res.NativeCount == 0 || len(res.Native) == 0 {
		t.Fatal("expected native leak witnesses")
	}
}

func TestTimeoutReturnsUnknown(t *testing.T) {
	sc := ExampleScenario()
	res := FindWitness(sc.Host, sc.Code, SearchOptions{Timeout: time.Nanosecond})
	if res.Status != StatusUnknown || !res.Unknown || !res.TimedOut {
		t.Fatalf("status=%s unknown=%v timedout=%v, want Unknown", res.Status, res.Unknown, res.TimedOut)
	}
	if res.Witness != nil {
		t.Fatal("timeout result must not claim a witness")
	}
}

func TestMinimalPatchOnSafeProgram(t *testing.T) {
	h := ExampleHost()
	code := []Instruction{Probe(3)}
	patch, report := FindMinimalBarrierPatch(h, code, PatchSearchOptions{
		VerifyOptions: VerifyOptions{
			PublicValues: []uint8{0, 1},
			SecretValues: []uint8{0, 1, 2, 3},
			Timeout:      5 * time.Second,
		},
	})
	if len(patch.Positions) != 0 {
		t.Fatalf("safe program should need no barrier, got %v", patch.Positions)
	}
	if !report.Valid || !report.LeakFree {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestNativeLeakCannotBePatchedAway(t *testing.T) {
	h := ExampleHost()
	code := []Instruction{
		Load(1, 0x405),
		Probe(1),
	}
	patch, report := FindMinimalBarrierPatch(h, code, PatchSearchOptions{
		VerifyOptions: VerifyOptions{
			PublicValues: []uint8{0},
			SecretValues: []uint8{0, 1, 2, 3},
			Timeout:      5 * time.Second,
		},
	})
	if len(patch.Positions) != 0 || report.Valid || !report.NativeVulnerable {
		t.Fatalf("native leak should be reported as unfixable: patch=%v report=%+v", patch, report)
	}
}

func TestReferenceStateMatchesArchitecturalExecution(t *testing.T) {
	sc := ExampleScenario()
	in := Input{PublicLow6: 3, Secret: 0x5a}
	arch := RunArchitectural(sc.Host, sc.Code, in)
	ref, err := ReferenceRun(sc.Host, sc.Code, in)
	if err != nil {
		t.Fatal(err)
	}
	if arch.FinalRegs != ref.Regs {
		t.Fatalf("registers differ: %v vs %v", arch.FinalRegs, ref.Regs)
	}
	if arch.FinalMemHash != ref.MemHash() {
		t.Fatal("memory differs")
	}
	if !arch.ArchTrace.Equal(ref.ArchTrace) {
		t.Fatalf("trace differs: %s vs %s", arch.ArchTrace, ref.ArchTrace)
	}
}

func TestInsertBarriersRepairsTargets(t *testing.T) {
	h := ExampleHost()
	h.InitialRegs[3] = 0x03
	h.InitialRegs[4] = 0x07
	code := []Instruction{
		CmpBranchImm(0, 0x00, 2, true),
		Probe(3),
		Probe(4),
	}
	patched, err := InsertBarriers(code, NewPatch(1))
	if err != nil {
		t.Fatal(err)
	}
	if len(patched) != 4 || patched[1].Op != OpBarrier {
		t.Fatalf("unexpected patched code: %+v", patched)
	}
	if patched[0].Target != 3 {
		t.Fatalf("branch target = %d, want 3 (original instruction 2)", patched[0].Target)
	}
	in := Input{PublicLow6: 0, Secret: 0}
	base, err := ReferenceRun(h, code, in)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ReferenceRun(h, patched, in)
	if err != nil {
		t.Fatal(err)
	}
	if !StatesEquivalent(base, after, code, patched) {
		t.Fatalf("barrier insertion changed semantics: base=%v after=%v", base.ArchTrace, after.ArchTrace)
	}
}

func TestInsertMultipleBarriersRepairsTargets(t *testing.T) {
	h := ExampleHost()
	h.InitialRegs[3], h.InitialRegs[4], h.InitialRegs[5] = 0x03, 0x04, 0x05
	code := []Instruction{
		CmpBranchImm(0, 0x00, 3, true),
		Probe(3),
		Probe(4),
		Probe(5),
	}
	patched, err := InsertBarriers(code, NewPatch(2, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(patched) != 6 {
		t.Fatalf("patched len=%d", len(patched))
	}
	if patched[0].Target != 5 {
		t.Fatalf("branch target=%d, want 5", patched[0].Target)
	}
	for _, pub := range []uint8{0, 1} {
		in := Input{PublicLow6: pub, Secret: 0}
		base, err := ReferenceRun(h, code, in)
		if err != nil {
			t.Fatal(err)
		}
		after, err := ReferenceRun(h, patched, in)
		if err != nil {
			t.Fatal(err)
		}
		if !StatesEquivalent(base, after, code, patched) {
			t.Fatalf("pub=%d semantics changed: %s vs %s", pub, base.ArchTrace, after.ArchTrace)
		}
	}
}

func TestMinimalBarrierPatchEliminatesLeak(t *testing.T) {
	sc := ExampleScenario()
	vopt := VerifyOptions{
		PublicValues: []uint8{0, 1},
		SecretValues: []uint8{0, 1, 2, 3},
		Timeout:      10 * time.Second,
	}
	patch, report := FindMinimalBarrierPatch(sc.Host, sc.Code, PatchSearchOptions{VerifyOptions: vopt})
	if !report.Valid || !report.LeakFree || !report.SemanticsPreserved {
		t.Fatalf("minimal patch invalid: patch=%s message=%s", patch, report.Message)
	}
	if len(patch.Positions) != 1 {
		t.Fatalf("expected one barrier, got %v", patch.Positions)
	}
	if got, want := patch.Positions[0], 14; got != want {
		t.Fatalf("chosen barrier site = %d, want latest effective site %d", got, want)
	}
	// The leak happens at instruction 14, so a barrier after it is too late.
	late := VerifyBarrierPatch(sc.Host, sc.Code, NewPatch(15), vopt)
	if late.LeakFree {
		t.Fatal("barrier after the leaking probe should not remove the leak")
	}
}

func TestParser(t *testing.T) {
	src := `
start:
    cmp_branch r0, 0x00, leak, taken
    probe(r3)
    cmp_branch r4, r4, done, taken
leak:
    load r1, [0x405]
    store r1, [0x40a]
    load r2, [0x00a]
    probe r2
done:
    barrier
`
	code, err := ParseProgram(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 8 {
		t.Fatalf("len=%d", len(code))
	}
	if code[0].Target != 3 {
		t.Fatalf("branch target=%d, want 3", code[0].Target)
	}
	if code[2].Target != 7 {
		t.Fatalf("branch target=%d, want 7", code[2].Target)
	}
	if code[7].Op != OpBarrier {
		t.Fatalf("last op=%s", code[7].Op)
	}
}
