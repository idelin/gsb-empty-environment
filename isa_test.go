package leaklab

import "testing"

func TestParseRoundTripAndLabels(t *testing.T) {
	text := `
		# 演示标签与全部指令形态
		const r0, 7
	loop:
		add r1, r0, r2
		xor r3, r1, r0
		load r4, r3
		probe r4
		jmpeq r1, r2, loop
		barrier
		halt
	`
	prog, err := ParseProgram(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Instr) != 8 {
		t.Fatalf("instruction count = %d, want 8", len(prog.Instr))
	}
	if prog.Instr[5].Op != OpJmpEq || prog.Instr[5].Target != 1 {
		t.Fatalf("label resolution failed: %+v", prog.Instr[5])
	}
	reparsed, err := ParseProgram(prog.Text())
	if err != nil {
		t.Fatal(err)
	}
	for i := range prog.Instr {
		if prog.Instr[i] != reparsed.Instr[i] {
			t.Fatalf("round-trip mismatch at %d: %+v vs %+v", i, prog.Instr[i], reparsed.Instr[i])
		}
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"bad register":    "const r8, 1\nhalt\n",
		"bad jump target": "jmpeq r0, r1, 9\nhalt\n",
		"unknown opcode":  "nope r0\nhalt\n",
		"undefined label": "jmpeq r0, r1, ghost\nhalt\n",
	}
	for name, text := range cases {
		if _, err := ParseProgram(text); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
	if _, err := ParseProgram(""); err == nil {
		t.Error("empty program should be rejected")
	}
	big := NewAsmBuilder()
	for i := 0; i < MaxProgram+1; i++ {
		big.Halt()
	}
	if _, err := big.Build(); err == nil {
		t.Error("program exceeding MaxProgram should fail validation")
	}
}

func TestAsmBuilderLabels(t *testing.T) {
	prog, err := NewAsmBuilder().
		Const(RZero, 0).
		JmpEqLabel(RPub, RZero, "end").
		Probe(RPub).
		Label("end").
		Halt().
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if prog.Instr[1].Target != 3 {
		t.Fatalf("builder target = %d, want 3", prog.Instr[1].Target)
	}
}
