package netsec

import (
	"fmt"
	"slices"
)

// Witness is a shortest trace in which unlock executes while the
// host-given verification result is false.
type Witness struct {
	Input      uint // public input bits
	Skip       int  // skipped instruction index, -1 for a fault-free run
	UnlockStep int  // step index of the first unlock
	// PreExisting reports that the same input unlocks even without any
	// fault; such a finding is a pre-existing vulnerability and cannot
	// be attributed to the fault.
	PreExisting bool
}

// Report is the outcome of Analyze.
type Report struct {
	// Witness is the shortest false-verification unlock trace, or nil
	// if none exists within the step bound.
	Witness *Witness
	// Patches lists every minimum-size set of guard insertion positions
	// that fixes the program, sorted by position sequence. Patches[0]
	// is the canonical choice. Empty when no solution exists.
	Patches [][]int
	// NoSolution reports that no subset of the candidate positions
	// yields a safe patch (including because a bounded run exceeds the
	// step limit, which cannot be reported safe).
	NoSolution bool
	// Verified reports that the independent exhaustive re-execution in
	// verify.go confirmed the witness, the patches, or the absence of
	// a solution.
	Verified bool
}

// Analyze searches for the shortest false-verification unlock trace and
// for minimum-size guard patches. Every conclusion is cross-checked by
// an independent exhaustive implementation before it is returned.
func Analyze(p Prog, candidates []int) (*Report, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := validateCandidates(p, candidates); err != nil {
		return nil, err
	}
	cands := sortedCopy(candidates)

	rep := &Report{}
	rep.Witness = findWitness(p.Code, p.NumPub)

	// Baseline: fault-free runs with a valid verification result. If
	// any of them exceeds the step bound, equivalence of unlock counts
	// cannot be established and no patch can be certified.
	n := 1 << uint(p.NumPub)
	base := make([]Outcome, n)
	baseOK := true
	for input := 0; input < n; input++ {
		base[input] = Run(p.Code, uint(input), true, -1)
		if !base[input].Halted {
			baseOK = false
		}
	}

	if baseOK {
		for k := 0; k <= len(cands); k++ {
			for _, combo := range combinations(cands, k) {
				if patchValid(InsertGuards(p.Code, combo), base, p.NumPub) {
					rep.Patches = append(rep.Patches, combo)
				}
			}
			if len(rep.Patches) > 0 {
				break // minimum size reached
			}
		}
	}
	slices.SortFunc(rep.Patches, comparePositions)
	rep.NoSolution = len(rep.Patches) == 0

	if err := VerifyReport(p, cands, rep); err != nil {
		return rep, fmt.Errorf("netsec: independent verification failed: %w", err)
	}
	rep.Verified = true
	return rep, nil
}

// findWitness enumerates every public input and every skippable
// (non-halt) instruction and returns the shortest false-verification
// unlock trace.
func findWitness(code []Instr, numPub int) *Witness {
	var best *Witness
	consider := func(w Witness) {
		if best == nil || lessWitness(w, *best) {
			w := w
			best = &w
		}
	}
	n := 1 << uint(numPub)
	for input := 0; input < n; input++ {
		noFault := Run(code, uint(input), false, -1)
		if noFault.FirstUnlockStep >= 0 {
			consider(Witness{
				Input:       uint(input),
				Skip:        -1,
				UnlockStep:  noFault.FirstUnlockStep,
				PreExisting: true,
			})
		}
		for pc, ins := range code {
			if ins.Op == OpHalt {
				continue // halt instructions cannot be skipped
			}
			out := Run(code, uint(input), false, pc)
			if out.FirstUnlockStep >= 0 {
				consider(Witness{
					Input:       uint(input),
					Skip:        pc,
					UnlockStep:  out.FirstUnlockStep,
					PreExisting: noFault.Unlocks > 0,
				})
			}
		}
	}
	return best
}

// lessWitness orders traces by unlock step, then input, then skip index.
func lessWitness(a, b Witness) bool {
	if a.UnlockStep != b.UnlockStep {
		return a.UnlockStep < b.UnlockStep
	}
	if a.Input != b.Input {
		return a.Input < b.Input
	}
	return a.Skip < b.Skip
}

// patchValid checks a patched program: unlock counts on valid
// verification inputs must match the baseline, and no bounded fault
// (including skipping a guard) may unlock on a false verification
// result. Any run exceeding the step bound invalidates the patch.
func patchValid(patched []Instr, base []Outcome, numPub int) bool {
	n := 1 << uint(numPub)
	for input := 0; input < n; input++ {
		out := Run(patched, uint(input), true, -1)
		if !out.Halted || out.Unlocks != base[input].Unlocks {
			return false
		}
	}
	for input := 0; input < n; input++ {
		if !safeUnderFaults(patched, uint(input)) {
			return false
		}
	}
	return true
}

// safeUnderFaults requires every false-verification run, fault-free or
// with any single non-halt instruction skipped, to halt within the step
// bound without unlocking.
func safeUnderFaults(code []Instr, input uint) bool {
	out := Run(code, input, false, -1)
	if !out.Halted || out.Unlocks != 0 {
		return false
	}
	for pc, ins := range code {
		if ins.Op == OpHalt {
			continue
		}
		out := Run(code, input, false, pc)
		if !out.Halted || out.Unlocks != 0 {
			return false
		}
	}
	return true
}
