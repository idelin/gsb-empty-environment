package relexec

import (
	"errors"
	"testing"
)

func TestRunArithmeticWrapsAtEightBits(t *testing.T) {
	prog := &Program{Code: []Instr{
		{Op: OpConst, Dst: 1, Imm: 200},
		{Op: OpConst, Dst: 2, Imm: 100},
		{Op: OpAdd, Dst: 0, Src1: 1, Src2: 2}, // 300 -> 44
		{Op: OpConst, Dst: 3, Imm: 1},
		{Op: OpSub, Dst: 4, Src1: 3, Src2: 1}, // 1 - 200 -> 57
		{Op: OpMul, Dst: 5, Src1: 1, Src2: 2}, // 20000 mod 256 -> 32
		{Op: OpHalt},
	}}
	out, err := Run(prog, nil, 4, UnlimitedBudget)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Return != 44 {
		t.Errorf("add wrap: got %d want 44", out.Return)
	}
	regs4, regs5 := runRegs(t, prog, 4), runRegs(t, prog, 5)
	if regs4 != 57 {
		t.Errorf("sub wrap: got %d want 57", regs4)
	}
	if regs5 != 32 {
		t.Errorf("mul wrap: got %d want 32", regs5)
	}
	if !out.Halted || out.HaltPC != 6 || out.OOB {
		t.Errorf("unexpected outcome: %+v", out)
	}
	if out.Steps != 7 {
		t.Errorf("steps: got %d want 7", out.Steps)
	}
}

// runRegs exposes a register value by moving it into r0 before halting.
func runRegs(t *testing.T, base *Program, reg uint8) uint8 {
	t.Helper()
	code := append([]Instr(nil), base.Code[:len(base.Code)-1]...)
	code = append(code, Instr{Op: OpMov, Dst: 0, Src1: reg}, Instr{Op: OpHalt})
	out, err := Run(&Program{Code: code}, nil, 4, UnlimitedBudget)
	if err != nil {
		t.Fatalf("runRegs: %v", err)
	}
	return out.Return
}

func TestRunComparisonsAndShifts(t *testing.T) {
	prog := &Program{Code: []Instr{
		{Op: OpConst, Dst: 1, Imm: 0xfe},         // 254 unsigned, -2 signed
		{Op: OpConst, Dst: 2, Imm: 0x02},         // 2
		{Op: OpCmpLtu, Dst: 3, Src1: 1, Src2: 2}, // 254 < 2 -> 0
		{Op: OpCmpLt, Dst: 4, Src1: 1, Src2: 2},  // -2 < 2 -> 1
		{Op: OpCmpEq, Dst: 5, Src1: 1, Src2: 1},  // equal -> 1
		{Op: OpShl, Dst: 6, Src1: 2, Src2: 2},    // 2 << 2 -> 8
		{Op: OpShr, Dst: 7, Src1: 1, Src2: 2},    // 254 >> 2 -> 63
		{Op: OpHalt},
	}}
	out, err := Run(prog, nil, 4, UnlimitedBudget)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := map[uint8]uint8{3: 0, 4: 1, 5: 1, 6: 8, 7: 63}
	for reg, v := range want {
		if got := runRegs(t, prog, reg); got != v {
			t.Errorf("r%d: got %d want %d", reg, got, v)
		}
	}
	_ = out
}

func TestRunConditionalForwardJumps(t *testing.T) {
	// r0 holds the single input byte.
	// if r0 == 0 -> r0 = 10 else r0 = 20, then halt.
	prog := &Program{Code: []Instr{
		{Op: OpJz, Src1: 0, Target: 4},
		{Op: OpConst, Dst: 0, Imm: 20},
		{Op: OpJmp, Target: 5},
		{Op: OpHalt},                   // pc 3 unreachable
		{Op: OpConst, Dst: 0, Imm: 10}, // pc 4
		{Op: OpHalt},                   // pc 5
	}}
	if out, _ := Run(prog, []byte{0}, 4, UnlimitedBudget); out.Return != 10 {
		t.Errorf("input 0: got %d want 10", out.Return)
	}
	if out, _ := Run(prog, []byte{7}, 4, UnlimitedBudget); out.Return != 20 {
		t.Errorf("input 7: got %d want 20", out.Return)
	}
}

