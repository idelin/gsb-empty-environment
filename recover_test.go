package controlflow

import (
	"errors"
	"testing"
)

// synthInstr describes one scripted interpretation for the synthetic decoder.
type synthInstr struct {
	length          int
	successors      []Target
	terminates      bool
	unknownIndirect bool
	err             error
}

// syntheticDecoder is a scripted decoder: addresses are virtual, bytes never
// matter, and the same address always yields the same Result.
type syntheticDecoder struct {
	program map[uint64]synthInstr
	calls   int
}

func (d *syntheticDecoder) Decode(addr uint64) Result {
	d.calls++
	instr, ok := d.program[addr]
	if !ok {
		return Result{Err: ErrShortRead}
	}
	return Result{
		Length:          instr.length,
		Successors:      append([]Target(nil), instr.successors...),
		Terminates:      instr.terminates,
		UnknownIndirect: instr.unknownIndirect,
		Err:             instr.err,
	}
}

func direct(addr uint64) Target   { return Target{Address: addr, Kind: EdgeDirect} }
func indirect(addr uint64) Target { return Target{Address: addr, Kind: EdgeIndirect} }

func newEngine(decoder Decoder) *Engine {
	return &Engine{Decoder: decoder, Base: 0, Size: 64}
}

func assertNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected recovery error: %v", err)
	}
}

// A conditional branch loops back to an earlier address: the repeated visit
// must be merged as provenance instead of being decoded again.
func TestRecoverBackEdge(t *testing.T) {
	decoder := &syntheticDecoder{program: map[uint64]synthInstr{
		0: {length: 2},
		2: {length: 2},
		4: {length: 2, successors: []Target{direct(6), direct(2)}},
		6: {length: 2, terminates: true},
	}}

	report, err := newEngine(decoder).Recover([]uint64{0})
	assertNoErr(t, err)

	if report.Decoded != 4 {
		t.Fatalf("Decoded = %d, want 4 (back edge must not re-decode)", report.Decoded)
	}
	if len(report.BackEdges) != 1 {
		t.Fatalf("BackEdges = %v, want exactly one", report.BackEdges)
	}
	edge := report.BackEdges[0]
	if edge.From != 4 || edge.To != 2 || edge.Kind != EdgeDirect {
		t.Fatalf("back edge = %+v, want 4->2 direct", edge)
	}

	loopHead := report.NodeAt(2)
	if loopHead == nil {
		t.Fatal("missing node at address 2")
	}
	if len(loopHead.Incoming) != 2 {
		t.Fatalf("incoming edges at 2 = %v, want fallthrough from 0 and back edge from 4", loopHead.Incoming)
	}
	wantPaths := map[string]bool{
		pathKey([]uint64{0, 2}):       false,
		pathKey([]uint64{0, 2, 4, 2}): false,
	}
	for _, path := range loopHead.Paths {
		key := pathKey(path)
		if _, ok := wantPaths[key]; !ok {
			t.Fatalf("unexpected retained path %v", path)
		}
		wantPaths[key] = true
	}
	for key, found := range wantPaths {
		if !found {
			t.Fatalf("missing retained source path key %q; got %v", key, loopHead.Paths)
		}
	}

	terminator := report.NodeAt(6)
	if terminator == nil || !terminator.Terminates {
		t.Fatalf("node 6 = %+v, want terminating instruction", terminator)
	}
}

