package seccomp

import "testing"

func execvePolicy() *Policy {
	return &Policy{Rules: []Rule{
		Deny("execve-x86_64-x32", []Arch{ArchX8664, ArchX32}, []uint32{SysExecveX8664}),
		Deny("execve-i386", []Arch{ArchI386}, []uint32{SysExecveI386}),
		Deny("execve-aarch64", []Arch{ArchAARCH64}, []uint32{SysExecveAARCH64}),
	}}
}

// Vulnerable filter: only compares nr == 59, with no arch validation.
func nr59OnlyFilter() []Instruction {
	return []Instruction{
		Stmt(opLdNR, OffNR),
		Jump(JmpJEQ|SrcK|ClassJMP, SysExecveX8664, 0, 1),
		Stmt(opRetK, RetKillThread),
		Stmt(opRetK, RetAllow),
	}
}

func TestBypassArchConfusion(t *testing.T) {
	insns := nr59OnlyFilter()
	rep, err := Verify(insns, execvePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe {
		t.Fatalf("expected arch-confusion bypass, got safe (complete=%v)", rep.Complete)
	}
	if !rep.Complete {
		t.Fatal("exploration should be complete for tiny filter")
	}
	m := rep.Minimal
	if m.Tuple.Arch != ArchI386 || m.Tuple.NR != SysExecveI386 {
		t.Fatalf("minimal witness = %s, want i386/%d", m.Tuple, SysExecveI386)
	}
	assertWitnessReproduces(t, insns, m)

	var sawARM64 bool
	for _, c := range rep.Counterexamples {
		assertWitnessReproduces(t, insns, c)
		if c.Tuple.Arch == ArchAARCH64 && c.Tuple.NR == SysExecveAARCH64 {
			sawARM64 = true
		}
	}
	if !sawARM64 {
		t.Fatalf("expected aarch64/%d confusion witness too", SysExecveAARCH64)
	}
}

// Vulnerable filter: validates x86_64 arch but compares the raw nr without
// masking __X32_SYSCALL_BIT.
func x32VulnerableFilter(t *testing.T) []Instruction {
	a := newAsm()
	a.stmt(opLdArch, OffArch)
	a.jeqK(AuditArchX8664, "nrchk", "allow")
	a.label("nrchk")
	a.stmt(opLdNR, OffNR)
	a.jeqK(SysExecveX8664, "kill", "allow")
	a.label("kill")
	a.stmt(opRetK, RetKillThread)
	a.label("allow")
	a.stmt(opRetK, RetAllow)
	return a.build(t)
}

func TestBypassX32Bit30(t *testing.T) {
	insns := x32VulnerableFilter(t)
	pol := &Policy{Rules: []Rule{
		Deny("execve", []Arch{ArchX8664, ArchX32}, []uint32{SysExecveX8664}),
	}}
	rep, err := Verify(insns, pol)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe {
		t.Fatal("expected x32 bit-30 bypass")
	}
	m := rep.Minimal
	if m.Tuple.Arch != ArchX32 {
		t.Fatalf("arch = %s, want x32", m.Tuple.Arch)
	}
	if m.Tuple.NR != SysExecveX8664|X32SyscallBit {
		t.Fatalf("nr = %#x, want %#x", m.Tuple.NR, SysExecveX8664|X32SyscallBit)
	}
	assertWitnessReproduces(t, insns, m)

	// The concrete x32 tuple must really be allowed by the filter.
	act, _ := RunTuple(insns, m.Tuple)
	if act&RetActionFull != RetAllow {
		t.Fatalf("x32 tuple returned %s, want ALLOW", ActionName(act))
	}
}

// Fixed filter masks off bit 30 before the nr comparison and must be safe.
func x32FixedFilter(t *testing.T) []Instruction {
	a := newAsm()
	a.stmt(opLdArch, OffArch)
	a.jeqK(AuditArchX8664, "nrchk", "allow")
	a.label("nrchk")
	a.stmt(opLdNR, OffNR)
	a.stmt(ClassALU|AluAND|SrcK, ^X32SyscallBit) // nr & 0xbfffffff
	a.jeqK(SysExecveX8664, "kill", "allow")
	a.label("kill")
	a.stmt(opRetK, RetKillThread)
	a.label("allow")
	a.stmt(opRetK, RetAllow)
	return a.build(t)
}

func TestX32MaskFixIsSafe(t *testing.T) {
	insns := x32FixedFilter(t)
	pol := &Policy{Rules: []Rule{
		Deny("execve", []Arch{ArchX8664, ArchX32}, []uint32{SysExecveX8664}),
	}}
	rep, err := Verify(insns, pol)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Safe {
		t.Fatalf("masked filter should be safe, got %s", rep.Minimal.Tuple)
	}
}

func TestBypassHighDwordArgument(t *testing.T) {
	// Host forbids bpf when arg1 carries the 0xdeadbeef high marker.
	pol := &Policy{Rules: []Rule{
		Deny("bpf-marker", []Arch{ArchX8664}, []uint32{SysBpfX8664},
			ArgCond{Index: 1, Mask: 0xffffffff00000000, Value: 0xdeadbeef00000000}),
	}}
	// Filter only inspects the low 32 bits of arg1 and blocks 0x1234.
	insns := []Instruction{
		Stmt(opLdNR, OffNR),
		Jump(JmpJEQ|SrcK|ClassJMP, SysBpfX8664, 0, 3),
		Stmt(ClassLD|SizeW|ModeABS, uint32(OffArg0+8)), // arg1 low word
		Jump(JmpJEQ|SrcK|ClassJMP, 0x1234, 0, 1),
		Stmt(opRetK, RetKillThread),
		Stmt(opRetK, RetAllow),
	}
	rep, err := Verify(insns, pol)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Safe {
		t.Fatal("expected high-dword bypass")
	}
	want := uint64(0xdeadbeef) << 32
	if got := rep.Minimal.Tuple.Args[1]; got != want {
		t.Fatalf("arg1 = %#x, want minimal %#x; tuple=%s", got, want, rep.Minimal.Tuple)
	}
	assertWitnessReproduces(t, insns, rep.Minimal)
}

// A comprehensive strict filter handling every target arch and the x32 bit.
func strictExecveFilter(t *testing.T) []Instruction {
	return strictExecveFilterWithKill(t, RetKillProcess)
}

func strictExecveFilterThread(t *testing.T) []Instruction {
	return strictExecveFilterWithKill(t, RetKillThread)
}

func strictExecveFilterWithKill(t *testing.T, killAction uint32) []Instruction {
	a := newAsm()
	a.stmt(opLdArch, OffArch)
	a.jeqK(AuditArchX8664, "x64", "try386")
	a.label("try386")
	a.jeqK(AuditArchI386, "i386", "tryarm")
	a.label("tryarm")
	a.jeqK(AuditArchAARCH64, "arm", "kill")

	a.label("x64")
	a.stmt(opLdNR, OffNR)
	a.stmt(ClassALU|AluAND|SrcK, ^X32SyscallBit)
	a.jeqK(SysExecveX8664, "kill", "allow")

	a.label("i386")
	a.stmt(opLdNR, OffNR)
	a.jeqK(SysExecveI386, "kill", "allow")

	a.label("arm")
	a.stmt(opLdNR, OffNR)
	a.jeqK(SysExecveAARCH64, "kill", "allow")

	a.label("kill")
	a.stmt(opRetK, killAction)
	a.label("allow")
	a.stmt(opRetK, RetAllow)
	return a.build(t)
}

func TestStrictCrossArchFilterSafe(t *testing.T) {
	insns := strictExecveFilter(t)
	rep, err := Verify(insns, execvePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Complete {
		t.Fatal("exploration incomplete")
	}
	if !rep.Safe {
		t.Fatalf("strict filter should be safe, got bypass %s", rep.Minimal.Tuple)
	}
}

func assertWitnessReproduces(t *testing.T, insns []Instruction, c *Counterexample) {
	t.Helper()
	if c == nil {
		t.Fatal("nil counterexample")
	}
	act, err := RunTuple(insns, c.Tuple)
	if err != nil {
		t.Fatalf("witness concrete run error: %v", err)
	}
	if !IsLaxAction(act, false) {
		t.Fatalf("witness %s does not reproduce ALLOW, got %s", c.Tuple, ActionName(act))
	}
}
