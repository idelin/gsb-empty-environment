package seccomp

import (
	"math/rand"
	"testing"
)

func TestCrossCheckArchConfusion(t *testing.T) {
	dom := EnumDomain{
		Archs: AllArchs,
		NRs:   []uint32{59, SysExecveI386, SysExecveAARCH64, SysExecveX8664 | X32SyscallBit},
	}
	res, err := ExhaustiveCrossCheck(nr59OnlyFilter(), execvePolicy(), dom)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Agree {
		t.Fatalf("cross-check disagreement: %s", res.Detail)
	}
	if !res.EnumFindsBypass || !res.SymbolicFinds {
		t.Fatalf("expected bypass both ways, enum=%v symbolic=%v", res.EnumFindsBypass, res.SymbolicFinds)
	}
}

func TestCrossCheckHighDword(t *testing.T) {
	pol := &Policy{Rules: []Rule{
		Deny("bpf-marker", []Arch{ArchX8664}, []uint32{SysBpfX8664},
			ArgCond{Index: 1, Mask: 0xffffffff00000000, Value: 0xdeadbeef00000000}),
	}}
	insns := []Instruction{
		Stmt(opLdNR, OffNR),
		Jump(JmpJEQ|SrcK|ClassJMP, SysBpfX8664, 0, 3),
		Stmt(ClassLD|SizeW|ModeABS, uint32(OffArg0+8)),
		Jump(JmpJEQ|SrcK|ClassJMP, 0x1234, 0, 1),
		Stmt(opRetK, RetKillThread),
		Stmt(opRetK, RetAllow),
	}
	dom := EnumDomain{
		Archs: []Arch{ArchX8664},
		NRs:   []uint32{SysBpfX8664},
		ArgValues: [ArgCount][]uint64{
			0: {0},
			1: {0, 0x1234, 0xdeadbeef00000000, 0xdeadbeef00001234, 0x0000000100001234},
		},
	}
	res, err := ExhaustiveCrossCheck(insns, pol, dom)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Agree {
		t.Fatalf("cross-check disagreement: %s", res.Detail)
	}
}

func TestCrossCheckStrictSafe(t *testing.T) {
	dom := EnumDomain{
		Archs: AllArchs,
		NRs:   []uint32{59, 11, 221, 59 | X32SyscallBit},
		ArgValues: [ArgCount][]uint64{
			0: {0, 1, 0x1234, 0x80000000, 0xffffffff},
			1: {0, 0xff, 0xdeadbeef00000000},
		},
	}
	res, err := ExhaustiveCrossCheck(strictExecveFilter(t), execvePolicy(), dom)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Agree {
		t.Fatalf("strict filter cross-check: %s", res.Detail)
	}
	if res.EnumFindsBypass {
		t.Fatalf("strict filter unexpectedly has %d enumerated bypasses", len(res.EnumBypasses))
	}
}

// TestDifferentialEnumeration runs a deterministic randomized family of
// valid seccomp-style comparison cascades and checks the sound implication
// "small-domain enumeration finds a bypass => symbolic verifier finds one",
// plus concrete reproducibility of every symbolic witness.
func TestDifferentialEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(0xC0FFEE))
	argCandidates := []uint64{
		0, 1, 2, 3, 0xff, 0x1234, 0xbeef,
		0x80000000, 0xffffffff,
		0x100000000, 0xdeadbeef00000000, 0xdeadbeef00000001,
	}
	for iter := 0; iter < 160; iter++ {
		activeArg := rng.Intn(ArgCount)
		insns, nr, argIndex, mask, want := genCascadeFilter(t, rng, activeArg)

		conds := []ArgCond{}
		if rng.Intn(2) == 0 {
			conds = append(conds, ArgCond{Index: argIndex, Mask: mask, Value: want})
		}
		pol := &Policy{Rules: []Rule{
			Deny("rand", []Arch{ArchX8664}, []uint32{nr}, conds...),
		}}

		rep, err := Verify(insns, pol)
		if err != nil {
			t.Fatalf("iter %d verify: %v\n%s", iter, err, Disassemble(insns))
		}
		for _, c := range rep.Counterexamples {
			act, err := RunTuple(insns, c.Tuple)
			if err != nil {
				t.Fatalf("iter %d witness run: %v", iter, err)
			}
			if !IsLaxAction(act, false) || pol.MatchesAnyRule(c.Tuple) == nil {
				t.Fatalf("iter %d false-positive witness %s act=%s", iter, c.Tuple, ActionName(act))
			}
		}

		dom := EnumDomain{
			Archs: []Arch{ArchX8664},
			NRs:   []uint32{nr},
		}
		dom.ArgValues[activeArg] = argCandidates
		hits, _, err := Enumerate(insns, pol, dom)
		if err != nil {
			t.Fatalf("iter %d enum: %v", iter, err)
		}
		if len(hits) > 0 && rep.Complete && rep.Safe {
			t.Fatalf("iter %d symbolic missed enum bypass %s\nfilter:\n%s",
				iter, hits[0].Tuple, Disassemble(insns))
		}
	}
}

