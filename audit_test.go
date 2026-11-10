package fwaudit

import (
	"bytes"
	"math/rand"
	"testing"
)

// The path to the load requires in[0]&0xF0 != 0 and (in[0]&0x0F) >= 2,
// so the lexicographically smallest replay input is [0x12].
func TestLexicographicMinimalInput(t *testing.T) {
	p := &Program{
		NRegs:  2,
		Entry:  0,
		Arrays: [][]uint8{{10, 20}},
		Instrs: []Instr{
			{Op: OpInput, Dst: 0, Pos: 0, Next: [2]int{1, Halt}},
			{Op: OpAnd, Dst: 0, Imm: 0xF0, Next: [2]int{2, Halt}},
			{Op: OpJnz, Src: 0, Next: [2]int{Halt, 3}},
			{Op: OpInput, Dst: 1, Pos: 0, Next: [2]int{4, Halt}},
			{Op: OpAnd, Dst: 1, Imm: 0x0F, Next: [2]int{5, Halt}},
			{Op: OpLoad, Dst: 0, Src: 1, Arr: 0, Next: [2]int{Halt, Halt}},
		},
	}
	rep, err := Audit(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(rep.Findings))
	}
	f := rep.Findings[0]
	if f.Instr != 5 {
		t.Errorf("finding at instr %d, want 5", f.Instr)
	}
	if want := []uint8{0x12}; !bytes.Equal(f.Input, want) {
		t.Errorf("input = %x, want %x", f.Input, want)
	}
	if f.Index != 2 {
		t.Errorf("index = %d, want 2", f.Index)
	}
	oob, err := Run(p, f.Input)
	if err != nil {
		t.Fatal(err)
	}
	if oob == nil || oob.Instr != 5 || oob.Index != 2 {
		t.Errorf("replay = %+v, want OOB at instr 5 index 2", oob)
	}
}

// The load is only reachable when in[0]&0x80 != 0 AND in[0]&0x80 == 0.
// The two reads of input byte 0 must agree, so the path is infeasible and
// no finding may be reported.
func TestRepeatedReadConsistency(t *testing.T) {
	p := &Program{
		NRegs:  2,
		Entry:  0,
		Arrays: [][]uint8{{7}},
		Instrs: []Instr{
			{Op: OpInput, Dst: 0, Pos: 0, Next: [2]int{1, Halt}},
			{Op: OpAnd, Dst: 0, Imm: 0x80, Next: [2]int{2, Halt}},
			{Op: OpJnz, Src: 0, Next: [2]int{Halt, 3}},
			{Op: OpInput, Dst: 1, Pos: 0, Next: [2]int{4, Halt}},
			{Op: OpAnd, Dst: 1, Imm: 0x80, Next: [2]int{5, Halt}},
			{Op: OpJnz, Src: 1, Next: [2]int{6, Halt}},
			{Op: OpLoad, Dst: 1, Src: 0, Arr: 0, Next: [2]int{Halt, Halt}},
		},
	}
	rep, err := Audit(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("want no findings, got %+v", rep.Findings)
	}
	// Cross-check by exhaustive simulation.
	for x := 0; x < 256; x++ {
		oob, err := Run(p, []uint8{uint8(x)})
		if err != nil {
			t.Fatal(err)
		}
		if oob != nil {
			t.Fatalf("input %02x produced OOB %+v, want none", x, oob)
		}
	}
}

