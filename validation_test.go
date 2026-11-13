package netsec

import (
	"errors"
	"reflect"
	"testing"
)

const testCodeLen = 56

type fixture struct {
	module   Module
	metadata Metadata
	registry *PersonalityRegistry
	digest   ModuleDigest
}

func newFixture() *fixture {
	code := make([]byte, testCodeLen)
	landing := map[uint64]bool{
		4: true, 8: true, 12: true, 16: true, 20: true,
		32: true, 40: true, 44: true,
	}
	instrs := make([]Instruction, 0, testCodeLen/4)
	for start := uint64(0); start < 48; start += 4 {
		instrs = append(instrs, Instruction{
			Start:      start,
			End:        start + 4,
			LandingPad: landing[start],
		})
	}
	// Instructions after the final function are legal decoder output but are
	// not part of any function and therefore can never be protected.
	for start := uint64(48); start < testCodeLen; start += 4 {
		instrs = append(instrs, Instruction{Start: start, End: start + 4})
	}
	module := Module{
		Code:         code,
		Instructions: instrs,
		Functions: []Function{
			{
				ID:             1,
				Start:          0,
				End:            24,
				TypeIDs:        []TypeID{1, 2},
				EntryPrefixEnd: 4,
			},
			{
				ID:             2,
				Start:          24,
				End:            48,
				TypeIDs:        []TypeID{2, 3},
				EntryPrefixEnd: 28,
			},
		},
	}
	metadata := Metadata{
		Entries: []ExceptionEntry{
			{
				FunctionID:    1,
				Start:         4,
				End:           20,
				LandingPad:    20,
				ActionStart:   0,
				PersonalityID: "p0",
			},
			{
				FunctionID:    1,
				Start:         8,
				End:           16,
				LandingPad:    4,
				ActionStart:   2,
				PersonalityID: "p0",
			},
			{
				FunctionID:    1,
				Start:         8,
				End:           12,
				LandingPad:    16,
				ActionStart:   2,
				PersonalityID: "p0",
			},
			{
				FunctionID:    2,
				Start:         28,
				End:           44,
				LandingPad:    44,
				ActionStart:   2,
				PersonalityID: "p1",
			},
		},
		Actions: []ActionNode{
			{TypeID: 1, Next: 1},
			{TypeID: 2, Next: NoAction},
			{TypeID: 2, Next: 3},
			{TypeID: 2, Next: NoAction},
		},
	}
	f := &fixture{module: module, metadata: metadata}
	f.digest = ComputeModuleDigest(module)
	f.registry = registryFor(f.digest, "p0", "p1")
	return f
}

func registryFor(digest ModuleDigest, ids ...PersonalityID) *PersonalityRegistry {
	r := NewPersonalityRegistry()
	for _, id := range ids {
		if err := r.Register(digest, id); err != nil {
			panic(err)
		}
	}
	return r
}

func compileFixture(f *fixture, limits ...Limits) (*Table, error) {
	return Compile(f.module, f.metadata, f.registry, limits...)
}

func expectFailure(t *testing.T, kind ErrorKind, moduleMut func(*Module), metadataMut func(*Metadata), limits ...Limits) {
	t.Helper()
	f := newFixture()
	if moduleMut != nil {
		moduleMut(&f.module)
		// Decoder metadata is part of the module digest.
		f.digest = ComputeModuleDigest(f.module)
		f.registry = registryFor(f.digest, "p0", "p1")
	}
	if metadataMut != nil {
		metadataMut(&f.metadata)
	}
	table, err := compileFixture(f, limits...)
	if err == nil {
		t.Fatalf("expected %s failure, compilation succeeded", kind)
	}
	if table != nil {
		t.Fatalf("failure published a handler table: %v", err)
	}
	if !errors.Is(err, kind) {
		t.Fatalf("expected %s, got %v", kind, err)
	}
}

