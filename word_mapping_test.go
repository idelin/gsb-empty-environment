package seccomp

import "testing"

// A filter that correctly compares the HIGH word of arg1 against the
// forbidden marker must be Safe (proves high-word offset mapping).
func TestHighWordCorrectCheckSafe(t *testing.T) {
	pol := NewPolicy(Deny("bpf-marker", []Arch{ArchX8664}, []uint32{SysBpfX8664},
		ArgCond{Index: 1, Mask: 0xffffffff00000000, Value: 0xdeadbeef00000000}))
	insns := []Instruction{
		Stmt(opLdNR, OffNR),
		Jump(JmpJEQ|SrcK|ClassJMP, SysBpfX8664, 0, 4),    // 1 ->2 or 6
		Stmt(ClassLD|SizeW|ModeABS, uint32(OffArg0+8+4)), // 2 arg1 hi = 28
		Jump(JmpJEQ|SrcK|ClassJMP, 0xdeadbeef, 0, 1),     // 3 ->4 kill or 5 allow
		Stmt(opRetK, RetKillThread),                      // 4
		Stmt(opRetK, RetAllow),                           // 5
		Stmt(opRetK, RetAllow),                           // 6
	}
	rep, err := Verify(insns, pol)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Safe {
		t.Fatalf("correct high-word check should be safe, got %s", rep.Minimal.Tuple)
	}
}

// A filter testing only the low byte while the policy forbids a high-bit
// pattern yields the minimal high-word witness.
func TestByteLoadAndWitness(t *testing.T) {
	pol := NewPolicy(Deny("ptrace", []Arch{ArchX8664}, []uint32{SysPtraceX8664},
		ArgCond{Index: 2, Mask: 0xff00, Value: 0x4200}))
	// Filter kills only when byte 0 of arg2 equals 0x7.
	insns := []Instruction{
		Stmt(opLdNR, OffNR),
		Jump(JmpJEQ|SrcK|ClassJMP, SysPtraceX8664, 0, 3),
		Stmt(ClassLD|SizeB|ModeABS, uint32(OffArg0+16)), // arg2 byte0 offset32
		Jump(JmpJEQ|SrcK|ClassJMP, 0x07, 0, 1),
		Stmt(opRetK, RetKillThread),
		Stmt(opRetK, RetAllow),
	}
	rep, err := Verify(insns, pol)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe {
		t.Fatal("expected mask-condition bypass")
	}
	// Minimal arg2 satisfying (arg2 & 0xff00)==0x4200 is exactly 0x4200;
	// its low byte is 0x00 != 0x07 so the filter allows it.
	if got := rep.Minimal.Tuple.Args[2]; got != 0x4200 {
		t.Fatalf("arg2=%#x want 0x4200 (tuple %s)", got, rep.Minimal.Tuple)
	}
	act, _ := RunTuple(insns, rep.Minimal.Tuple)
	if act&RetActionFull != RetAllow {
		t.Fatalf("witness action %s", ActionName(act))
	}
}