// genCascadeFilter builds a valid libseccomp-like cascade over nr/arch/arg.
func genCascadeFilter(t *testing.T, rng *rand.Rand, activeArg int) (insns []Instruction, nr uint32, argIndex int, mask uint64, want uint64) {
	t.Helper()
	a := newAsm()
	nr = []uint32{59, 101, 321, uint32(rng.Intn(500))}[rng.Intn(4)]
	block := []uint32{RetKillThread, RetErrno | 1, RetTrap}[rng.Intn(3)]

	a.stmt(opLdNR, OffNR)
	a.jeqK(nr, "check", "allow")
	a.label("check")

	argIndex = activeArg
	wordOff := uint32(OffArg0 + ArgStride*argIndex)
	high := rng.Intn(2) == 0
	if high {
		wordOff += 4
	}
	a.stmt(ClassLD|SizeW|ModeABS, wordOff)

	switch rng.Intn(4) {
	case 0: // no mask
	case 1:
		m := []uint32{0xff, 0xffff, 0xffffff00, 0x80000000, 0x40000000}[rng.Intn(5)]
		a.stmt(ClassALU|AluAND|SrcK, m)
	case 2:
		a.stmt(ClassALU|AluRSH|SrcK, uint32(rng.Intn(31)+1))
	case 3:
		a.stmt(ClassALU|AluAND|SrcK, 0xffff)
		a.stmt(ClassALU|AluRSH|SrcK, uint32(rng.Intn(8)+1))
	}
	// Occasionally mix in invertible / arithmetic ops for domain coverage.
	switch rng.Intn(5) {
	case 0:
		a.stmt(ClassALU|AluXOR|SrcK, []uint32{0, 1, 0xff, 0x40000000}[rng.Intn(4)])
	case 1:
		a.stmt(ClassALU|AluOR|SrcK, []uint32{0, 1, 0x100}[rng.Intn(3)])
	case 2:
		a.stmt(ClassALU|AluADD|SrcK, uint32(rng.Intn(4)))
	case 3:
		a.stmt(ClassALU|AluSUB|SrcK, uint32(rng.Intn(4)))
	}

	cmpVal := uint32(rng.Intn(8))
	switch rng.Intn(3) {
	case 0:
		a.jeqK(cmpVal, "block", "allow")
	case 1:
		a.jgeK(cmpVal, "block", "allow")
	default:
		a.jsetK([]uint32{1, 0xff, 0x80000000}[rng.Intn(3)], "block", "allow")
	}

	a.label("block")
	a.stmt(opRetK, block)
	a.label("allow")
	a.stmt(opRetK, RetAllow)
	insns = a.build(t)

	// Policy condition independent-ish: full 64-bit mask with random want.
	mask = []uint64{^uint64(0), 0xff, 0xffffffff00000000, 0xffff}[rng.Intn(4)]
	want = rng.Uint64() & mask
	return
}
