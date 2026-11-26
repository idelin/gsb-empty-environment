package seccomp

import "testing"

// X-register source comparison must be modeled symbolically.
func TestXSourceComparison(t *testing.T) {
	insns := []Instruction{
		Stmt(ClassLDX|SizeW|ModeIMM, 59),    // X = 59
		Stmt(ClassLD|SizeW|ModeABS, OffNR),  // A = nr
		Jump(JmpJEQ|SrcX|ClassJMP, 0, 0, 1), // jeq X ->3 kill else4 allow
		Stmt(opRetK, RetKillThread),
		Stmt(opRetK, RetAllow),
	}
	pol := execvePolicy()
	rep, err := Verify(insns, pol)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe || rep.Minimal.Tuple.Arch != ArchI386 {
		t.Fatalf("X-source filter minimal=%v safe=%v, want i386 bypass", rep.Minimal, rep.Safe)
	}
}

// Programs exercising MSH / IND / LEN must analyze without error, and every
// reported witness must still reproduce concretely.
func TestExoticAddressingNoFalsePositive(t *testing.T) {
	insns := []Instruction{
		Stmt(ClassLDX|SizeB|ModeMSH, OffNR), // X = 4*(nr_byte0 & 0xf)
		Stmt(ClassLD|SizeW|ModeIND, 0),      // A = seccomp[X+0] (symbolic idx -> opaque)
		Stmt(ClassALU|AluXOR|SrcK, 0),
		Stmt(ClassLD|SizeW|ModeLEN, 0),      // A = len (modeled 0)
		Stmt(ClassALU|AluOR|SrcK, RetAllow), // A = ALLOW
		retA(),
	}
	if err := Validate(insns); err != nil {
		t.Fatalf("exotic fixture should validate: %v", err)
	}
	pol := NewPolicy(Deny("x", []Arch{ArchX8664}, []uint32{59}))
	rep, err := Verify(insns, pol)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Counterexamples {
		act, err := RunTuple(insns, c.Tuple)
		if err != nil {
			t.Fatal(err)
		}
		if !IsLaxAction(act, false) {
			t.Fatalf("false positive %s -> %s", c.Tuple, ActionName(act))
		}
	}
}

// Two layers returning dynamic RET A with constant ALLOW must be solved by
// the stack enumeration, exercising opaque-id remapping.
func TestStackDynamicReturnLayers(t *testing.T) {
	dynAllow := []Instruction{
		Stmt(ClassLD|SizeW|ModeIMM, RetAllow),
		retA(),
	}
	pol := NewPolicy(Deny("x", []Arch{ArchX8664}, []uint32{59}))
	rep, err := VerifyStack([][]Instruction{dynAllow, dynAllow}, pol)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe {
		t.Fatal("both dynamic-ALLOW layers should yield a stack bypass")
	}
	if len(rep.Minimal.LayerActions) != 2 {
		t.Fatalf("layer actions %v", rep.Minimal.LayerActions)
	}
}

func TestParseArchAndNames(t *testing.T) {
	for _, s := range []string{"x86_64", "amd64", "i386", "arm64", "x32", "aarch64"} {
		if _, err := ParseArch(s); err != nil {
			t.Fatalf("ParseArch(%q): %v", s, err)
		}
	}
	if _, err := ParseArch("mips"); err == nil {
		t.Fatal("expected unsupported arch error")
	}
	if ArchAARCH64.AuditArch() != AuditArchAARCH64 {
		t.Fatal("aarch64 audit const")
	}
	if ArchX32.EncodeNR(59) != 59|X32SyscallBit {
		t.Fatal("x32 nr encoding")
	}
}

func TestVerifyBytesConvenience(t *testing.T) {
	raw := EncodeAll(nr59OnlyFilter())
	pol := NewPolicy(Deny("i", []Arch{ArchI386}, []uint32{11}))
	rep, err := VerifyBytes(raw, pol)
	if err != nil || rep.Safe {
		t.Fatalf("VerifyBytes safe=%v err=%v", rep.Safe, err)
	}
	if _, err := VerifyBytes([]byte{1, 2, 3}, pol); err == nil {
		t.Fatal("short raw should error")
	}
}

func TestDenyNamedTable(t *testing.T) {
	rules := DenyNamed("execve", AllArchs)
	if len(rules) != 4 {
		t.Fatalf("want 4 per-arch rules, got %d", len(rules))
	}
	pol := NewPolicy(rules...)
	rep, err := Verify(nr59OnlyFilter(), pol)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe || rep.Minimal.Tuple.Arch != ArchI386 {
		t.Fatalf("named policy minimal=%v safe=%v", rep.Minimal, rep.Safe)
	}
	if nr := MustResolveSyscall("bpf", ArchI386); nr != SysBpfI386 {
		t.Fatalf("i386 bpf=%d", nr)
	}
}