// A jump lands at address 2 while a linear decode already owns bytes 0..4:
// the two interpretations partially overlap and must both survive.
func TestRecoverJumpIntoMiddleConflict(t *testing.T) {
	decoder := &syntheticDecoder{program: map[uint64]synthInstr{
		0: {length: 4, successors: []Target{direct(4)}},
		2: {length: 2, successors: []Target{direct(4)}},
		4: {length: 2, terminates: true},
	}}

	report, err := newEngine(decoder).Recover([]uint64{0, 2})
	assertNoErr(t, err)

	if report.NodeAt(0) == nil || report.NodeAt(2) == nil {
		t.Fatal("both overlapping interpretations must be retained")
	}
	if len(report.Conflicts) != 1 {
		t.Fatalf("Conflicts = %v, want one partial overlap", report.Conflicts)
	}
	conflict := report.Conflicts[0]
	if conflict.First != (Interval{Start: 0, End: 4}) {
		t.Fatalf("First = %+v, want [0,4) (first arrival wins)", conflict.First)
	}
	if conflict.Second != (Interval{Start: 2, End: 4}) {
		t.Fatalf("Second = %+v, want [2,4)", conflict.Second)
	}
	if conflict.Shared != [2]uint64{2, 4} {
		t.Fatalf("Shared = %v, want [2,4]", conflict.Shared)
	}

	first := report.NodeAt(0)
	second := report.NodeAt(2)
	if len(first.Conflicts) != 1 || len(second.Conflicts) != 1 {
		t.Fatalf("both nodes must reference the conflict: %+v %+v", first.Conflicts, second.Conflicts)
	}

	// The first interpretation keeps its own provenance and is never replaced.
	if len(first.EntryRoots) != 1 || first.EntryRoots[0] != 0 {
		t.Fatalf("first node roots = %v, want [0]", first.EntryRoots)
	}
	if len(second.EntryRoots) != 1 || second.EntryRoots[0] != 2 {
		t.Fatalf("second node roots = %v, want [2]", second.EntryRoots)
	}

	// An identical re-decode through another path merges instead of conflicting.
	decoder2 := &syntheticDecoder{program: decoder.program}
	report2, err := newEngine(decoder2).Recover([]uint64{0})
	assertNoErr(t, err)
	if len(report2.Conflicts) != 0 {
		t.Fatalf("single linear walk must not conflict, got %v", report2.Conflicts)
	}
}

// A short read at a reached address becomes an Error node and stops expansion
// there without discarding the rest of the graph.
func TestRecoverShortRead(t *testing.T) {
	decoder := &syntheticDecoder{program: map[uint64]synthInstr{
		0: {length: 2, successors: []Target{direct(2), direct(8)}},
		2: {err: ErrShortRead},
		8: {length: 2, terminates: true},
	}}

	report, err := newEngine(decoder).Recover([]uint64{0})
	assertNoErr(t, err)

	if !IsShortRead(ErrShortRead) {
		t.Fatal("IsShortRead must recognise the sentinel")
	}
	if len(report.Errors) != 1 {
		t.Fatalf("Errors = %v, want one failure node", report.Errors)
	}
	failed := report.Errors[0]
	if failed.Address != 2 || !IsShortRead(failed.Err) {
		t.Fatalf("error node = %+v, want short read at 2", failed)
	}
	if !report.NodeAt(2).IsError() {
		t.Fatal("node at 2 must expose IsError")
	}
	if report.NodeAt(8) == nil {
		t.Fatal("the alternative branch must still be recovered")
	}

	// A zero-length result without an error is normalised by the engine.
	zeroDecoder := &syntheticDecoder{program: map[uint64]synthInstr{
		0: {length: 0},
	}}
	zeroReport, err := newEngine(zeroDecoder).Recover([]uint64{0})
	assertNoErr(t, err)
	if len(zeroReport.Errors) != 1 || !errors.Is(zeroReport.Errors[0].Err, ErrZeroLength) {
		t.Fatalf("zero-length decode = %+v, want ErrZeroLength", zeroReport.Errors)
	}
}

// Unresolved indirect transfers are listed separately, not followed.
func TestRecoverUnknownIndirect(t *testing.T) {
	decoder := &syntheticDecoder{program: map[uint64]synthInstr{
		0: {length: 2, successors: []Target{indirect(2)}},
		2: {length: 2, unknownIndirect: true, terminates: true},
	}}

	report, err := newEngine(decoder).Recover([]uint64{0})
	assertNoErr(t, err)

	if len(report.UnknownIndirect) != 1 {
		t.Fatalf("UnknownIndirect = %v, want one site", report.UnknownIndirect)
	}
	site := report.UnknownIndirect[0]
	if site.At != 2 {
		t.Fatalf("site = %+v, want address 2", site)
	}
	wantPath := []uint64{0, 2}
	if len(site.Path) != len(wantPath) {
		t.Fatalf("site path = %v, want %v", site.Path, wantPath)
	}
	for i := range wantPath {
		if site.Path[i] != wantPath[i] {
			t.Fatalf("site path = %v, want %v", site.Path, wantPath)
		}
	}
	// The unknown site must not generate a phantom edge or extra node.
	if report.Decoded != 2 {
		t.Fatalf("Decoded = %d, want 2", report.Decoded)
	}
}

