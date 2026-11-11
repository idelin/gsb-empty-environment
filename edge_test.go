package packetvm

import "testing"

func TestEmptyAndMalformedRules(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
		kind RejectKind
	}{
		{"nil body", Rule{Body: nil}, RejectInvalidAST},
		{"empty and", Rule{Body: &And{Pos: src(1, "and")}}, RejectInvalidAST},
		{"empty or", Rule{Body: &Or{Pos: src(2, "or")}}, RejectInvalidAST},
		{"not without inner", Rule{Body: &Not{Pos: src(3, "not")}}, RejectInvalidAST},
		{"cmp missing rhs", Rule{Body: &Cmp{LHS: lenNode(1), Pos: src(4, "cmp")}}, RejectInvalidAST},
		{"bad load width", Rule{Body: &Cmp{
			LHS: &Load{Offset: 0, Size: Size(9), Pos: src(5, "load")},
			RHS: immNode(6, 0), Pos: src(7, "cmp")}}, RejectBadOffset},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Compile(c.rule)
			re, ok := AsReject(err)
			if !ok {
				t.Fatalf("expected RejectError, got %v", err)
			}
			if re.Kind != c.kind {
				t.Fatalf("kind=%v want %v", re.Kind, c.kind)
			}
		})
	}
}

func TestExecuteGuards(t *testing.T) {
	if _, err := (*Program)(nil).Execute(nil); err != ErrMalformedProgram {
		t.Fatalf("nil program err=%v", err)
	}
	empty := &Program{Insns: nil}
	if _, err := empty.Execute(nil); err != ErrMalformedProgram {
		t.Fatalf("empty program err=%v", err)
	}

	rule := mustParse(t, "ok", "len >= 2 and u16(0) == 1")
	prog, err := Compile(rule)
	if err != nil {
		t.Fatal(err)
	}
	// A verified program must never raise an out-of-bounds runtime error.
	for n := 0; n < 6; n++ {
		pkt := make([]byte, n)
		if _, err := prog.Execute(pkt); err != nil {
			t.Fatalf("verified program errored on len %d: %v", n, err)
		}
	}
}

// The join invariant: after a merge the verifier's bound is exactly the
// minimum of the incoming bounds, regardless of join order.
func TestJoinIsMinimumAndOrderIndependent(t *testing.T) {
	a := state{minLen: 4, r0: valPacketLen, r1: valUnknown}
	b := state{minLen: 9, r0: valPacketLen, r1: valUnknown}
	j1 := join(a, b)
	j2 := join(b, a)
	if j1.minLen != 4 || j2.minLen != 4 {
		t.Fatalf("join minLen=%d/%d, want 4", j1.minLen, j2.minLen)
	}
	if j1 != j2 {
		t.Fatal("join is not order independent")
	}
	// Divergent register identities collapse to unknown.
	c := state{minLen: 4, r0: valUnknown, r1: valPacketLen}
	if join(a, c).r0 != valUnknown {
		t.Fatal("divergent R0 must join to unknown")
	}
}

func TestAllComparisonOperators(t *testing.T) {
	ops := []string{"==", "!=", "<", "<=", ">", ">="}
	for _, op := range ops {
		rule := mustParse(t, op, "len >= 4 and u8(0) "+op+" 7")
		prog, err := Compile(rule)
		if err != nil {
			t.Fatalf("operator %s: %v", op, err)
		}
		assertEquivalent(t, rule, enumeratePackets(rule, 4))
		if len(prog.Insns) == 0 {
			t.Fatalf("operator %s produced no code", op)
		}
	}
}

func TestRejectErrorIsExplicitAndLocated(t *testing.T) {
	rule := mustParse(t, "located", "len >= 1 and u32(0) == 1")
	_, err := Compile(rule)
	re, ok := AsReject(err)
	if !ok {
		t.Fatal("expected reject")
	}
	if re.Kind != RejectUnverifiedLoad {
		t.Fatalf("kind=%v", re.Kind)
	}
	if re.Index < 0 {
		t.Fatal("rejection must carry an instruction index")
	}
	if re.Source.Text == "" || re.Source.Line < 1 || re.Source.Col < 1 {
		t.Fatalf("rejection source not located: %+v", re.Source)
	}
	if re.Error() == "" {
		t.Fatal("rejection must render a message")
	}
}

func TestDanglingControlFlowRejected(t *testing.T) {
	// Hand-built bytecode whose last instruction falls through with no RET.
	insns := []Insn{
		{Op: OpImm, Imm: 1, Dst: R0, Origin: src(1, "imm")},
		{Op: OpImm, Imm: 2, Dst: R0, Origin: src(2, "imm")},
	}
	err := verify(insns)
	re, ok := AsReject(err)
	if !ok || re.Kind != RejectDanglingJump {
		t.Fatalf("got %v, want dangling-jump rejection", err)
	}
}

func TestInstructionBudgetEnforced(t *testing.T) {
	// Build a deeply nested OR tree directly to exceed MaxInstructions
	// without relying on parser depth.
	var build func(depth int) Bool
	build = func(depth int) Bool {
		if depth == 0 {
			return &Cmp{Op: Eq, LHS: immNode(1, 1), RHS: immNode(2, 2), Pos: src(3, "c")}
		}
		return &Or{Terms: []Bool{
			&Cmp{Op: Gt, LHS: lenNode(4), RHS: immNode(5, 1), Pos: src(6, "g")},
			build(depth - 1),
		}, Pos: src(7, "or")}
	}
	rule := Rule{Body: build(2500)}
	_, err := Compile(rule)
	re, ok := AsReject(err)
	if !ok || re.Kind != RejectTooLarge {
		t.Fatalf("got %v, want RejectTooLarge", err)
	}
}
