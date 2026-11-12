package synthisa_test

import (
	"math/rand"
	"testing"

	"example.org/domainreissue/q007/cfverify"
	"example.org/domainreissue/q007/cfverify/synthisa"
)

func baseConfig(image []byte, entries ...uint64) cfverify.Config {
	return cfverify.Config{
		Image:         image,
		Entries:       entries,
		VersionOffset: 0,
		VersionByte:   synthisa.VersionByte,
		Decode:        synthisa.Decode,
	}
}

func equalUint64(a, b []uint64) bool {
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

// TestLegalSharedTail converges two trusted entries onto one HALT and expects
// a loadable safe result.
func TestLegalSharedTail(t *testing.T) {
	image := synthisa.Image(
		synthisa.Jmp(3),               // pc 1..2 -> pc 4
		synthisa.Ins(synthisa.OpNop),  // pc 3 (second entry)
		synthisa.Ins(synthisa.OpHalt), // pc 4 shared tail
	)
	got := cfverify.Verify(baseConfig(image, 1, 3))
	if got.Verdict != cfverify.VerdictSafe {
		t.Fatalf("shared tail must be safe, got %s: %+v", got.Verdict, got.Conflict)
	}
	// The shared HALT is decoded once even though two entries reach it.
	if got.Stats.Decoded != 3 {
		t.Fatalf("decoded=%d, want 3 distinct instructions", got.Stats.Decoded)
	}
}

// TestJumpIntoInstructionMiddle hides a jump target inside a previously
// decoded 3-byte instruction. The first owner must win rather than being
// overwritten.
func TestJumpIntoInstructionMiddle(t *testing.T) {
	image := synthisa.Image(
		synthisa.Calli(8),             // pc 1..3, legal indirect target pc 8
		synthisa.Jmp(-1),              // pc 4..5 -> pc 3 (middle of the CALLI)
		synthisa.Ins(synthisa.OpHalt), // pc 6
		synthisa.Ins(synthisa.OpNop),  // pc 7
		synthisa.Ins(synthisa.OpHalt), // pc 8
	)
	got := cfverify.Verify(baseConfig(image, 1))
	if got.Verdict != cfverify.VerdictConflict {
		t.Fatalf("want conflict for mid-entry jump, got %s", got.Verdict)
	}
	if got.Conflict.Kind != cfverify.ConflictMidEntry ||
		got.Conflict.Target != 3 || got.Conflict.OwnerPC != 1 {
		t.Fatalf("unexpected conflict: %+v", got.Conflict)
	}
	wantWitness := []uint64{1, 4, 3}
	if !equalUint64(got.Witness, wantWitness) {
		t.Fatalf("witness = %v, want %v", got.Witness, wantWitness)
	}
}

// TestIndirectTargetHiddenInMiddle places a permitted indirect target at the
// second byte of an instruction, matching the malicious-module scenario.
func TestIndirectTargetHiddenInMiddle(t *testing.T) {
	image := synthisa.Image(
		synthisa.Calli(2),             // pc 1..3, permitted target pc 2 (inside itself)
		synthisa.Ins(synthisa.OpHalt), // pc 4
	)
	got := cfverify.Verify(baseConfig(image, 1))
	if got.Verdict != cfverify.VerdictConflict {
		t.Fatalf("want conflict for hidden indirect target, got %s", got.Verdict)
	}
	if got.Conflict.Target != 2 {
		t.Fatalf("conflict target = %d, want 2", got.Conflict.Target)
	}
	if got.Conflict.Kind != cfverify.ConflictMidEntry &&
		got.Conflict.Kind != cfverify.ConflictIndirectNotStart {
		t.Fatalf("unexpected conflict kind %s", got.Conflict.Kind)
	}
	if len(got.Witness) == 0 || got.Witness[len(got.Witness)-1] != 2 {
		t.Fatalf("witness must end at target 2, got %v", got.Witness)
	}
}

// TestIndirectTargetNotAnInstruction points a permitted target outside the
// image; bounds violations must be rejected rather than loaded.
func TestIndirectTargetNotAnInstruction(t *testing.T) {
	image := synthisa.Image(
		synthisa.Ins(synthisa.OpNop),  // pc 1 -> pc 2
		synthisa.Calli(99),            // pc 2..4, permitted target outside image
		synthisa.Ins(synthisa.OpHalt), // pc 5
	)
	got := cfverify.Verify(baseConfig(image, 1))
	if got.Verdict != cfverify.VerdictReject {
		t.Fatalf("out-of-image target must reject, got %s (%s)", got.Verdict, got.Reason)
	}
}

// TestShortestWitnessUsesClosestEntry gives two trusted entries and confirms
// the returned witness starts at the nearest entry.
func TestShortestWitnessUsesClosestEntry(t *testing.T) {
	image := synthisa.Image(
		synthisa.Ins(synthisa.OpNop),  // pc 1
		synthisa.Ins(synthisa.OpNop),  // pc 2
		synthisa.Ins(synthisa.OpNop),  // pc 3
		synthisa.Calli(6),             // pc 4..6, fall-through to pc 7
		synthisa.Jmp(-1),              // pc 7..8 -> pc 6 (middle of CALLI)
		synthisa.Ins(synthisa.OpHalt), // pc 9
	)
	got := cfverify.Verify(baseConfig(image, 1, 7))
	if got.Verdict != cfverify.VerdictConflict {
		t.Fatalf("want conflict, got %s", got.Verdict)
	}
	wantWitness := []uint64{7, 6}
	if !equalUint64(got.Witness, wantWitness) {
		t.Fatalf("witness = %v, want shortest %v", got.Witness, wantWitness)
	}
}

// TestBudgetExhaustionIsUnknown drives a long linear image into the decode
// budget and requires Unknown rather than any loadable conclusion.
func TestBudgetExhaustionIsUnknown(t *testing.T) {
	body := make([]byte, 0, 40)
	for i := 0; i < 39; i++ {
		body = append(body, synthisa.OpNop)
	}
	body = append(body, synthisa.OpHalt)
	image := synthisa.Image(body)

	cfg := baseConfig(image, 1)
	cfg.Work = cfverify.Budget{MaxInstructions: 5, MaxEdges: 5}
	got := cfverify.Verify(cfg)
	if got.Verdict != cfverify.VerdictUnknown {
		t.Fatalf("budget exhaustion must be unknown, got %s", got.Verdict)
	}
	if got.Reason == "" {
		t.Fatal("unknown result must explain the exhausted budget")
	}
	if got.Stats.Decoded > 5 || got.Stats.Edges > 5 {
		t.Fatalf("work beyond budget: %+v", got.Stats)
	}

	// The same image with ample budget must reach a loadable conclusion.
	full := cfverify.Verify(baseConfig(image, 1))
	if full.Verdict != cfverify.VerdictSafe {
		t.Fatalf("full-budget run must be safe, got %s", full.Verdict)
	}
}

// TestVersionByteGuards the fixed version byte requirement.
func TestVersionByteGuards(t *testing.T) {
	image := synthisa.Image(synthisa.Ins(synthisa.OpHalt))
	image[0] = 0x00 // tamper with the version byte
	got := cfverify.Verify(baseConfig(image, 1))
	if got.Verdict != cfverify.VerdictReject {
		t.Fatalf("tampered version must reject, got %s", got.Verdict)
	}

	cfg := baseConfig(synthisa.Image(synthisa.Ins(synthisa.OpHalt)), 1)
	cfg.VersionOffset = 99
	if res := cfverify.Verify(cfg); res.Verdict != cfverify.VerdictReject {
		t.Fatalf("out-of-range version offset must reject, got %s", res.Verdict)
	}
}

// TestDecoderContractViolation rejects a decoder that misreports its PC.
func TestDecoderContractViolation(t *testing.T) {
	image := synthisa.Image(
		synthisa.Ins(synthisa.OpNop),
		synthisa.Ins(synthisa.OpHalt),
	)
	bad := func(_ []byte, pc uint64) (cfverify.DecodedInstr, error) {
		instr, err := synthisa.Decode(image, pc)
		if err != nil {
			return instr, err
		}
		instr.PC = pc + 1
		return instr, nil
	}
	cfg := baseConfig(image, 1)
	cfg.Decode = bad
	if got := cfverify.Verify(cfg); got.Verdict != cfverify.VerdictReject {
		t.Fatalf("decoder PC mismatch must reject, got %s", got.Verdict)
	}
}

// TestDeterministicRepeatedRuns verifies repeated verification of the same
// image yields identical verdicts and witnesses.
func TestDeterministicRepeatedRuns(t *testing.T) {
	image := synthisa.Image(
		synthisa.Calli(8),
		synthisa.Jmp(-1),
		synthisa.Ins(synthisa.OpNop),
		synthisa.Ins(synthisa.OpHalt),
	)
	var first cfverify.Result
	for i := 0; i < 5; i++ {
		got := cfverify.Verify(baseConfig(image, 1))
		if i == 0 {
			first = got
			continue
		}
		if got.Verdict != first.Verdict ||
			(got.Conflict == nil) != (first.Conflict == nil) ||
			!equalUint64(got.Witness, first.Witness) {
			t.Fatalf("non-deterministic result on run %d: %+v vs %+v", i, got, first)
		}
	}
}

// TestNonZeroBase exercises the base-aware decoder with absolute addressing.
func TestNonZeroBase(t *testing.T) {
	const base = uint64(0x1000)
	image := synthisa.Image(
		synthisa.Calli(4), // absolute target base+4 (the HALT), legal start
		synthisa.Ins(synthisa.OpHalt),
	)
	got := cfverify.Verify(cfverify.Config{
		Image:         image,
		Base:          base,
		Entries:       []uint64{base + 1},
		VersionOffset: 0,
		VersionByte:   synthisa.VersionByte,
		Decode:        synthisa.DecodeAtBase(base),
	})
	if got.Verdict != cfverify.VerdictSafe {
		t.Fatalf("base-addressed legal image must be safe, got %s: %+v", got.Verdict, got.Conflict)
	}
}

// TestRandomImagesNeverPanic fuzzes random images and asserts every result is
// one of the documented verdicts and never panics.
func TestRandomImagesNeverPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for iter := 0; iter < 2000; iter++ {
		n := 1 + rng.Intn(20)
		image := make([]byte, n)
		image[0] = synthisa.VersionByte
		for i := 1; i < n; i++ {
			image[i] = byte(rng.Intn(6)) // includes one unknown opcode value
		}
		entry := uint64(1)
		if n == 1 {
			entry = 0
		}
		got := cfverify.Verify(baseConfig(image, entry))
		switch got.Verdict {
		case cfverify.VerdictSafe, cfverify.VerdictConflict,
			cfverify.VerdictReject, cfverify.VerdictUnknown:
		default:
			t.Fatalf("iter %d: invalid verdict %d", iter, got.Verdict)
		}
		if got.Verdict == cfverify.VerdictConflict && got.Conflict == nil {
			t.Fatalf("iter %d: conflict verdict without detail", iter)
		}
	}
}