func TestAddressByAddressReference(t *testing.T) {
	f := newFixture()
	table, err := compileFixture(f)
	if err != nil {
		t.Fatalf("fixture did not compile: %v", err)
	}

	for pc := uint64(0); pc < testCodeLen+16; pc++ {
		for _, typ := range []TypeID{0, 1, 2, 3, 99} {
			want := referenceResolve(f, pc, typ)
			got := table.Resolve(pc, typ)
			if got.Unwind != (want.entry < 0) {
				t.Fatalf("pc=%d type=%d unwind=%v, want %v", pc, typ, got.Unwind, want.entry < 0)
			}
			if want.entry < 0 {
				if got.LandingPad != 0 || len(got.Actions) != 0 ||
					got.EntryIndex != -1 || got.MatchedAction != -1 {
					t.Fatalf("pc=%d type=%d unmatched result published handler: %+v", pc, typ, got)
				}
				continue
			}
			if got.LandingPad != want.landing {
				t.Fatalf("pc=%d type=%d landing=%d, want %d", pc, typ, got.LandingPad, want.landing)
			}
			if got.EntryIndex != want.entry {
				t.Fatalf("pc=%d type=%d entry=%d, want %d", pc, typ, got.EntryIndex, want.entry)
			}
			if got.MatchedAction != want.matched {
				t.Fatalf("pc=%d type=%d matched=%d, want %d", pc, typ, got.MatchedAction, want.matched)
			}
			if got.Personality.ID != want.personality {
				t.Fatalf("pc=%d type=%d personality=%q, want %q", pc, typ, got.Personality.ID, want.personality)
			}
			if len(got.Actions) != len(want.chain) {
				t.Fatalf("pc=%d type=%d action trail length=%d, want %d", pc, typ, len(got.Actions), len(want.chain))
			}
			for i, ai := range want.chain {
				node := f.metadata.Actions[ai]
				if got.Actions[i] != (ActionEvidence{Index: ai, TypeID: node.TypeID, Cleanup: node.Cleanup}) {
					t.Fatalf("pc=%d type=%d action[%d]=%+v, want index %d", pc, typ, i, got.Actions[i], ai)
				}
			}
		}
	}
}

type refResult struct {
	entry       int
	landing     uint64
	chain       []int
	matched     int
	personality PersonalityID
}

// referenceResolve deliberately uses a different algorithm from Resolve: an
// exhaustive scan over every function and entry for each address.
func referenceResolve(f *fixture, pc uint64, typ TypeID) refResult {
	best := refResult{entry: -1, matched: -1}
	var bestSpan uint64
	for ei, e := range f.metadata.Entries {
		var fn *Function
		for i := range f.module.Functions {
			if f.module.Functions[i].ID == e.FunctionID {
				fn = &f.module.Functions[i]
				break
			}
		}
		if fn == nil || pc < fn.Start || pc >= fn.End {
			continue
		}
		if pc < e.Start || pc >= e.End {
			continue
		}
		chain := rawChain(f.metadata.Actions, e.ActionStart)
		match := -1
		for i, ai := range chain {
			if f.metadata.Actions[ai].TypeID == typ {
				match = i
				break
			}
		}
		if match < 0 {
			continue
		}
		span := e.End - e.Start
		if best.entry < 0 || span < bestSpan || (span == bestSpan && ei < best.entry) {
			best = refResult{
				entry:       ei,
				landing:     e.LandingPad,
				chain:       chain,
				matched:     match,
				personality: e.PersonalityID,
			}
			bestSpan = span
		}
	}
	return best
}

func rawChain(actions []ActionNode, start int) []int {
	if start == NoAction {
		return nil
	}
	var chain []int
	for idx := start; idx != NoAction; idx = actions[idx].Next {
		chain = append(chain, idx)
	}
	return chain
}

func TestLegalNestingInnerWins(t *testing.T) {
	f := newFixture()
	table, err := compileFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		pc      uint64
		typ     TypeID
		entry   int
		landing uint64
	}{
		{6, 1, 0, 20},
		{10, 2, 2, 16},
		{14, 2, 1, 4},
		{18, 2, 0, 20},
		{30, 2, 3, 44},
	}
	for _, tc := range cases {
		r := table.Query(tc.pc, tc.typ)
		if r.Unwind || r.EntryIndex != tc.entry || r.LandingPad != tc.landing {
			t.Fatalf("pc=%d type=%d: got %+v", tc.pc, tc.typ, r)
		}
	}
}

func TestLandingPadEveryAddress(t *testing.T) {
	for landing := uint64(0); landing < testCodeLen+8; landing++ {
		f := newFixture()
		f.metadata.Entries[0].LandingPad = landing
		table, err := compileFixture(f)
		if landing == 20 {
			if err != nil || table == nil {
				t.Fatalf("legal landing pad 20 rejected: %v", err)
			}
			continue
		}
		if err == nil || table != nil {
			t.Fatalf("landing pad %d accepted", landing)
		}
	}
}

