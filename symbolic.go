package policycheck

import (
	"fmt"
	"math"
	"strings"
)

func collectProofAtoms(policy *Policy, dag *DAG, opts *Options) []*Pred {
	var roots []*Pred
	if policy != nil {
		for i := range policy.Rules {
			roots = append(roots, policy.Rules[i].When)
		}
	}
	if dag != nil {
		for _, node := range dag.Nodes {
			if node != nil && node.Test != nil {
				roots = append(roots, node.Test)
			}
		}
	}
	if opts != nil && opts.Transparent != nil {
		if p := opts.Transparent.predicate(); p != nil {
			roots = append(roots, p)
		}
	}
	seen := make(map[string]*Pred)
	for _, root := range roots {
		for _, atom := range CollectAtoms(root) {
			seen[atom.AtomKey()] = atom
		}
	}
	keys := sortedKeys(seen)
	out := make([]*Pred, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out
}

func (z *Zone) predicate() *Pred {
	if z == nil {
		return nil
	}
	var conds []*Pred
	if len(z.Archs) > 0 {
		conds = append(conds, ArchIn(z.Archs...))
	}
	if len(z.NrRanges) > 0 {
		conds = append(conds, NrInRanges(z.NrRanges...))
	}
	if z.Pred != nil {
		conds = append(conds, z.Pred)
	}
	if len(conds) == 0 {
		return nil
	}
	if len(conds) == 1 {
		return conds[0]
	}
	return And(conds...)
}

type nrVector struct {
	values map[string]bool
	sample uint64
}

func verifySymbolic(policy *Policy, dag *DAG, opts *Options, report *Report) error {
	atoms := collectProofAtoms(policy, dag, opts)
	if opts.MaxAssignments <= 0 {
		opts.MaxAssignments = 1 << 16
	}

	var archAtoms, nrAtoms, freeAtoms []*Pred
	for _, atom := range atoms {
		switch atom.Kind {
		case KindArch:
			archAtoms = append(archAtoms, atom)
		case KindNr:
			nrAtoms = append(nrAtoms, atom)
		default:
			freeAtoms = append(freeAtoms, atom)
		}
	}
	if len(freeAtoms) >= 31 {
		return fmt.Errorf("policycheck: %d free atoms require more than the supported symbolic assignment space", len(freeAtoms))
	}

	freeLeaves := uint64(1) << uint(len(freeAtoms))
	archList := []Arch{ArchX86_64, ArchIA32}
	nrVectorCount := 0
	nrVectorsByArch := make(map[Arch][]nrVector, len(archList))
	for _, arch := range archList {
		var vectors []nrVector
		if legalNumbers, ok := opts.Legal.numbersFor(arch); ok {
			vectors = buildExplicitNRVectors(nrAtoms, legalNumbers)
		} else {
			vectors = buildNRVectors(nrAtoms)
		}
		nrVectorsByArch[arch] = vectors
		nrVectorCount += len(vectors)
	}
	totalLeaves := freeLeaves * uint64(nrVectorCount)
	if totalLeaves > uint64(opts.MaxAssignments) {
		return fmt.Errorf("policycheck: symbolic space has %d assignments, exceeds MaxAssignments=%d", totalLeaves, opts.MaxAssignments)
	}

	maxMismatch := maxMismatches(opts)

	for _, arch := range archList {
		archValues := make(map[string]bool, len(archAtoms)+len(nrAtoms)+len(freeAtoms))
		for _, atom := range archAtoms {
			v, err := atom.Eval(Input{Arch: arch}, nil)
			if err != nil {
				return err
			}
			archValues[atom.AtomKey()] = v
		}

		for _, nrVec := range nrVectorsByArch[arch] {
			baseValues := make(map[string]bool, len(archValues)+len(nrVec.values)+len(freeAtoms))
			for k, v := range archValues {
				baseValues[k] = v
			}
			for k, v := range nrVec.values {
				baseValues[k] = v
			}

			values := make(map[string]bool, len(baseValues)+len(freeAtoms))
			for k, v := range baseValues {
				values[k] = v
			}

			var dfs func(int) error
			dfs = func(pos int) error {
				if len(report.Mismatches) >= maxMismatch {
					return nil
				}
				if pos == len(freeAtoms) {
					report.AssignmentCount++
					return checkSymbolicLeaf(policy, dag, opts, report, atoms, values, arch, nrVec.sample)
				}
				key := freeAtoms[pos].AtomKey()
				values[key] = false
				if err := dfs(pos + 1); err != nil {
					return err
				}
				values[key] = true
				return dfs(pos + 1)
			}
			if err := dfs(0); err != nil {
				return err
			}
		}
	}

	report.SymbolicRun = true
	report.SymbolicComplete = true
	return nil
}

func checkSymbolicLeaf(
	policy *Policy,
	dag *DAG,
	opts *Options,
	report *Report,
	atoms []*Pred,
	values map[string]bool,
	arch Arch,
	nrSample uint64,
) error {
	ref, err := policy.EvaluateSymbolic(values)
	if err != nil {
		return err
	}
	got, _, err := dag.EvaluateSymbolic(values)
	if err != nil {
		return err
	}

	transparent := opts.Strict
	if opts.Transparent != nil {
		zonePred := opts.Transparent.predicate()
		if zonePred == nil {
			transparent = true
		} else {
			transparent, err = evalSymbolic(zonePred, values)
			if err != nil {
				return err
			}
		}
	}
	violations := compareDecisions(ref, got, transparent, opts.Strict)
	if len(violations) == 0 {
		return nil
	}

	region := makeSymbolicRegion(atoms, values)
	region.Arch = arch
	nrCopy := nrSample
	region.Nr = &nrCopy

	witness, ok := solveWitness(opts, atoms, values)
	if ok {
		region.Witness = witness
		region.Satisfiable = true
		region.WitnessFound = true
		region.Arch = witness.Arch
		nr := witness.Nr
		region.Nr = &nr
	}
	report.Mismatches = append(report.Mismatches, Mismatch{
		Violations: violations,
		Region:     region,
		Reference:  ref,
		Candidate:  got,
	})
	return nil
}

func buildNRVectors(atoms []*Pred) []nrVector {
	if len(atoms) == 0 {
		return []nrVector{{values: map[string]bool{}, sample: 0}}
	}
	candidates := map[uint64]struct{}{0: {}, math.MaxUint64: {}}
	for _, atom := range atoms {
		for _, r := range atom.NrRanges {
			candidates[r[0]] = struct{}{}
			candidates[r[1]] = struct{}{}
			if r[0] > 0 {
				candidates[r[0]-1] = struct{}{}
			}
			if r[1] < math.MaxUint64 {
				candidates[r[1]+1] = struct{}{}
			}
		}
	}
	return buildNRVectorsFromCandidates(atoms, candidates)
}

func buildExplicitNRVectors(atoms []*Pred, numbers []uint64) []nrVector {
	candidates := make(map[uint64]struct{}, len(numbers))
	for _, nr := range numbers {
		candidates[nr] = struct{}{}
	}
	if len(atoms) == 0 && len(numbers) > 0 {
		return []nrVector{{values: map[string]bool{}, sample: numbers[0]}}
	}
	return buildNRVectorsFromCandidates(atoms, candidates)
}

func buildNRVectorsFromCandidates(atoms []*Pred, candidates map[uint64]struct{}) []nrVector {
	keys := sortedUintSet(candidates)
	seenVectors := make(map[string]struct{})
	var out []nrVector
	for _, nr := range keys {
		values := make(map[string]bool, len(atoms))
		var vectorKey strings.Builder
		for _, atom := range atoms {
			v, err := atom.Eval(Input{Nr: nr}, nil)
			if err != nil {
				continue
			}
			key := atom.AtomKey()
			values[key] = v
			if v {
				vectorKey.WriteByte('1')
			} else {
				vectorKey.WriteByte('0')
			}
		}
		vk := vectorKey.String()
		if _, ok := seenVectors[vk]; ok {
			continue
		}
		seenVectors[vk] = struct{}{}
		out = append(out, nrVector{values: values, sample: nr})
	}
	return out
}

func makeSymbolicRegion(atoms []*Pred, values map[string]bool) Region {
	region := Region{AtomTruth: make(map[string]bool, len(values))}
	assignments := make([]AtomAssignment, 0, len(atoms))
	for _, atom := range atoms {
		key := atom.AtomKey()
		v := values[key]
		region.AtomTruth[key] = v
		assignments = append(assignments, AtomAssignment{
			Key:        key,
			Expression: atom.Expression(),
			Value:      v,
		})
		if v {
			region.Literals = append(region.Literals, atom.Expression())
		} else {
			region.Literals = append(region.Literals, "NOT "+atom.Expression())
		}
	}
	region.Assignments = assignments
	return region
}