func TestRunStoreLoadAndWriteTrace(t *testing.T) {
	prog := &Program{Code: []Instr{
		{Op: OpConst, Dst: 1, Imm: 2},
		{Op: OpConst, Dst: 2, Imm: 0x5a},
		{Op: OpStore, Src1: 1, Src2: 2}, // mem[2] = 0x5a
		{Op: OpConst, Dst: 2, Imm: 0xa5},
		{Op: OpStore, Src1: 1, Src2: 2}, // mem[2] = 0xa5
		{Op: OpLoad, Dst: 0, Src1: 1},   // r0 = mem[2]
		{Op: OpHalt},
	}}
	out, err := Run(prog, nil, 4, UnlimitedBudget)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Return != 0xa5 {
		t.Errorf("load: got %#x want 0xa5", out.Return)
	}
	wantWrites := []Write{{2, 0x5a}, {2, 0xa5}}
	if len(out.Writes) != 2 || out.Writes[0] != wantWrites[0] || out.Writes[1] != wantWrites[1] {
		t.Errorf("writes: got %+v want %+v", out.Writes, wantWrites)
	}
}

func TestRunOutOfBoundsTraps(t *testing.T) {
	// Store then load at the input index; length 4 traps at index >= 4.
	prog := &Program{Code: []Instr{
		{Op: OpConst, Dst: 1, Imm: 9},
		{Op: OpStore, Src1: 0, Src2: 1},
		{Op: OpLoad, Dst: 2, Src1: 0},
		{Op: OpHalt},
	}}
	out, err := Run(prog, []byte{4}, 4, UnlimitedBudget)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.OOB || !out.OOBStore || out.OOBIndex != 4 || out.OOBPC != 1 {
		t.Errorf("store trap: %+v", out)
	}
	if out.Return != 0 || len(out.Writes) != 0 {
		t.Errorf("trap must expose no return or writes: %+v", out)
	}

	loadProg := &Program{Code: []Instr{
		{Op: OpLoad, Dst: 1, Src1: 0},
		{Op: OpHalt},
	}}
	// Length-16 array: index 15 is the last valid byte, 16 is the first trap.
	if inBounds, _ := Run(loadProg, []byte{15}, 16, UnlimitedBudget); inBounds.OOB {
		t.Errorf("index 15 must stay in bounds on a 16-byte array")
	}
	out, _ = Run(loadProg, []byte{16}, 16, UnlimitedBudget)
	if !out.OOB || out.OOBStore || out.OOBIndex != 16 || out.OOBPC != 0 {
		t.Errorf("load boundary trap: %+v", out)
	}
}

func TestRunBudgetExhaustion(t *testing.T) {
	prog := &Program{Code: []Instr{{Op: OpConst, Dst: 0, Imm: 1}, {Op: OpHalt}}}
	if _, err := Run(prog, nil, 4, 1); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("budget 1: got %v want ErrBudgetExhausted", err)
	}
	out, err := Run(prog, nil, 4, 2)
	if err != nil || out.Return != 1 {
		t.Fatalf("budget 2 should complete: out=%+v err=%v", out, err)
	}
}

func TestValidateRejectsBadPrograms(t *testing.T) {
	cases := map[string]*Program{
		"bad register":   {Code: []Instr{{Op: OpConst, Dst: 8}}},
		"unknown opcode": {Code: []Instr{{Op: Op(99)}}},
		"backward jump": {Code: []Instr{
			{Op: OpHalt},
			{Op: OpJmp, Target: 0},
		}},
		"self jump":     {Code: []Instr{{Op: OpJmp, Target: 0}}},
		"jump past end": {Code: []Instr{{Op: OpJmp, Target: 2}}},
	}
	for name, prog := range cases {
		t.Run(name, func(t *testing.T) {
			if err := prog.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", name)
			}
		})
	}
}

func TestRunRejectsBadConfig(t *testing.T) {
	prog := &Program{Code: []Instr{{Op: OpHalt}}}
	if _, err := Run(prog, nil, 0, UnlimitedBudget); err == nil {
		t.Error("array length 0 should be rejected")
	}
	if _, err := Run(prog, nil, MaxArrayLen+1, UnlimitedBudget); err == nil {
		t.Error("array length above max should be rejected")
	}
	if _, err := Run(prog, make([]byte, MaxInputLen+1), 4, UnlimitedBudget); err == nil {
		t.Error("input above max should be rejected")
	}
	if _, err := Run(prog, nil, 4, -2); err == nil {
		t.Error("negative budget should be rejected")
	}
}
