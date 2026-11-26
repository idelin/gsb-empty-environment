package seccomp

import "testing"

func retA() Instruction { return Stmt(ClassRET|RetA, 0) }

func runA(t *testing.T, insns ...Instruction) uint32 {
	t.Helper()
	insns = append(insns, retA())
	got, err := Run(insns, &Data{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return got
}

func TestEmulateALU(t *testing.T) {
	if got := runA(t, Stmt(0x00, 10), Stmt(ClassALU|AluADD|SrcK, 5)); got != 15 {
		t.Fatalf("add=%d", got)
	}
	if got := runA(t, Stmt(0x00, 0), Stmt(ClassALU|AluSUB|SrcK, 1)); got != 0xffffffff {
		t.Fatalf("sub wrap=%#x", got)
	}
	if got := runA(t, Stmt(0x00, 6), Stmt(ClassALU|AluMUL|SrcK, 7)); got != 42 {
		t.Fatalf("mul=%d", got)
	}
	if got := runA(t, Stmt(0x00, 0xff), Stmt(ClassALU|AluAND|SrcK, 0x0f)); got != 0x0f {
		t.Fatalf("and=%#x", got)
	}
	if got := runA(t, Stmt(0x00, 1), Stmt(ClassALU|AluLSH|SrcK, 30)); got != 0x40000000 {
		t.Fatalf("lsh=%#x", got)
	}
	if got := runA(t, Stmt(0x00, 0x80000000), Stmt(ClassALU|AluRSH|SrcK, 31)); got != 1 {
		t.Fatalf("rsh=%#x", got)
	}
	if got := runA(t, Stmt(0x00, 5), Stmt(ClassALU|AluNEG, 0)); got != ^uint32(4) {
		t.Fatalf("neg=%#x", got)
	}
	if got := runA(t, Stmt(0x00, 10), Stmt(ClassALU|AluMOD|SrcK, 3)); got != 1 {
		t.Fatalf("mod=%d", got)
	}
	if got := runA(t, Stmt(0x00, 12), Stmt(ClassALU|AluDIV|SrcK, 4)); got != 3 {
		t.Fatalf("div=%d", got)
	}
}

func TestEmulateXAndMiscAndMem(t *testing.T) {
	// X = 7; A = 3; TAX makes X=A; ST X? exercise TXA and MEM.
	prog := []Instruction{
		Stmt(ClassLDX|SizeW|ModeIMM, 7), // ldx 7
		Stmt(ClassLD|SizeW|ModeIMM, 3),  // ld 3
		Stmt(ClassMISC|MiscTAX, 0),      // tax X=A=3
		Stmt(ClassSTX, 2),               // M[2]=X
		Stmt(ClassLD|SizeW|ModeMEM, 2),  // A=M[2]
		retA(),
	}
	got, err := Run(prog, &Data{})
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Fatalf("mem/tax roundtrip=%d want3", got)
	}

	// TXA path.
	prog2 := []Instruction{
		Stmt(ClassLDX|SizeW|ModeIMM, 9),
		Stmt(ClassMISC|MiscTXA, 0),
		retA(),
	}
	if got := runProg(t, prog2); got != 9 {
		t.Fatalf("txa=%d want9", got)
	}
}

func runProg(t *testing.T, insns []Instruction) uint32 {
	t.Helper()
	got, err := Run(insns, &Data{})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestEmulateDivZeroXKills(t *testing.T) {
	prog := []Instruction{
		Stmt(ClassLD|SizeW|ModeIMM, 42),
		Stmt(ClassLDX|SizeW|ModeIMM, 0),
		Stmt(ClassALU|AluDIV|SrcX, 0),
		Stmt(opRetK, RetAllow),
	}
	got, err := Run(prog, &Data{})
	if err != nil {
		t.Fatal(err)
	}
	if got != RetKillThread {
		t.Fatalf("div by X=0 should return 0 KILL, got %#x", got)
	}
}

func TestEmulateSeccompDataWords(t *testing.T) {
	d := &Data{
		NR:   SysExecveX8664 | X32SyscallBit,
		Arch: AuditArchX8664,
		Args: [6]uint64{0x1122334455667788},
	}
	// Load arch then return A.
	archProg := []Instruction{Stmt(ClassLD|SizeW|ModeABS, OffArch), retA()}
	if got, _ := Run(archProg, d); got != AuditArchX8664 {
		t.Fatalf("arch=%#x", got)
	}
	// arg0 low word offset16 must be 0x55667788 (LE low half).
	loProg := []Instruction{Stmt(ClassLD|SizeW|ModeABS, uint32(OffArg0)), retA()}
	if got, _ := Run(loProg, d); got != 0x55667788 {
		t.Fatalf("arg0 lo=%#x", got)
	}
	hiProg := []Instruction{Stmt(ClassLD|SizeW|ModeABS, uint32(OffArg0+4)), retA()}
	if got, _ := Run(hiProg, d); got != 0x11223344 {
		t.Fatalf("arg0 hi=%#x", got)
	}
}

func TestEmulateJumps(t *testing.T) {
	// JA skips the KILL.
	prog := []Instruction{
		{Code: ClassJMP | JmpJA, K: 1}, // pc0 -> pc2
		Stmt(opRetK, RetKillThread),    // pc1 skipped
		Stmt(opRetK, RetAllow),         // pc2
	}
	if got, _ := Run(prog, &Data{}); got&RetActionFull != RetAllow {
		t.Fatalf("ja did not skip: %s", ActionName(got))
	}
}
