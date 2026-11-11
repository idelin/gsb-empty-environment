package model

import "testing"

const (
	t0 = 0
	t1 = 1
)

// readerProgram is the canonical hazard-pointer reader sequence:
// load root -> publish protection -> re-read root -> deref -> clear.
func readerProgram() Program {
	return Program{
		{Kind: OpLoadRoot, Thread: t1, Root: "head", Reg: "p", Label: "read root"},
		{Kind: OpProtect, Thread: t1, Slot: 0, Addr: "A", Label: "stage hazard A"},
		{Kind: OpFlushHP, Thread: t1, Label: "publish hazard (late?)"},
		{Kind: OpVerifyRoot, Thread: t1, Root: "head", Reg: "p", Label: "re-read root"},
		{Kind: OpDeref, Thread: t1, Reg: "p", Label: "use object"},
	}
}

// reclaimerProgram unlinks A by swinging the root to a fresh object B,
// which lets a reader whose protection published in time keep using A while
// the reclaimer still gets to scan and retire the old generation.
func reclaimerProgram() Program {
	return Program{
		{Kind: OpRetire, Thread: t0, Addr: "A", Label: "retire A"},
		{Kind: OpSetRoot, Thread: t0, Root: "head", Addr: "B", Label: "unlink A, install B"},
		{Kind: OpFlushRoot, Thread: t0, Label: "publish unlink"},
		{Kind: OpScan, Thread: t0, Label: "reclaim"},
	}
}

func initialRoots() map[string]string {
	return map[string]string{"head": "A"}
}

// initialRootsWithReplacement also pre-registers a live replacement object B.
func initialRootsWithReplacement() map[string]string {
	return map[string]string{"head": "A", "next": "B"}
}

func TestLatePublicationIsWitnessed(t *testing.T) {
	progs := Programs{
		t0: {
			{Kind: OpRetire, Thread: t0, Addr: "A", Label: "retire A"},
			{Kind: OpSetRoot, Thread: t0, Root: "head", Addr: "", Label: "unlink root"},
			{Kind: OpFlushRoot, Thread: t0, Label: "publish unlink"},
			{Kind: OpScan, Thread: t0, Label: "reclaim"},
		},
		// Reader publishes its hazard pointer, but the publish fence is
		// allowed to land after the reclaimer's scan. The re-read gate is
		// disabled here: this models an implementation that got publication
		// ordering wrong, so the delayed hazard cannot save the deref.
		t1: {
			{Kind: OpLoadRoot, Thread: t1, Root: "head", Reg: "p", Label: "read root"},
			{Kind: OpProtect, Thread: t1, Slot: 0, Addr: "A", Label: "stage hazard A"},
			{Kind: OpFlushHP, Thread: t1, Label: "publish hazard (late?)"},
			{Kind: OpDeref, Thread: t1, Reg: "p", Label: "use object"},
		},
	}
	cfg := DefaultConfig()
	cfg.RequireConsistentReread = false
	rep, err := Enumerate([]int{t0, t1}, initialRoots(), cfg, progs)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe() {
		t.Fatal("expected violations, got safe report:\n" + RenderReport(rep))
	}
	if rep.ViolationCounts[ErrUseAfterFree] == 0 {
		t.Fatal("expected a USE_AFTER_FREE witness for the late-publish interleaving:\n" + RenderReport(rep))
	}
	t.Log("\n" + RenderReport(rep))
	t.Log("\n" + RenderWitness(rep.Witnesses[ErrUseAfterFree]))
}

func TestMissingPublicationIsWitnessed(t *testing.T) {
	progs := Programs{
		t0: {
			{Kind: OpRetire, Thread: t0, Addr: "A", Label: "retire A"},
			{Kind: OpSetRoot, Thread: t0, Root: "head", Addr: "", Label: "unlink root"},
			{Kind: OpFlushRoot, Thread: t0, Label: "publish unlink"},
			{Kind: OpScan, Thread: t0, Label: "reclaim"},
		},
		// Buggy reader: hazard is never published. The re-read obligation is
		// switched off here so the test isolates the missing-fence hazard
		// instead of the missing-reread one.
		t1: {
			{Kind: OpLoadRoot, Thread: t1, Root: "head", Reg: "p", Label: "read root"},
			{Kind: OpProtect, Thread: t1, Slot: 0, Addr: "A", Label: "stage hazard only"},
			{Kind: OpDeref, Thread: t1, Reg: "p", Label: "deref without publish/re-read"},
		},
	}
	cfg := DefaultConfig()
	cfg.RequireConsistentReread = false
	rep, err := Enumerate([]int{t0, t1}, initialRoots(), cfg, progs)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ViolationCounts[ErrUseAfterFree] == 0 {
		t.Fatal("missing-publish reader must allow a USE_AFTER_FREE deref:\n" + RenderReport(rep))
	}
}

