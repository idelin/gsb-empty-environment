package packetvm

import (
	"testing"
)

func src(id int, text string) Source {
	return Source{ID: id, Text: text, Line: 1, Col: 1}
}

func loadNode(id int, off uint32, size Size) *Load {
	return &Load{Offset: off, Size: size, Pos: src(id, "load")}
}

func lenNode(id int) *Len { return &Len{Pos: src(id, "len")} }

func immNode(id int, v uint32) *Imm { return &Imm{Value: v, Pos: src(id, "imm")} }

// A bare load with no preceding length guard must be rejected, and the
// rejection must name the load's own source expression.
func TestRejectsUnprotectedLoad(t *testing.T) {
	rule := Rule{Name: "unsafe", Body: &Cmp{
		Op:  Eq,
		LHS: loadNode(7, 4, Word),
		RHS: immNode(8, 0),
		Pos: src(9, "cmp"),
	}}
	_, err := Compile(rule)
	re, ok := AsReject(err)
	if !ok {
		t.Fatalf("expected RejectError, got %v", err)
	}
	if re.Kind != RejectUnverifiedLoad {
		t.Fatalf("kind = %v, want RejectUnverifiedLoad", re.Kind)
	}
	if re.Source.ID != 7 {
		t.Fatalf("rejection source = expr#%d, want expr#7 (the load)", re.Source.ID)
	}
	if re.Need != 8 || re.Guaranteed != 0 {
		t.Fatalf("need=%d guaranteed=%d, want 8/0", re.Need, re.Guaranteed)
	}
}

// The same load is accepted when a length guard dominates it.
func TestAcceptsGuardedLoad(t *testing.T) {
	guard := &Cmp{Op: Ge, LHS: lenNode(1), RHS: immNode(2, 8), Pos: src(3, "len>=8")}
	body := &And{Terms: []Bool{
		guard,
		&Cmp{Op: Eq, LHS: loadNode(4, 4, Word), RHS: immNode(5, 1), Pos: src(6, "load==1")},
	}, Pos: src(10, "and")}
	prog, err := Compile(Rule{Name: "safe", Body: body})
	if err != nil {
		t.Fatalf("guarded load rejected: %v", err)
	}
	if len(prog.Insns) == 0 {
		t.Fatal("compiled program is empty")
	}
}

// At an OR confluence only the length guaranteed by BOTH paths survives.
// One branch proves len>=4, the other proves len>=2; the join may assume
// only len>=2, so a 4-byte read placed after the join must be rejected.
func TestJoinTakesMinimumGuarantee(t *testing.T) {
	branch4 := &Cmp{Op: Ge, LHS: lenNode(1), RHS: immNode(2, 4), Pos: src(3, "len>=4")}
	branch2 := &Cmp{Op: Ge, LHS: lenNode(4), RHS: immNode(5, 2), Pos: src(6, "len>=2")}
	or := &Or{Terms: []Bool{branch4, branch2}, Pos: src(7, "or")}
	afterJoin := &Cmp{Op: Eq, LHS: loadNode(8, 0, Word), RHS: immNode(9, 0), Pos: src(11, "word")}
	body := &And{Terms: []Bool{or, afterJoin}, Pos: src(12, "and")}

	_, err := Compile(Rule{Name: "join", Body: body})
	re, ok := AsReject(err)
	if !ok {
		t.Fatalf("expected RejectError, got %v", err)
	}
	if re.Kind != RejectUnverifiedLoad || re.Source.ID != 8 {
		t.Fatalf("got kind=%v source=expr#%d, want unverified load at expr#8", re.Kind, re.Source.ID)
	}
	if re.Guaranteed != 2 {
		t.Fatalf("join guarantee = %d, want 2", re.Guaranteed)
	}
}

// When every incoming path proves enough length, the join admits the read.
func TestJoinAdmitsWhenAllPathsProveLength(t *testing.T) {
	branch8 := &Cmp{Op: Ge, LHS: lenNode(1), RHS: immNode(2, 8), Pos: src(3, "len>=8")}
	branch10 := &Cmp{Op: Ge, LHS: lenNode(4), RHS: immNode(5, 10), Pos: src(6, "len>=10")}
	or := &Or{Terms: []Bool{branch8, branch10}, Pos: src(7, "or")}
	afterJoin := &Cmp{Op: Eq, LHS: loadNode(8, 0, Word), RHS: immNode(9, 0), Pos: src(11, "word")}
	body := &And{Terms: []Bool{or, afterJoin}, Pos: src(12, "and")}

	if _, err := Compile(Rule{Name: "join-ok", Body: body}); err != nil {
		t.Fatalf("join should prove len>=8: %v", err)
	}
}

