package cfverify

import "testing"

// fakeInstr describes one instruction for the in-package fake decoder.
type fakeInstr struct {
	length          uint64
	successors      []uint64
	indirectTargets []uint64
	fail            bool
}

func newFakeDecoder(program map[uint64]fakeInstr) Decoder {
	return func(_ []byte, pc uint64) (DecodedInstr, error) {
		instr, ok := program[pc]
		if !ok || instr.fail {
			return DecodedInstr{}, errFakeDecode
		}
		return DecodedInstr{
			PC:              pc,
			Bytes:           Interval{Start: pc, End: pc + instr.length},
			Successors:      append([]uint64(nil), instr.successors...),
			IndirectTargets: append([]uint64(nil), instr.indirectTargets...),
		}, nil
	}
}

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

const errFakeDecode = fakeErr("fake decode failure")

func fakeConfig(program map[uint64]fakeInstr, entries []uint64, size int) Config {
	return Config{
		Image:         make([]byte, size),
		Entries:       entries,
		VersionOffset: 0,
		VersionByte:   0,
		Decode:        newFakeDecoder(program),
	}
}

func TestNilDecoderAndEmptyEntries(t *testing.T) {
	if got := Verify(Config{}); got.Verdict != VerdictReject {
		t.Fatalf("nil decoder must reject, got %s", got.Verdict)
	}
	cfg := fakeConfig(map[uint64]fakeInstr{}, nil, 4)
	if got := Verify(cfg); got.Verdict != VerdictReject {
		t.Fatalf("empty entries must reject, got %s", got.Verdict)
	}
}

func TestOverlapIsNotOverwritten(t *testing.T) {
	// Instruction at 0 owns bytes 0..2; an instruction reached at 2 collides.
	program := map[uint64]fakeInstr{
		0: {length: 3, successors: []uint64{2}},
		2: {length: 1},
	}
	got := Verify(fakeConfig(program, []uint64{0}, 8))
	if got.Verdict != VerdictConflict {
		t.Fatalf("overlap must conflict, got %s", got.Verdict)
	}
	if got.Conflict.OwnerPC != 0 || got.Conflict.Target != 2 {
		t.Fatalf("first owner must be retained: %+v", got.Conflict)
	}
	if len(got.Witness) != 2 || got.Witness[0] != 0 || got.Witness[1] != 2 {
		t.Fatalf("witness = %v, want [0 2]", got.Witness)
	}
}

func TestIndirectTargetUniqueStart(t *testing.T) {
	program := map[uint64]fakeInstr{
		0: {length: 2, successors: []uint64{2}, indirectTargets: []uint64{5}},
		2: {length: 1},
	}
	got := Verify(fakeConfig(program, []uint64{0}, 8))
	if got.Verdict != VerdictConflict {
		t.Fatalf("in-image indirect target at non-start must conflict, got %s", got.Verdict)
	}
	if got.Conflict.Target != 5 {
		t.Fatalf("conflict target = %d, want 5", got.Conflict.Target)
	}
	if got.Conflict.Kind != ConflictIndirectNotStart {
		t.Fatalf("conflict kind = %s, want indirect-not-start", got.Conflict.Kind)
	}
}

func TestIndirectTargetOutOfImageRejects(t *testing.T) {
	program := map[uint64]fakeInstr{
		0: {length: 1, indirectTargets: []uint64{40}},
	}
	got := Verify(fakeConfig(program, []uint64{0}, 4))
	if got.Verdict != VerdictReject {
		t.Fatalf("out-of-image indirect target must reject, got %s", got.Verdict)
	}
}

func TestEdgeBudgetUnknown(t *testing.T) {
	program := map[uint64]fakeInstr{
		0: {length: 1, successors: []uint64{1}},
		1: {length: 1, successors: []uint64{2}},
		2: {length: 1, successors: []uint64{3}},
		3: {length: 1},
	}
	cfg := fakeConfig(program, []uint64{0}, 8)
	cfg.Work = Budget{MaxInstructions: 10, MaxEdges: 1}
	got := Verify(cfg)
	if got.Verdict != VerdictUnknown {
		t.Fatalf("edge budget exhaustion must be unknown, got %s", got.Verdict)
	}
}

func TestSafeGraph(t *testing.T) {
	program := map[uint64]fakeInstr{
		0: {length: 1, successors: []uint64{1}},
		1: {length: 2, successors: []uint64{3}, indirectTargets: []uint64{3}},
		3: {length: 1},
	}
	got := Verify(fakeConfig(program, []uint64{0}, 8))
	if got.Verdict != VerdictSafe {
		t.Fatalf("disjoint legal graph must be safe, got %s: %+v", got.Verdict, got.Conflict)
	}
	if got.Stats.Decoded != 3 {
		t.Fatalf("decoded = %d, want 3", got.Stats.Decoded)
	}
}

func TestVerdictAndConflictStrings(t *testing.T) {
	cases := map[fmtStringer]string{
		VerdictSafe:              "safe",
		VerdictConflict:          "conflict",
		VerdictReject:            "reject",
		VerdictUnknown:           "unknown",
		ConflictOverlap:          "overlap",
		ConflictMidEntry:         "mid-entry",
		ConflictIndirectNotStart: "indirect-not-start",
		ConflictUnknown:          "unknown",
	}
	for value, want := range cases {
		if got := value.String(); got != want {
			t.Fatalf("%v.String() = %q, want %q", value, got, want)
		}
	}
}

type fmtStringer interface {
	String() string
}