func TestAddressReuseIsWitnessed(t *testing.T) {
	progs := Programs{
		t0: {
			{Kind: OpRetire, Thread: t0, Addr: "A", Label: "retire A"},
			{Kind: OpSetRoot, Thread: t0, Root: "head", Addr: "", Label: "unlink A"},
			{Kind: OpFlushRoot, Thread: t0, Label: "publish unlink"},
			{Kind: OpScan, Thread: t0, Label: "reclaim A"},
			{Kind: OpAlloc, Thread: t0, Reg: "b", ReuseAddr: "A", Label: "reuse address A for B"},
		},
		t1: {
			{Kind: OpLoadRoot, Thread: t1, Root: "head", Reg: "p", Label: "read root A"},
			{Kind: OpProtect, Thread: t1, Slot: 0, Addr: "A", Label: "hazard A"},
			{Kind: OpFlushHP, Thread: t1, Label: "publish hazard"},
			// Protection is withdrawn before the object is reclaimed.
			{Kind: OpClearHP, Thread: t1, Slot: 0, Label: "release hazard"},
			{Kind: OpFlushHP, Thread: t1, Label: "publish clear"},
			{Kind: OpDeref, Thread: t1, Reg: "p", Label: "stale deref after reuse"},
		},
	}
	cfg := DefaultConfig()
	cfg.RequireConsistentReread = false // isolate the ABA/address-reuse hazard
	rep, err := Enumerate([]int{t0, t1}, initialRoots(), cfg, progs)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ViolationCounts[ErrAddressReuse] == 0 {
		t.Fatal("expected ADDRESS_REUSE_ABA witness:\n" + RenderReport(rep))
	}
	t.Log("\n" + RenderWitness(rep.Witnesses[ErrAddressReuse]))
}

func TestPartialScanIsRejected(t *testing.T) {
	progs := Programs{
		t1: {
			{Kind: OpProtect, Thread: t1, Slot: 0, Addr: "A", Label: "hazard A"},
			{Kind: OpFlushHP, Thread: t1, Label: "publish hazard"},
		},
		t0: {
			{Kind: OpRetire, Thread: t0, Addr: "A", Label: "retire A"},
			{Kind: OpScan, Thread: t0, IncludeThreads: []int{t0}, Label: "scan self only"},
		},
	}
	rep, err := Enumerate([]int{t0, t1}, initialRoots(), DefaultConfig(), progs)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ViolationCounts[ErrPartialScan] == 0 {
		t.Fatal("partial scan ignoring T1's hazard must be rejected:\n" + RenderReport(rep))
	}
}

func TestCompliantProtocolIsSafe(t *testing.T) {
	progs := Programs{
		t0: reclaimerProgram(),
		t1: readerProgram(),
	}
	rep, err := Enumerate([]int{t0, t1}, initialRootsWithReplacement(), DefaultConfig(), progs)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Safe() {
		t.Fatal("compliant hazard-pointer protocol must be safe:\n" +
			RenderReport(rep) + "\n" +
			RenderWitness(rep.Witnesses[firstViolation(rep)]))
	}
	if rep.Completed == 0 {
		t.Fatal("expected completed interleavings")
	}
}

func TestDerefBeforeConsistencyIsRejected(t *testing.T) {
	progs := Programs{
		t1: {
			{Kind: OpLoadRoot, Thread: t1, Root: "head", Reg: "p", Label: "read root"},
			{Kind: OpDeref, Thread: t1, Reg: "p", Label: "deref without re-read"},
		},
	}
	rep, err := Enumerate([]int{t1}, initialRoots(), DefaultConfig(), progs)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ViolationCounts[ErrDerefBeforeConsistency] == 0 {
		t.Fatal("deref before a consistent re-read must be rejected:\n" + RenderReport(rep))
	}
}

func firstViolation(r *Report) ViolationKind {
	for k := range r.Witnesses {
		return k
	}
	return ViolationNone
}