func TestJumpIntoInstructionMiddleRejected(t *testing.T) {
	f := newFixture()
	for _, ins := range f.module.Instructions {
		for delta := uint64(1); delta < ins.End-ins.Start; delta++ {
			landing := ins.Start + delta
			f := newFixture()
			f.metadata.Entries[0].LandingPad = landing
			table, err := compileFixture(f)
			if err == nil || table != nil {
				t.Fatalf("mid-instruction landing pad %d published a handler", landing)
			}
		}
	}
}

func TestProtectionEndpointsAreInstructionBoundaries(t *testing.T) {
	expectFailure(t, ErrEntryBoundary, nil, func(m *Metadata) {
		m.Entries[0].Start = 5
	})
	expectFailure(t, ErrEntryBoundary, nil, func(m *Metadata) {
		m.Entries[0].End = 6
	})
	expectFailure(t, ErrEntryBoundary, nil, func(m *Metadata) {
		m.Entries[0].End = 19
	})
}

func TestProtectedIntervalConstraints(t *testing.T) {
	expectFailure(t, ErrEntryRange, nil, func(m *Metadata) {
		m.Entries[0].Start = 4
		m.Entries[0].End = 4
	})
	expectFailure(t, ErrEntryRange, nil, func(m *Metadata) {
		m.Entries[3].Start = 20
	})
	expectFailure(t, ErrEntryRange, nil, func(m *Metadata) {
		m.Entries[0].End = 28 // crosses from function 1 into function 2
	})
	expectFailure(t, ErrEntryFunction, nil, func(m *Metadata) {
		m.Entries[0].FunctionID = 99
	})
}

func TestLandingPadConstraints(t *testing.T) {
	expectFailure(t, ErrLandingPadRange, nil, func(m *Metadata) {
		m.Entries[0].LandingPad = 24
	})
	expectFailure(t, ErrLandingPadInstruction, nil, func(m *Metadata) {
		m.Entries[0].LandingPad = 0
	})
	expectFailure(t, ErrLandingPadInstruction, nil, func(m *Metadata) {
		m.Entries[0].LandingPad = 10
	})
	expectFailure(t, ErrLandingPadProtected, nil, func(m *Metadata) {
		m.Entries[0].LandingPad = 8
	})
	expectFailure(t, ErrLandingPadEntryPrefix, func(mod *Module) {
		mod.Instructions[0].LandingPad = true
	}, func(m *Metadata) {
		m.Entries[0].LandingPad = 0
	})
}

func TestCrossingProtectedEntriesRejected(t *testing.T) {
	expectFailure(t, ErrEntryOverlap, func(mod *Module) {
		// Make address 4 a registered landing instruction so both crossing
		// entries have otherwise legal landing pads.
		mod.Instructions[1].LandingPad = true
	}, func(m *Metadata) {
		m.Entries[1].Start = 4
		m.Entries[1].End = 12
		m.Entries[1].LandingPad = 16
		m.Entries[2].Start = 8
		m.Entries[2].End = 20
		m.Entries[2].LandingPad = 4
	})
}

func TestActionChains(t *testing.T) {
	expectFailure(t, ErrActionCycle, nil, func(m *Metadata) {
		m.Actions[0].Next = 0
	})
	expectFailure(t, ErrActionCycle, nil, func(m *Metadata) {
		m.Actions[3].Next = 1
	})
	expectFailure(t, ErrActionIndex, nil, func(m *Metadata) {
		m.Actions[0].Next = len(m.Actions)
	})
	expectFailure(t, ErrActionIndex, nil, func(m *Metadata) {
		m.Entries[0].ActionStart = -2
	})
	expectFailure(t, ErrActionType, nil, func(m *Metadata) {
		m.Actions[2].TypeID = 99
	})
	expectFailure(t, ErrActionBudget, nil, func(m *Metadata) {
		m.Actions = append(m.Actions, ActionNode{TypeID: 2, Next: NoAction})
		m.Actions[3].Next = 4
	}, Limits{MaxActionChain: 2})
}

