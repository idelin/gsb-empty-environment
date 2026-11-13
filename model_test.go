package netsec

import (
	"reflect"
	"testing"
)

const (
	pubHigh6   uint8 = 0x02
	pub2High6  uint8 = 0x03
	privHigh6  uint8 = 0x20
	priv2High6 uint8 = 0x21
)

func addr(high6, low6 uint8) Addr {
	return Addr(uint16(high6)<<6 | uint16(low6))
}

func mustRun(t *testing.T, program Program, input Input) *RunResult {
	t.Helper()
	result, err := Run(program, input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

func probeGroups(result *RunResult) (arch, spec []uint8) {
	return result.ArchProbes, result.SpecProbes
}

func TestNoAliasingNeitherForwardsNorLeaks(t *testing.T) {
	program := Program{
		Store(FixedAddr(addr(privHigh6, 3)), SecretValue()),
		Load(SlotAddr(0, pubHigh6, 3), 0),
		Probe(0),
		Barrier(),
	}
	public := PublicInput{
		Low6:   [2]uint8{5, 0},
		Memory: map[Addr]Value{addr(pubHigh6, 5): 0x2a},
	}
	result := mustRun(t, program, Input{Secret: 0x11, PublicInput: public})

	if len(result.Loads) != 1 {
		t.Fatalf("got %d loads, want 1", len(result.Loads))
	}
	load := result.Loads[0]
	if load.Forwarded || load.SelectedStore != -1 {
		t.Fatalf("load forwarded from %d despite no low-six-bit match", load.SelectedStore)
	}
	if load.ResolveCycle != load.IssueCycle {
		t.Fatalf("memory load resolved at %d, want issue cycle %d", load.ResolveCycle, load.IssueCycle)
	}
	if load.RolledBack {
		t.Fatal("memory load was rolled back")
	}
	if got, want := result.ArchProbes, []uint8{0x2a & 0x0f}; !reflect.DeepEqual(got, want) {
		t.Fatalf("arch probes = %v, want %v", got, want)
	}
	if got, want := result.SpecProbes, []uint8{0x2a & 0x0f}; !reflect.DeepEqual(got, want) {
		t.Fatalf("spec probes = %v, want %v", got, want)
	}

	search, err := Search(SearchConfig{
		Program:      program,
		PublicInputs: []PublicInput{public},
		Secrets:      []Value{0x11, 0x12},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if search.Status != StatusNoLeak || !search.Complete {
		t.Fatalf("search = %v, complete=%v, want no-leak/complete", search.Status, search.Complete)
	}
}

func TestPartialAliasForwardsSecretBeforeRollback(t *testing.T) {
	program := Program{
		Store(FixedAddr(addr(privHigh6, 4)), SecretValue()),
		Load(SlotAddr(0, pubHigh6, 4), 0),
		Probe(0),
		Barrier(),
		Probe(0),
	}
	public := PublicInput{
		Low6:   [2]uint8{4, 0},
		Memory: map[Addr]Value{addr(pubHigh6, 4): 0x2a},
	}

	result := mustRun(t, program, Input{Secret: 0x11, PublicInput: public})
	if len(result.Loads) != 1 {
		t.Fatalf("got %d loads, want 1", len(result.Loads))
	}
	load := result.Loads[0]
	if !load.Forwarded || !load.Mismatch {
		t.Fatalf("load forwarded=%v mismatch=%v, want true/true", load.Forwarded, load.Mismatch)
	}
	if load.SelectedStore != 0 {
		t.Fatalf("selected store = %d, want 0", load.SelectedStore)
	}
	if load.ArchStore != -1 {
		t.Fatalf("architectural store = %d, want memory", load.ArchStore)
	}
	if load.SpecValue != 0x11 || load.ArchValue != 0x2a {
		t.Fatalf("spec/arch values = %#x/%#x, want 0x11/0x2a", load.SpecValue, load.ArchValue)
	}
	if load.ResolveCycle != 3 || !load.ResolvedAtBarrier {
		t.Fatalf("resolve cycle=%d barrier=%v, want 3/true", load.ResolveCycle, load.ResolvedAtBarrier)
	}
	if !load.RolledBack || load.RegBeforeRollback != 0x11 || load.RegAfterRollback != 0x2a {
		t.Fatalf("rollback before/after=%#x/%#x rolled=%v, want 0x11/0x2a true",
			load.RegBeforeRollback, load.RegAfterRollback, load.RolledBack)
	}
	if got, want := result.ArchProbes, []uint8{0x0a, 0x0a}; !reflect.DeepEqual(got, want) {
		t.Fatalf("arch probes = %v, want %v", got, want)
	}
	if got, want := result.SpecProbes, []uint8{0x01, 0x0a}; !reflect.DeepEqual(got, want) {
		t.Fatalf("spec probes = %v, want %v", got, want)
	}

	search, err := Search(SearchConfig{
		Program:      program,
		PublicInputs: []PublicInput{public},
		Secrets:      []Value{0x11, 0x12},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if search.Status != StatusSpeculativeLeak {
		t.Fatalf("search status = %v, want %v", search.Status, StatusSpeculativeLeak)
	}
	if len(search.SpecLeaks) == 0 {
		t.Fatal("expected a speculative leak pair")
	}
}

func TestTrueSameAddressForwarding(t *testing.T) {
	program := Program{
		Store(FixedAddr(addr(pubHigh6, 4)), SecretValue()),
		Load(FixedAddr(addr(pubHigh6, 4)), 0),
		Probe(0),
	}
	public := PublicInput{Low6: [2]uint8{4, 0}}

	result := mustRun(t, program, Input{Secret: 0x2b, PublicInput: public})
	if len(result.Loads) != 1 {
		t.Fatalf("got %d loads, want 1", len(result.Loads))
	}
	load := result.Loads[0]
	if !load.Forwarded || load.Mismatch || load.SelectedStore != 0 || load.ArchStore != 0 {
		t.Fatalf("forwarded=%v mismatch=%v selected=%d arch=%d, want true/false/0/0",
			load.Forwarded, load.Mismatch, load.SelectedStore, load.ArchStore)
	}
	if load.SpecValue != 0x2b || load.ArchValue != 0x2b {
		t.Fatalf("spec/arch values = %#x/%#x, want 0x2b/0x2b", load.SpecValue, load.ArchValue)
	}
	if load.ResolveCycle != 5 || load.RolledBack {
		t.Fatalf("resolve cycle=%d rolled=%v, want 5/false", load.ResolveCycle, load.RolledBack)
	}
	if got, want := result.SpecProbes, result.ArchProbes; !reflect.DeepEqual(got, want) {
		t.Fatalf("spec probes = %v, arch probes = %v", got, want)
	}

	// Secrets that differ only above the observable four bits are equivalent.
	search, err := Search(SearchConfig{
		Program:      program,
		PublicInputs: []PublicInput{public},
		Secrets:      []Value{0x20, 0x30},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if search.Status != StatusNoLeak {
		t.Fatalf("search status = %v, want no-leak", search.Status)
	}
}

func TestMostRecentStoreWins(t *testing.T) {
	t.Run("same full address", func(t *testing.T) {
		program := Program{
			Store(FixedAddr(addr(pubHigh6, 4)), ConstValue(0x2a)),
			Store(FixedAddr(addr(pubHigh6, 4)), ConstValue(0x2b)),
			Load(FixedAddr(addr(pubHigh6, 4)), 0),
			Probe(0),
		}
		result := mustRun(t, program, Input{Secret: 0x11, PublicInput: PublicInput{Low6: [2]uint8{4, 0}}})
		load := result.Loads[0]
		if load.SelectedStore != 1 || load.ArchStore != 1 {
			t.Fatalf("selected/arch stores = %d/%d, want 1/1", load.SelectedStore, load.ArchStore)
		}
		if load.SpecValue != 0x2b || load.Mismatch || load.RolledBack {
			t.Fatalf("spec=%#x mismatch=%v rolled=%v, want 0x2b/false/false", load.SpecValue, load.Mismatch, load.RolledBack)
		}
	})

	t.Run("partial alias shadows older full match", func(t *testing.T) {
		program := Program{
			Store(FixedAddr(addr(pubHigh6, 4)), ConstValue(0x2a)),
			Store(FixedAddr(addr(privHigh6, 4)), SecretValue()),
			Load(SlotAddr(0, pubHigh6, 4), 0),
			Probe(0),
		}
		public := PublicInput{
			Low6:   [2]uint8{4, 0},
			Memory: map[Addr]Value{addr(pubHigh6, 4): 0x2a},
		}
		result := mustRun(t, program, Input{Secret: 0x11, PublicInput: public})
		load := result.Loads[0]
		if load.SelectedStore != 1 || load.ArchStore != 0 {
			t.Fatalf("selected/arch stores = %d/%d, want 1/0", load.SelectedStore, load.ArchStore)
		}
		if !load.Mismatch || load.SpecValue != 0x11 || load.ArchValue != 0x2a {
			t.Fatalf("mismatch=%v spec=%#x arch=%#x, want true/0x11/0x2a", load.Mismatch, load.SpecValue, load.ArchValue)
		}
		if load.ResolveCycle != 6 {
			t.Fatalf("resolve cycle = %d, want 6", load.ResolveCycle)
		}
		if !load.RolledBack || load.RegAfterRollback != 0x2a {
			t.Fatalf("rolled=%v after=%#x, want true/0x2a", load.RolledBack, load.RegAfterRollback)
		}

		search, err := Search(SearchConfig{
			Program:      program,
			PublicInputs: []PublicInput{public},
			Secrets:      []Value{0x11, 0x12},
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if search.Status != StatusSpeculativeLeak {
			t.Fatalf("search status = %v, want speculative leak", search.Status)
		}
	})
}

func TestBarrierRepairsBeforeFollowingProbe(t *testing.T) {
	base := Program{
		Store(FixedAddr(addr(privHigh6, 4)), SecretValue()),
		Load(SlotAddr(0, pubHigh6, 4), 0),
		Probe(0),
	}
	public := PublicInput{
		Low6:   [2]uint8{4, 0},
		Memory: map[Addr]Value{addr(pubHigh6, 4): 0x2a},
	}

	withBarrier := append(append(Program{}, base...), Barrier(), Probe(0))
	result := mustRun(t, withBarrier, Input{Secret: 0x11, PublicInput: public})
	load := result.Loads[0]
	if load.ResolveCycle != 3 || !load.ResolvedAtBarrier {
		t.Fatalf("resolve cycle=%d barrier=%v, want 3/true", load.ResolveCycle, load.ResolvedAtBarrier)
	}
	if got, want := result.SpecProbes, []uint8{0x01, 0x0a}; !reflect.DeepEqual(got, want) {
		t.Fatalf("spec probes = %v, want %v", got, want)
	}

	withoutBarrier := append(append(Program{}, base...), Probe(0))
	result = mustRun(t, withoutBarrier, Input{Secret: 0x11, PublicInput: public})
	load = result.Loads[0]
	if load.ResolveCycle != 5 || load.ResolvedAtBarrier {
		t.Fatalf("unbarriered resolve cycle=%d barrier=%v, want 5/false", load.ResolveCycle, load.ResolvedAtBarrier)
	}
	if got, want := result.SpecProbes, []uint8{0x01, 0x01}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unbarriered spec probes = %v, want %v", got, want)
	}
}

func TestTwoPendingLoadsResolveIndependently(t *testing.T) {
	program := Program{
		Store(FixedAddr(addr(privHigh6, 5)), SecretValue()),
		Load(SlotAddr(0, pubHigh6, 5), 0),
		Store(FixedAddr(addr(priv2High6, 6)), ConstValue(0x33)),
		Load(SlotAddr(1, pub2High6, 6), 1),
		Probe(0),
		Probe(1),
		Barrier(),
		Probe(0),
		Probe(1),
	}
	public := PublicInput{
		Low6: [2]uint8{5, 6},
		Memory: map[Addr]Value{
			addr(pubHigh6, 5):  0x11,
			addr(pub2High6, 6): 0x22,
		},
	}
	result := mustRun(t, program, Input{Secret: 0x41, PublicInput: public})

	if len(result.Loads) != 2 {
		t.Fatalf("got %d loads, want 2", len(result.Loads))
	}
	if got := len(result.Cycles[4].Pending); got != 2 {
		t.Fatalf("cycle 4 has %d pending loads, want 2", got)
	}
	if got := len(result.Cycles[5].Pending); got != 1 {
		t.Fatalf("cycle 5 has %d pending loads, want 1", got)
	}

	first, second := result.Loads[0], result.Loads[1]
	if first.ResolveCycle != 5 || first.ResolvedAtBarrier {
		t.Fatalf("first resolve cycle=%d barrier=%v, want 5/false", first.ResolveCycle, first.ResolvedAtBarrier)
	}
	if second.ResolveCycle != 6 || !second.ResolvedAtBarrier {
		t.Fatalf("second resolve cycle=%d barrier=%v, want 6/true", second.ResolveCycle, second.ResolvedAtBarrier)
	}
	if !first.RolledBack || first.RegAfterRollback != 0x11 {
		t.Fatalf("first rolled=%v after=%#x, want true/0x11", first.RolledBack, first.RegAfterRollback)
	}
	if !second.RolledBack || second.RegAfterRollback != 0x22 {
		t.Fatalf("second rolled=%v after=%#x, want true/0x22", second.RolledBack, second.RegAfterRollback)
	}
	if got, want := result.SpecProbes, []uint8{0x01, 0x03, 0x01, 0x02}; !reflect.DeepEqual(got, want) {
		t.Fatalf("spec probes = %v, want %v", got, want)
	}
	if got, want := result.ArchProbes, []uint8{0x01, 0x02, 0x01, 0x02}; !reflect.DeepEqual(got, want) {
		t.Fatalf("arch probes = %v, want %v", got, want)
	}

	search, err := Search(SearchConfig{
		Program:      program,
		PublicInputs: []PublicInput{public},
		Secrets:      []Value{0x41, 0x42},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if search.Status != StatusSpeculativeLeak {
		t.Fatalf("search status = %v, want speculative leak", search.Status)
	}
}

func TestBarrierResolvesTwoPendingLoads(t *testing.T) {
	program := Program{
		Store(FixedAddr(addr(privHigh6, 5)), SecretValue()),
		Load(SlotAddr(0, pubHigh6, 5), 0),
		Store(FixedAddr(addr(priv2High6, 6)), ConstValue(0x33)),
		Load(SlotAddr(1, pub2High6, 6), 1),
		Barrier(),
		Probe(0),
		Probe(1),
	}
	public := PublicInput{
		Low6: [2]uint8{5, 6},
		Memory: map[Addr]Value{
			addr(pubHigh6, 5):  0x11,
			addr(pub2High6, 6): 0x22,
		},
	}
	result := mustRun(t, program, Input{Secret: 0x41, PublicInput: public})
	if got := len(result.Cycles[4].Pending); got != 0 {
		t.Fatalf("barrier left %d pending loads", got)
	}
	for i, load := range result.Loads {
		if load.ResolveCycle != 4 || !load.ResolvedAtBarrier {
			t.Fatalf("load %d resolve cycle=%d barrier=%v, want 4/true", i, load.ResolveCycle, load.ResolvedAtBarrier)
		}
	}
	if got, want := result.SpecProbes, []uint8{0x01, 0x02}; !reflect.DeepEqual(got, want) {
		t.Fatalf("spec probes = %v, want %v", got, want)
	}
}

func TestRollbackReplaysLaterXor(t *testing.T) {
	program := Program{
		Store(FixedAddr(addr(privHigh6, 4)), SecretValue()),
		Load(SlotAddr(0, pubHigh6, 4), 0),
		Xor(0, 0x03),
		Probe(0),
		Barrier(),
		Probe(0),
	}
	public := PublicInput{
		Low6:   [2]uint8{4, 0},
		Memory: map[Addr]Value{addr(pubHigh6, 4): 0x2a},
	}
	result := mustRun(t, program, Input{Secret: 0x11, PublicInput: public})
	load := result.Loads[0]
	if !load.RolledBack {
		t.Fatal("load was not rolled back")
	}
	if load.RegBeforeRollback != 0x12 || load.RegAfterRollback != 0x29 {
		t.Fatalf("before/after = %#x/%#x, want 0x12/0x29", load.RegBeforeRollback, load.RegAfterRollback)
	}
	if got, want := result.ArchProbes, []uint8{0x09, 0x09}; !reflect.DeepEqual(got, want) {
		t.Fatalf("arch probes = %v, want %v", got, want)
	}
	if got, want := result.SpecProbes, []uint8{0x02, 0x09}; !reflect.DeepEqual(got, want) {
		t.Fatalf("spec probes = %v, want %v", got, want)
	}
}

func TestArchitecturalLeakIsReportedSeparately(t *testing.T) {
	program := Program{
		Store(FixedAddr(addr(privHigh6, 4)), SecretValue()),
		Load(FixedAddr(addr(privHigh6, 4)), 0),
		Probe(0),
	}
	search, err := Search(SearchConfig{
		Program:      program,
		PublicInputs: []PublicInput{{Low6: [2]uint8{4, 0}}},
		Secrets:      []Value{0x11, 0x12},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if search.Status != StatusArchitecturalLeak {
		t.Fatalf("search status = %v, want architectural leak", search.Status)
	}
	if len(search.ArchLeaks) == 0 {
		t.Fatal("expected an architectural leak pair")
	}
	if len(search.SpecLeaks) != 0 {
		t.Fatalf("got %d speculative leak pairs, want none", len(search.SpecLeaks))
	}
}

func TestFinalStateEqualityDoesNotMaskCycleLeak(t *testing.T) {
	program := Program{
		Store(FixedAddr(addr(privHigh6, 4)), SecretValue()),
		Load(SlotAddr(0, pubHigh6, 4), 0),
		Probe(0),
		Barrier(),
		Probe(0),
	}
	public := PublicInput{
		Low6:   [2]uint8{4, 0},
		Memory: map[Addr]Value{addr(pubHigh6, 4): 0x2a},
	}
	search, err := Search(SearchConfig{
		Program:      program,
		PublicInputs: []PublicInput{public},
		Secrets:      []Value{0x11, 0x12},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if search.Status != StatusSpeculativeLeak {
		t.Fatalf("search status = %v, want speculative leak", search.Status)
	}
	pair := search.SpecLeaks[0]

	if pair.RunA.FinalArchRegs != pair.RunB.FinalArchRegs {
		t.Fatal("runs have different final architectural registers")
	}
	if pair.RunA.FinalSpecRegs != pair.RunB.FinalSpecRegs {
		t.Fatal("runs have different final speculative registers")
	}
	if !reflect.DeepEqual(pair.RunA.FinalArchMemory, pair.RunA.FinalSpecMemory) ||
		!reflect.DeepEqual(pair.RunB.FinalArchMemory, pair.RunB.FinalSpecMemory) {
		t.Fatal("final architectural and speculative memories differ")
	}
	if reflect.DeepEqual(pair.RunA.SpecProbes, pair.RunB.SpecProbes) {
		t.Fatal("speculative probe traces unexpectedly agree")
	}

	foundCycleDifference := false
	for _, cycle := range pair.RunA.Cycles {
		if cycle.Probe != nil && cycle.Probe.ArchGroup != cycle.Probe.SpecGroup {
			foundCycleDifference = true
			break
		}
	}
	if !foundCycleDifference {
		t.Fatal("no cycle shows the architectural/speculative probe difference")
	}
}

func TestSearchBudgetExhaustionIsUnknown(t *testing.T) {
	program := Program{
		Store(FixedAddr(addr(privHigh6, 4)), SecretValue()),
		Load(SlotAddr(0, pubHigh6, 4), 0),
		Probe(0),
	}
	search, err := Search(SearchConfig{
		Program: program,
		PublicInputs: []PublicInput{
			{Low6: [2]uint8{4, 0}},
			{Low6: [2]uint8{5, 0}},
		},
		Secrets: []Value{0x11, 0x12},
		Budget:  1,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if search.Status != StatusUnknown {
		t.Fatalf("status = %v, want unknown", search.Status)
	}
	if search.Complete {
		t.Fatal("search marked complete despite exhausted budget")
	}
	if search.Examined != 1 {
		t.Fatalf("examined %d runs, want 1", search.Examined)
	}
}

func TestStoreBufferIsBoundedAndCommits(t *testing.T) {
	program := Program{
		Store(FixedAddr(addr(pubHigh6, 0)), ConstValue(1)),
		Store(FixedAddr(addr(pubHigh6, 1)), ConstValue(2)),
		Store(FixedAddr(addr(pubHigh6, 2)), ConstValue(3)),
		Store(FixedAddr(addr(pubHigh6, 3)), ConstValue(4)),
		Store(FixedAddr(addr(pubHigh6, 4)), ConstValue(5)),
	}
	result := mustRun(t, program, Input{PublicInput: PublicInput{Low6: [2]uint8{0, 0}}})
	for _, cycle := range result.Cycles {
		if len(cycle.StoreBuffer) > MaxStoreBuffer {
			t.Fatalf("cycle %d buffered %d stores, max is %d", cycle.Cycle, len(cycle.StoreBuffer), MaxStoreBuffer)
		}
	}
	if got := result.Cycles[0].StoreBuffer[0].Committed; got {
		t.Fatal("fresh store was already committed")
	}
	if got := result.Cycles[1].StoreBuffer[0].Committed; !got {
		t.Fatal("store did not commit on the following cycle")
	}
	for pc := 0; pc < 5; pc++ {
		want := Value(pc + 1)
		if got := result.FinalSpecMemory[addr(pubHigh6, uint8(pc))]; got != want {
			t.Fatalf("address %d = %#x, want %#x", pc, got, want)
		}
	}
}

func TestStoreRetirementPreservesSameAddressOrder(t *testing.T) {
	target := addr(pubHigh6, 9)
	program := Program{
		Store(FixedAddr(target), ConstValue(1)),
		Store(FixedAddr(target), ConstValue(2)),
		Store(FixedAddr(target), ConstValue(3)),
		Store(FixedAddr(target), ConstValue(4)),
		Store(FixedAddr(target), ConstValue(5)),
	}
	result := mustRun(t, program, Input{PublicInput: PublicInput{Low6: [2]uint8{9, 0}}})
	if got := result.FinalArchMemory[target]; got != 5 {
		t.Fatalf("architectural memory = %d, want 5", got)
	}
	if got := result.FinalSpecMemory[target]; got != 5 {
		t.Fatalf("speculative memory = %d, want 5; retirement reordered stores", got)
	}
}

func TestPublicDomainEnumeratesDeterministically(t *testing.T) {
	domain := PublicDomain{
		Low6: [2][]uint8{{0, 1}, {2}},
		MemoryCells: []MemoryCellDomain{
			{Addr: addr(pubHigh6, 0), Values: []Value{7, 8}},
		},
	}
	inputs, complete, err := domain.Enumerate(100)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if !complete || len(inputs) != 4 {
		t.Fatalf("complete=%v inputs=%d, want true/4", complete, len(inputs))
	}
	want := []PublicInput{
		{Low6: [2]uint8{0, 2}, Memory: map[Addr]Value{addr(pubHigh6, 0): 7}},
		{Low6: [2]uint8{0, 2}, Memory: map[Addr]Value{addr(pubHigh6, 0): 8}},
		{Low6: [2]uint8{1, 2}, Memory: map[Addr]Value{addr(pubHigh6, 0): 7}},
		{Low6: [2]uint8{1, 2}, Memory: map[Addr]Value{addr(pubHigh6, 0): 8}},
	}
	if !reflect.DeepEqual(inputs, want) {
		t.Fatalf("inputs = %#v, want %#v", inputs, want)
	}
}

func TestValidation(t *testing.T) {
	long := make(Program, MaxInstructions+1)
	for i := range long {
		long[i] = Barrier()
	}
	if err := long.Validate(); err == nil {
		t.Fatal("program longer than 16 instructions was accepted")
	}

	badAddr := Program{Store(AddrSpec{High6: 0x40, Slot: NoSlot}, SecretValue())}
	if err := badAddr.Validate(); err == nil {
		t.Fatal("out-of-range address was accepted")
	}

	badSlot := Program{Load(AddrSpec{High6: 1, Slot: 2}, 0)}
	if err := badSlot.Validate(); err == nil {
		t.Fatal("out-of-range address slot was accepted")
	}

	if _, err := Search(SearchConfig{
		Program: Program{Load(FixedAddr(addr(pubHigh6, 0)), 0)},
		Secrets: []Value{1, 2},
	}); err == nil {
		t.Fatal("search without a secret store was accepted")
	}

	if _, err := Search(SearchConfig{
		Program: Program{
			Store(FixedAddr(addr(privHigh6, 0)), SecretValue()),
			Store(FixedAddr(addr(privHigh6, 1)), SecretValue()),
		},
		Secrets: []Value{1, 2},
	}); err == nil {
		t.Fatal("search with two secret stores was accepted")
	}

	if _, err := RunWithConfig(
		Program{Barrier()},
		Input{},
		Config{StoreBufferSize: MaxStoreBuffer + 1},
	); err == nil {
		t.Fatal("oversized store buffer was accepted")
	}
}
