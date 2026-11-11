package relexec

import (
	"math/big"
	"math/rand"
	"testing"
)

// oracleExpectation is the result produced by the independent full-enumeration
// reference below. It deliberately reimplements the relational check instead
// of calling Verify.
type oracleExpectation struct {
	verdict Verdict
	reason  CounterexampleReason
	input   []byte
	checked *big.Int
}

func TestOracleCrossCheckHandcraftedPairs(t *testing.T) {
	type pair struct {
		name        string
		oldP, newP  *Program
		inputLen    int
		arrayLen    int
		wantReason  CounterexampleReason
		wantInput   []byte
		wantVerdict Verdict
	}

	earlyOld, earlyNew := earlyRejectPrograms(4)
	branchOld, branchNew := divergentBranchPrograms()

	pairs := []pair{
		{
			name:        "early-reject-too-tight",
			oldP:        earlyOld,
			newP:        earlyNew,
			inputLen:    1,
			arrayLen:    16,
			wantVerdict: VerdictCounterexample,
			wantReason:  ReasonNewOOB,
			wantInput:   []byte{4},
		},
		{
			name:        "branch-divergence",
			oldP:        branchOld,
			newP:        branchNew,
			inputLen:    1,
			arrayLen:    4,
			wantVerdict: VerdictCounterexample,
			wantReason:  ReasonReturnMismatch,
			wantInput:   []byte{0},
		},
	}

	// Equivalent early-rejection pair: guard limit equals the array length.
	eqOld, eqNew := earlyRejectPrograms(4)
	pairs = append(pairs, pair{
		name:        "early-reject-equivalent",
		oldP:        eqOld,
		newP:        eqNew,
		inputLen:    1,
		arrayLen:    4,
		wantVerdict: VerdictEquivalent,
		wantReason:  ReasonNone,
	})

	for _, tc := range pairs {
		t.Run(tc.name, func(t *testing.T) {
			exp := referenceEnumerate(t, tc.oldP, tc.newP, tc.inputLen, tc.arrayLen)
			if exp.verdict != tc.wantVerdict || exp.reason != tc.wantReason {
				t.Fatalf("oracle: got %s/%s want %s/%s",
					exp.verdict, exp.reason, tc.wantVerdict, tc.wantReason)
			}
			if tc.wantVerdict == VerdictCounterexample {
				if len(exp.input) != len(tc.wantInput) {
					t.Fatalf("oracle input length %d want %d", len(exp.input), len(tc.wantInput))
				}
				for i := range tc.wantInput {
					if exp.input[i] != tc.wantInput[i] {
						t.Fatalf("oracle lex-min input %v want %v", exp.input, tc.wantInput)
					}
				}
			}

			res, err := Verify(tc.oldP, tc.newP, Config{
				InputLen: tc.inputLen,
				ArrayLen: tc.arrayLen,
				Budget:   UnlimitedBudget,
			})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if res.Verdict != exp.verdict || res.Reason != exp.reason {
				t.Fatalf("Verify %s/%s disagrees with oracle %s/%s",
					res.Verdict, res.Reason, exp.verdict, exp.reason)
			}
			if res.Checked.Cmp(exp.checked) != 0 {
				t.Fatalf("checked %s vs oracle %s", res.Checked, exp.checked)
			}
			if res.Verdict == VerdictCounterexample {
				got := res.Counterexample.Input
				for i := range exp.input {
					if got[i] != exp.input[i] {
						t.Fatalf("Verify counterexample %v vs oracle %v", got, exp.input)
					}
				}
			}
		})
	}
}

// referenceEnumerate is the standalone full-enumeration oracle. It keeps its
// own input iteration and equivalence logic so a bug shared with Verify's
// nextLex/loop cannot silently pass.
func referenceEnumerate(t *testing.T, oldP, newP *Program, inputLen, arrayLen int) oracleExpectation {
	t.Helper()
	exp := oracleExpectation{checked: big.NewInt(0)}

	var recurse func(pos int, input []byte)
	recurse = func(pos int, input []byte) {
		if exp.verdict == VerdictCounterexample {
			return
		}
		if pos == inputLen {
			probe := append([]byte(nil), input...)
			oldOut, err := Run(oldP, probe, arrayLen, UnlimitedBudget)
			if err != nil {
				t.Fatalf("reference old run: %v", err)
			}
			newOut, err := Run(newP, probe, arrayLen, UnlimitedBudget)
			if err != nil {
				t.Fatalf("reference new run: %v", err)
			}
			exp.checked.Add(exp.checked, big.NewInt(1))
			if oldOut.OOB {
				return
			}
			switch {
			case newOut.OOB:
				exp.verdict, exp.reason, exp.input = VerdictCounterexample, ReasonNewOOB, probe
			case newOut.Return != oldOut.Return:
				exp.verdict, exp.reason, exp.input = VerdictCounterexample, ReasonReturnMismatch, probe
			case !writesEqual(oldOut.Writes, newOut.Writes):
				exp.verdict, exp.reason, exp.input = VerdictCounterexample, ReasonWriteMismatch, probe
			}
			return
		}
		for v := 0; v < 256; v++ {
			recurse(pos+1, append(input, byte(v)))
			if exp.verdict == VerdictCounterexample {
				return
			}
		}
	}
	recurse(0, nil)
	if exp.verdict != VerdictCounterexample {
		exp.verdict = VerdictEquivalent
	}
	return exp
}