// The instruction budget bounds Decode calls and is reported, not panicked on.
func TestRecoverBudget(t *testing.T) {
	decoder := &syntheticDecoder{program: map[uint64]synthInstr{
		0: {length: 2},
		2: {length: 2},
		4: {length: 2},
		6: {length: 2},
	}}

	engine := &Engine{Decoder: decoder, Base: 0, Size: 64, MaxInstructions: 2}
	report, err := engine.Recover([]uint64{0})
	assertNoErr(t, err)

	if !report.BudgetExceeded {
		t.Fatal("BudgetExceeded must be set")
	}
	if report.Decoded != 2 || report.BudgetLimit != 2 {
		t.Fatalf("Decoded = %d, limit = %d, want 2/2", report.Decoded, report.BudgetLimit)
	}
}

// Successors outside the decodable span are unmapped, never sent to the
// decoder, while fall-through is synthesised for non-terminating nodes.
func TestRecoverUnmappedAndFallthrough(t *testing.T) {
	decoder := &syntheticDecoder{program: map[uint64]synthInstr{
		0: {length: 2},
		2: {length: 2, successors: []Target{direct(128)}, terminates: true},
	}}

	report, err := newEngine(decoder).Recover([]uint64{0})
	assertNoErr(t, err)

	if len(report.UnmappedTargets) != 1 {
		t.Fatalf("UnmappedTargets = %v, want one", report.UnmappedTargets)
	}
	edge := report.UnmappedTargets[0]
	if edge.From != 2 || edge.To != 128 || edge.Kind != EdgeDirect {
		t.Fatalf("unmapped edge = %+v", edge)
	}
	// Address 0 did not terminate: engine synthesised the 0->2 fall-through.
	fallEdge := Edge{From: 0, To: 2, Kind: EdgeFallthrough}
	found := false
	for _, candidate := range report.Edges {
		if candidate == fallEdge {
			found = true
		}
	}
	if !found {
		t.Fatalf("Edges = %v, missing synthesised fall-through", report.Edges)
	}
	if decoder.calls != 2 {
		t.Fatalf("decoder calls = %d, unmapped target must not be decoded", decoder.calls)
	}
}

// Later arrivals never overwrite earlier ones; provenance is merged instead.
func TestRecoverNoOverwrite(t *testing.T) {
	decoder := &syntheticDecoder{program: map[uint64]synthInstr{
		0: {length: 2, successors: []Target{direct(4)}},
		4: {length: 2, successors: []Target{direct(8)}},
		8: {length: 2, successors: []Target{direct(4)}, terminates: true},
	}}

	report, err := newEngine(decoder).Recover([]uint64{0})
	assertNoErr(t, err)

	node := report.NodeAt(4)
	if node == nil {
		t.Fatal("missing node 4")
	}
	if node.Length != 2 || node.End != 6 {
		t.Fatalf("node 4 mutated: %+v", node)
	}
	if len(node.Incoming) != 2 {
		t.Fatalf("Incoming = %v, want edges from 0 and 8 retained together", node.Incoming)
	}
	if len(node.Paths) != 2 {
		t.Fatalf("Paths = %v, want two distinct source paths", node.Paths)
	}
}

func TestRecoverValidation(t *testing.T) {
	decoder := &syntheticDecoder{}
	if _, err := (&Engine{Base: 0, Size: 8}).Recover([]uint64{0}); !errors.Is(err, ErrNoDecoder) {
		t.Fatalf("missing decoder err = %v", err)
	}
	if _, err := newEngine(decoder).Recover(nil); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("empty entry err = %v", err)
	}
}

func TestEdgeKindString(t *testing.T) {
	cases := map[EdgeKind]string{
		EdgeFallthrough: "fallthrough",
		EdgeDirect:      "direct",
		EdgeIndirect:    "indirect",
	}
	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Fatalf("%d.String() = %q, want %q", kind, got, want)
		}
	}
}