// A load guarded by an earlier in-bounds load: the second load is only
// reachable when the first index is in bounds.
func TestChainedLoads(t *testing.T) {
	// arr0 = [0, 1]: in[0]&1 indexes arr0; arr1 has length 1, so indexing
	// it with the loaded value is OOB exactly when in[0] is odd.
	p := &Program{
		NRegs:  2,
		Entry:  0,
		Arrays: [][]uint8{{0, 1}, {0}},
		Instrs: []Instr{
			{Op: OpInput, Dst: 0, Pos: 0, Next: [2]int{1, Halt}},
			{Op: OpAnd, Dst: 0, Imm: 1, Next: [2]int{2, Halt}},
			{Op: OpLoad, Dst: 1, Src: 0, Arr: 0, Next: [2]int{3, Halt}},
			{Op: OpLoad, Dst: 0, Src: 1, Arr: 1, Next: [2]int{Halt, Halt}},
		},
	}
	rep, err := Audit(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Instr != 3 {
		t.Fatalf("findings = %+v, want one at instr 3", rep.Findings)
	}
	if want := []uint8{1}; !bytes.Equal(rep.Findings[0].Input, want) {
		t.Errorf("input = %x, want %x", rep.Findings[0].Input, want)
	}
}

// bruteForce enumerates all inputs of length nPos in lexicographic order
// and records the first input faulting at each instruction.
func bruteForce(t *testing.T, p *Program, nPos int) map[int][]uint8 {
	t.Helper()
	total := 1 << (8 * nPos)
	best := make(map[int][]uint8)
	for i := 0; i < total; i++ {
		in := make([]uint8, nPos)
		for j := 0; j < nPos; j++ {
			in[j] = uint8(i >> (8 * (nPos - 1 - j)))
		}
		oob, err := Run(p, in)
		if err != nil {
			t.Fatalf("Run(%x): %v", in, err)
		}
		if oob != nil {
			if _, seen := best[oob.Instr]; !seen {
				best[oob.Instr] = in
			}
		}
	}
	return best
}

func genProgram(r *rand.Rand) (*Program, int) {
	n := 4 + r.Intn(6)     // instructions
	nRegs := 2 + r.Intn(2) // registers
	nPos := 1 + r.Intn(2)  // input bytes
	nArr := 1 + r.Intn(2)  // arrays
	arrays := make([][]uint8, nArr)
	for i := range arrays {
		a := make([]uint8, 1+r.Intn(4))
		for j := range a {
			a[j] = uint8(r.Intn(256))
		}
		arrays[i] = a
	}
	fwd := func(i int) int {
		if i == n-1 || r.Intn(5) == 0 {
			return Halt
		}
		return i + 1 + r.Intn(n-1-i)
	}
	instrs := make([]Instr, n)
	for i := range instrs {
		var ins Instr
		switch r.Intn(10) {
		case 0, 1:
			ins = Instr{Op: OpInput, Dst: r.Intn(nRegs), Pos: r.Intn(nPos)}
		case 2, 3:
			ins = Instr{Op: OpAnd, Dst: r.Intn(nRegs), Imm: uint8(r.Intn(256))}
		case 4, 5:
			ins = Instr{Op: OpAdd, Dst: r.Intn(nRegs), Imm: uint8(r.Intn(256))}
		case 6, 7:
			ins = Instr{Op: OpJnz, Src: r.Intn(nRegs)}
			ins.Next[1] = fwd(i)
		default:
			ins = Instr{Op: OpLoad, Dst: r.Intn(nRegs), Src: r.Intn(nRegs), Arr: r.Intn(nArr)}
		}
		ins.Next[0] = fwd(i)
		if ins.Op != OpJnz {
			ins.Next[1] = Halt
		}
		instrs[i] = ins
	}
	return &Program{NRegs: nRegs, Entry: 0, Instrs: instrs, Arrays: arrays}, nPos
}

// TestExhaustiveRandom compares the auditor against exhaustive input
// enumeration on random short programs: identical vulnerable sites, and
// identical minimal replay inputs.
func TestExhaustiveRandom(t *testing.T) {
	const seeds = 1000
	for seed := int64(0); seed < seeds; seed++ {
		r := rand.New(rand.NewSource(seed))
		p, nPos := genProgram(r)
		rep, err := Audit(p)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if rep.Truncated {
			t.Fatalf("seed %d: path cap hit", seed)
		}
		want := bruteForce(t, p, nPos)
		if len(rep.Findings) != len(want) {
			t.Fatalf("seed %d: auditor found %d sites, brute force %d\nfindings: %+v",
				seed, len(rep.Findings), len(want), rep.Findings)
		}
		for _, f := range rep.Findings {
			w, ok := want[f.Instr]
			if !ok {
				t.Fatalf("seed %d: auditor reports instr %d, brute force does not", seed, f.Instr)
			}
			padded := make([]uint8, nPos)
			copy(padded, f.Input)
			if !bytes.Equal(padded, w) {
				t.Fatalf("seed %d: instr %d: auditor input %x, brute force %x", seed, f.Instr, padded, w)
			}
			// The reported input must actually replay the fault.
			oob, err := Run(p, f.Input)
			if err != nil {
				t.Fatalf("seed %d: replay %x: %v", seed, f.Input, err)
			}
			if oob == nil || oob.Instr != f.Instr {
				t.Fatalf("seed %d: replay %x gave %+v, want OOB at instr %d", seed, f.Input, oob, f.Instr)
			}
		}
	}
}
