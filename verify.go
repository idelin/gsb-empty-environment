package netsec

import (
	"fmt"
	"slices"
)

// This file is an independent re-implementation used only to
// cross-check Analyze. The interpreter models a skipped instruction by
// rewriting a cloned program with an internal nop, and guard insertion
// is written separately from InsertGuards, so a bug in the primary
// implementation cannot silently propagate here.

// opNop exists only inside the verifier to model a skipped instruction.
const opNop Op = -1

type vmResult struct {
	halted      bool
	unlocks     int
	firstUnlock int
}

// vmExecute is the verifier's interpreter. It shares no code with Run.
func vmExecute(code []Instr, input uint, ver bool, skip int) vmResult {
	work := code
	if skip >= 0 {
		work = make([]Instr, len(code))
		copy(work, code)
		work[skip] = Instr{Op: opNop}
	}
	var regs [NumRegs]bool
	res := vmResult{firstUnlock: -1}
	pc := 0
	for step := 0; step < MaxSteps; step++ {
		if pc >= len(work) {
			res.halted = true
			return res
		}
		switch ins := work[pc]; ins.Op {
		case opNop:
			pc++
		case OpReadPub:
			regs[ins.A] = (input>>uint(ins.B))&1 == 1
			pc++
		case OpReadVer:
			regs[ins.A] = ver
			pc++
		case OpAnd:
			regs[ins.A] = regs[ins.A] && regs[ins.B]
			pc++
		case OpNot:
			regs[ins.A] = !regs[ins.A]
			pc++
		case OpCJmp:
			if regs[ins.A] {
				pc = ins.B
			} else {
				pc++
			}
		case OpUnlock:
			res.unlocks++
			if res.firstUnlock < 0 {
				res.firstUnlock = step
			}
			pc++
		case OpHalt:
			res.halted = true
			return res
		case OpGuard:
			if !ver {
				res.halted = true
				return res
			}
			pc++
		}
	}
	return res
}

// insertGuardsIndependent re-derives the patched program without using
// InsertGuards.
func insertGuardsIndependent(code []Instr, positions []int) []Instr {
	pos := sortedCopy(positions)
	out := make([]Instr, 0, len(code)+len(pos))
	next := 0
	for i := 0; i <= len(code); i++ {
		for next < len(pos) && pos[next] == i {
			out = append(out, Guard())
			next++
		}
		if i == len(code) {
			break
		}
		ins := code[i]
		if ins.Op == OpCJmp {
			target := ins.B
			for _, q := range pos {
				if q <= ins.B {
					target++
				}
			}
			ins.B = target
		}
		out = append(out, ins)
	}
	return out
}

// vmSafeUnderFaults mirrors safeUnderFaults on the independent
// interpreter.
func vmSafeUnderFaults(code []Instr, input uint) bool {
	if r := vmExecute(code, input, false, -1); !r.halted || r.unlocks != 0 {
		return false
	}
	for pc, ins := range code {
		if ins.Op == OpHalt {
			continue
		}
		if r := vmExecute(code, input, false, pc); !r.halted || r.unlocks != 0 {
			return false
		}
	}
	return true
}

// vmPatchValid mirrors patchValid on the independent interpreter,
// recomputing the baseline from the original program.
func vmPatchValid(patched, orig []Instr, numPub int) bool {
	n := 1 << uint(numPub)
	for input := 0; input < n; input++ {
		b := vmExecute(orig, uint(input), true, -1)
		o := vmExecute(patched, uint(input), true, -1)
		if !b.halted || !o.halted || b.unlocks != o.unlocks {
			return false
		}
	}
	for input := 0; input < n; input++ {
		if !vmSafeUnderFaults(patched, uint(input)) {
			return false
		}
	}
	return true
}

// vmFindWitness mirrors findWitness on the independent interpreter.
func vmFindWitness(code []Instr, numPub int) *Witness {
	var best *Witness
	consider := func(w Witness) {
		if best == nil || lessWitness(w, *best) {
			w := w
			best = &w
		}
	}
	n := 1 << uint(numPub)
	for input := 0; input < n; input++ {
		noFault := vmExecute(code, uint(input), false, -1)
		if noFault.firstUnlock >= 0 {
			consider(Witness{Input: uint(input), Skip: -1, UnlockStep: noFault.firstUnlock, PreExisting: true})
		}
		for pc, ins := range code {
			if ins.Op == OpHalt {
				continue
			}
			if r := vmExecute(code, uint(input), false, pc); r.firstUnlock >= 0 {
				consider(Witness{
					Input:       uint(input),
					Skip:        pc,
					UnlockStep:  r.firstUnlock,
					PreExisting: noFault.unlocks > 0,
				})
			}
		}
	}
	return best
}

// VerifyReport exhaustively re-checks a report produced by Analyze: the
// unfixed witness, every returned patch, the minimality and ordering of
// the patch set, and the no-solution conclusion.
func VerifyReport(p Prog, candidates []int, rep *Report) error {
	cands := sortedCopy(candidates)

	wantWitness := vmFindWitness(p.Code, p.NumPub)
	if (rep.Witness == nil) != (wantWitness == nil) {
		return fmt.Errorf("witness presence mismatch: report=%v independent=%v", rep.Witness != nil, wantWitness != nil)
	}
	if rep.Witness != nil && *rep.Witness != *wantWitness {
		return fmt.Errorf("witness mismatch: report=%+v independent=%+v", *rep.Witness, *wantWitness)
	}

	var valid [][]int
	for k := 0; k <= len(cands); k++ {
		for _, combo := range combinations(cands, k) {
			patched := insertGuardsIndependent(p.Code, combo)
			if primary := InsertGuards(p.Code, combo); !slices.Equal(patched, primary) {
				return fmt.Errorf("guard insertion mismatch at %v: %v vs %v", combo, patched, primary)
			}
			if vmPatchValid(patched, p.Code, p.NumPub) {
				valid = append(valid, combo)
			}
		}
		if len(valid) > 0 {
			break
		}
	}
	slices.SortFunc(valid, comparePositions)
	if !equalPatches(valid, rep.Patches) {
		return fmt.Errorf("patch set mismatch: report=%v independent=%v", rep.Patches, valid)
	}
	if rep.NoSolution != (len(valid) == 0) {
		return fmt.Errorf("no-solution flag mismatch: report=%v independent=%v", rep.NoSolution, len(valid) == 0)
	}
	if !slices.IsSortedFunc(rep.Patches, comparePositions) {
		return fmt.Errorf("patches not sorted by position sequence: %v", rep.Patches)
	}
	return nil
}

func equalPatches(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !slices.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