// Strict and equality length checks must derive the right lower bounds.
func TestLengthBoundDerivations(t *testing.T) {
	cases := []struct {
		op    CmpOp
		k     uint32
		taken bool
		want  uint32
		ok    bool
	}{
		{Gt, 7, true, 8, true},
		{Gt, 7, false, 0, false},
		{Eq, 8, true, 8, true},
		{Ge, 8, true, 8, true},
		{Lt, 8, true, 0, false},
		{Le, 8, false, 9, true}, // negation of <=8 is >8 => >=9
		{Ne, 8, true, 0, false},
	}
	for _, c := range cases {
		got, ok := edgeBound(c.op, valPacketLen, c.k, c.taken)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("edgeBound(%v,%d,taken=%v)=(%d,%v), want (%d,%v)",
				c.op, c.k, c.taken, got, ok, c.want, c.ok)
		}
	}
	// Non-length comparisons never establish a length bound.
	if _, ok := edgeBound(Eq, valUnknown, 8, true); ok {
		t.Error("comparison on non-length register must not prove a bound")
	}
}

// Offsets whose end address overflows are refused structurally.
func TestOverflowOffsetRejected(t *testing.T) {
	rule := Rule{Body: &And{Terms: []Bool{
		&Cmp{Op: Ge, LHS: lenNode(1), RHS: immNode(2, 10), Pos: src(3, "g")},
		&Cmp{Op: Eq, LHS: &Load{Offset: ^uint32(0), Size: Byte, Pos: src(4, "wrap")},
			RHS: immNode(5, 0), Pos: src(6, "c")},
	}, Pos: src(9, "and")}}
	_, err := Compile(rule)
	re, ok := AsReject(err)
	if !ok || re.Kind != RejectBadOffset || re.Source.ID != 4 {
		t.Fatalf("got %v, want bad-offset attributed to expr#4", err)
	}
}

// All control transfers in a compiled program must be strictly forward.
// A load reached only after an earlier AND term guards the length is safe
// even though no single comparison node wraps the load. This exercises
// path-sensitive, short-circuit-aware verification.
func TestShortCircuitGuardDominatesLoad(t *testing.T) {
	rule, err := Parse("sc", "len >= 8 and u32(4) == 9 and u16(0) == 1")
	if err != nil {
		t.Fatal(err)
	}
	prog, err := Compile(rule)
	if err != nil {
		t.Fatalf("short-circuit guard rejected: %v", err)
	}
	// Every length below the guard must run without a runtime trap.
	for n := 0; n < 8; n++ {
		if _, err := prog.Execute(make([]byte, n)); err != nil {
			t.Fatalf("trap on short packet len=%d: %v", n, err)
		}
	}
	// A packet that satisfies every term accepts.
	good := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x09}
	if ok, _ := prog.Execute(good); !ok {
		t.Fatal("satisfying packet should accept")
	}
}

// A guard nested only inside one OR branch must not leak to the other
// branch's load at the confluence.
func TestGuardDoesNotLeakAcrossBranches(t *testing.T) {
	// First branch proves len>=8; second branch reads a word immediately.
	rule, err := Parse("leak", "(len >= 8 and u8(0) == 1) or u32(0) == 2")
	if err != nil {
		t.Fatal(err)
	}
	_, cerr := Compile(rule)
	re, ok := AsReject(cerr)
	if !ok || re.Kind != RejectUnverifiedLoad {
		t.Fatalf("got %v, want unverified load on unguarded OR branch", cerr)
	}
}

func TestAllJumpsAreForward(t *testing.T) {
	rule, err := Parse("r", "len >= 8 and (u32(0) == 1 or u8(4) == 2)")
	if err != nil {
		t.Fatal(err)
	}
	prog, err := Compile(rule)
	if err != nil {
		t.Fatal(err)
	}
	for i, in := range prog.Insns {
		switch in.Op {
		case OpJmp, OpJmpIf:
			if in.Off == 0 || i+int(in.Off) <= i {
				t.Fatalf("instruction %d has non-forward jump", i)
			}
			if i+int(in.Off) >= len(prog.Insns) {
				t.Fatalf("instruction %d jumps past end", i)
			}
		}
	}
}