// randomLoopFreeProgram builds a small straight-line/forward-jump program for
// differential fuzzing. Index registers for memory ops are drawn from a small
// pool so out-of-bounds traps genuinely occur, while value registers stay
// distinct to keep the generated code varied.
func randomLoopFreeProgram(rng *rand.Rand, arrayLen int) *Program {
	const n = 10
	code := make([]Instr, 0, n+1)
	for i := 0; i < n; i++ {
		dst := uint8(rng.Intn(RegCount))
		s1 := uint8(rng.Intn(RegCount))
		s2 := uint8(rng.Intn(RegCount))
		switch rng.Intn(12) {
		case 0:
			code = append(code, Instr{Op: OpConst, Dst: dst, Imm: byte(rng.Intn(256))})
		case 1, 2:
			code = append(code, Instr{Op: OpAdd, Dst: dst, Src1: s1, Src2: s2})
		case 3:
			code = append(code, Instr{Op: OpSub, Dst: dst, Src1: s1, Src2: s2})
		case 4:
			code = append(code, Instr{Op: OpXor, Dst: dst, Src1: s1, Src2: s2})
		case 5:
			code = append(code, Instr{Op: OpShl, Dst: dst, Src1: s1, Src2: s2})
		case 6:
			code = append(code, Instr{Op: OpCmpLtu, Dst: dst, Src1: s1, Src2: s2})
		case 7, 8:
			code = append(code, Instr{Op: OpStore, Src1: s1, Src2: s2})
		case 9:
			code = append(code, Instr{Op: OpLoad, Dst: dst, Src1: s1})
		case 10:
			// Strictly forward conditional jump; target fixed up after build.
			code = append(code, Instr{Op: OpJnz, Src1: s1, Target: i + 2})
		default:
			code = append(code, Instr{Op: OpMov, Dst: dst, Src1: s1})
		}
	}
	for i := range code {
		if code[i].Op == OpJnz && code[i].Target >= len(code) {
			code[i].Target = len(code) - 1
			if code[i].Target <= i {
				code[i] = Instr{Op: OpConst, Dst: 0, Imm: byte(rng.Intn(arrayLen))}
			}
		}
	}
	code = append(code, Instr{Op: OpHalt})
	prog := &Program{Code: code}
	if err := prog.Validate(); err != nil {
		// Regenerate rather than fail the fuzz run; random code is only valid
		// when all jump targets remain strictly forward.
		return randomLoopFreeProgram(rng, arrayLen)
	}
	return prog
}

func TestOracleCrossCheckRandomPairs(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1236))
	for iter := 0; iter < 12; iter++ {
		arrayLen := 2 + rng.Intn(4)
		oldP := randomLoopFreeProgram(rng, arrayLen)
		newP := randomLoopFreeProgram(rng, arrayLen)

		exp := referenceEnumerate(t, oldP, newP, 2, arrayLen)
		res, err := Verify(oldP, newP, Config{InputLen: 2, ArrayLen: arrayLen, Budget: UnlimitedBudget})
		if err != nil {
			t.Fatalf("iter %d Verify: %v", iter, err)
		}
		if res.Verdict != exp.verdict || res.Reason != exp.reason {
			t.Fatalf("iter %d: Verify %s/%s vs oracle %s/%s",
				iter, res.Verdict, res.Reason, exp.verdict, exp.reason)
		}
		if res.Checked.Cmp(exp.checked) != 0 {
			t.Fatalf("iter %d: checked %s vs %s", iter, res.Checked, exp.checked)
		}
		if res.Verdict == VerdictCounterexample {
			for i := range exp.input {
				if res.Counterexample.Input[i] != exp.input[i] {
					t.Fatalf("iter %d: counterexample %v vs %v",
						iter, res.Counterexample.Input, exp.input)
				}
			}
		}
	}
}

func TestOracleBudgetUnknownAgreesOnCheckedPrefix(t *testing.T) {
	oldP, newP := earlyRejectPrograms(16)
	res, err := Verify(oldP, newP, Config{InputLen: 1, ArrayLen: 4, Budget: 20})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Verdict != VerdictUnknown {
		t.Fatalf("got %s want unknown", res.Verdict)
	}
	// Independently replay the checked prefix and confirm no earlier
	// counterexample was missed before the budget ran out.
	input := make([]byte, 1)
	for n := int64(0); n < res.Checked.Int64(); n++ {
		oldOut, err := Run(oldP, input, 4, UnlimitedBudget)
		if err != nil {
			t.Fatalf("prefix old run: %v", err)
		}
		newOut, err := Run(newP, input, 4, UnlimitedBudget)
		if err != nil {
			t.Fatalf("prefix new run: %v", err)
		}
		if !oldOut.OOB && (newOut.OOB || newOut.Return != oldOut.Return ||
			!writesEqual(oldOut.Writes, newOut.Writes)) {
			t.Fatalf("prefix mismatch missed at %v", input)
		}
		if !nextLex(input) {
			break
		}
	}
}