func TestLimitsAndBudgets(t *testing.T) {
	expectFailure(t, ErrEntryBudget, nil, nil, Limits{MaxEntries: 1})
	expectFailure(t, ErrActionNodeBudget, nil, nil, Limits{MaxActionNodes: 3})
	expectFailure(t, ErrLimits, nil, nil, Limits{MaxActionChain: -1})
	expectFailure(t, ErrLimits, nil, nil, Limits{MaxEntries: 1}, Limits{MaxEntries: 2})
}

func TestPersonalityMustResolveForModuleDigest(t *testing.T) {
	expectFailure(t, ErrPersonality, nil, func(m *Metadata) {
		m.Entries[0].PersonalityID = "forged"
	})

	f := newFixture()
	otherModule := f.module
	otherModule.Code = append([]byte(nil), f.module.Code...)
	otherModule.Code[0] ^= 0xff
	otherRegistry := registryFor(ComputeModuleDigest(otherModule), "p0", "p1")
	table, err := Compile(f.module, f.metadata, otherRegistry)
	if err == nil || table != nil {
		t.Fatalf("personality bound to another digest accepted")
	}
	if !errors.Is(err, ErrPersonality) {
		t.Fatalf("want personality failure, got %v", err)
	}
}

func TestTrustedDecoderInvariants(t *testing.T) {
	expectFailure(t, ErrInstructionOverlap, func(m *Module) {
		m.Instructions[1].Start = 2
	}, nil)
	expectFailure(t, ErrInstructionRange, func(m *Module) {
		m.Instructions[len(m.Instructions)-1].End = testCodeLen + 1
	}, nil)
	expectFailure(t, ErrFunctionOverlap, func(m *Module) {
		m.Functions[1].Start = 20
	}, nil)
	expectFailure(t, ErrFunctionRange, func(m *Module) {
		m.Functions[1].End = 24
	}, nil)
	expectFailure(t, ErrFunctionID, func(m *Module) {
		m.Functions[1].ID = m.Functions[0].ID
	}, nil)
	expectFailure(t, ErrFunctionCoverage, func(m *Module) {
		m.Instructions[2].Start = 10
	}, nil)
	expectFailure(t, ErrFunctionCoverage, func(m *Module) {
		m.Functions[0].End = 22
	}, nil)
	expectFailure(t, ErrFunctionPrefix, func(m *Module) {
		m.Functions[0].EntryPrefixEnd = 7
	}, nil)
}

func TestFailurePublishesNoHandler(t *testing.T) {
	// Every negative path above also asserts table == nil, but repeat the core
	// property with a table that would otherwise have four resolving entries.
	f := newFixture()
	f.metadata.Entries[1].LandingPad = 10
	table, err := compileFixture(f)
	if err == nil {
		t.Fatal("expected rejection")
	}
	if table != nil {
		t.Fatal("partial table escaped validation")
	}
}

func TestCompiledTableSnapshotsInputs(t *testing.T) {
	f := newFixture()
	table, err := compileFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	before := table.Code()
	want := table.Resolve(10, 2)

	f.module.Code[0] ^= 0xff
	f.module.Instructions[0].LandingPad = false
	f.metadata.Entries[0].LandingPad = 4
	f.metadata.Actions[0].TypeID = 99

	if got := table.Code(); !equalBytes(got, before) {
		t.Fatal("published code changed with caller-owned bytes")
	}
	if got := table.Resolve(10, 2); !reflect.DeepEqual(got, want) {
		t.Fatalf("published table changed with caller-owned metadata: %+v != %+v", got, want)
	}
	entries := table.Entries()
	entries[0].LandingPad = 0
	if e, _ := table.Entry(0); e.LandingPad != 20 {
		t.Fatal("Entry exposed mutable table storage")
	}
}

func TestMetadataTypesCarryNoFunctionPointers(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(ExceptionEntry{}),
		reflect.TypeOf(ActionNode{}),
		reflect.TypeOf(Metadata{}),
		reflect.TypeOf(Personality{}),
	} {
		assertNoFuncFields(t, typ, typ.String())
	}
}

func assertNoFuncFields(t *testing.T, typ reflect.Type, path string) {
	t.Helper()
	switch typ.Kind() {
	case reflect.Func:
		t.Fatalf("%s carries a function pointer", path)
	case reflect.Pointer, reflect.Slice, reflect.Array:
		assertNoFuncFields(t, typ.Elem(), path+"[]")
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			assertNoFuncFields(t, typ.Field(i).Type, path+"."+typ.Field(i).Name)
		}
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
