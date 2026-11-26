package capgate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
)

// Compare exhaustively evaluates old and new under the same small feature
// universe and returns only newly added high-privilege instance conditions.
func Compare(oldGraph, newGraph *Graph, policy Policy) (*DiffReport, error) {
	u, err := buildUniverse([]*Graph{oldGraph, newGraph}, policy)
	if err != nil {
		return nil, err
	}
	oldGA, err := analyzeGraph(oldGraph, u, policy)
	if err != nil {
		return nil, err
	}
	newGA, err := analyzeGraph(newGraph, u, policy)
	if err != nil {
		return nil, err
	}

	oldRuns := map[int]*assignmentAnalysis{}
	newRuns := map[int]*assignmentAnalysis{}
	var coverageRows []AssignmentCoverage
	var totalIterations int

	delta := map[InstanceKey]*big.Int{}
	addedKinds := map[InstanceKey]map[string]bool{}

	for idx := 0; idx < u.count; idx++ {
		if u.legalSet.Bit(idx) == 0 {
			continue
		}
		oldRun, err := oldGA.run(idx)
		if err != nil {
			return nil, err
		}
		newRun, err := newGA.run(idx)
		if err != nil {
			return nil, err
		}
		oldRuns[idx] = oldRun
		newRuns[idx] = newRun
		totalIterations += oldRun.iterations + newRun.iterations

		oldHigh := highKeySet(oldRun)
		newHigh := highKeySet(newRun)
		row := AssignmentCoverage{Assignment: u.assignmentForIndex(idx)}
		for k := range oldHigh {
			row.OldHigh = append(row.OldHigh, k)
		}
		for k := range newHigh {
			row.NewHigh = append(row.NewHigh, k)
		}
		sortInstanceKeys(row.OldHigh)
		sortInstanceKeys(row.NewHigh)

		for _, k := range row.NewHigh {
			if oldHigh[k] {
				continue
			}
			row.Added = append(row.Added, k)
			if delta[k] == nil {
				delta[k] = new(big.Int)
				addedKinds[k] = map[string]bool{}
			}
			delta[k].SetBit(delta[k], idx, 1)
			for kind := range newRun.instances[k].Kinds {
				addedKinds[k][kind] = true
			}
		}
		sortInstanceKeys(row.Added)
		coverageRows = append(coverageRows, row)
	}

	keys := make([]InstanceKey, 0, len(delta))
	for k := range delta {
		keys = append(keys, k)
	}
	sortInstanceKeys(keys)

	r := &DiffReport{
		Features:       append([]string(nil), u.features...),
		LegalCondition: u.legalSource,
		Stats: DiffStats{
			LegalAssignments: len(u.legal),
			Iterations:       totalIterations,
		},
	}

	chainReg := &chainRegistry{report: r}

	for _, key := range keys {
		onset := delta[key]
		primes := u.primeCubes(onset)
		disjoint := u.disjointCubes(onset, primes)

		added := AddedInstance{Instance: key}
		piSet := PrimeImplicantSet{Instance: key}
		for _, idx := range bitsetIndices(onset) {
			added.AddedAssignments = append(added.AddedAssignments, u.assignmentForIndex(idx))
		}
		for _, p := range primes {
			added.PrimeImplicants = append(added.PrimeImplicants, p.Expr())
			piSet.Cubes = append(piSet.Cubes, p)
			piSet.Exprs = append(piSet.Exprs, p.Expr())
			r.Stats.PrimeImplicants++
		}
		r.PrimeImplicants = append(r.PrimeImplicants, piSet)
		for kind := range addedKinds[key] {
			added.Kinds = append(added.Kinds, kind)
		}
		sort.Strings(added.Kinds)

		for blockIndex, dc := range disjoint {
			blockID := fmt.Sprintf("block-%s-%d", shortKey(key), blockIndex+1)
			block := ConditionBlock{
				ID:            blockID,
				Instance:      key,
				Expr:          dc.cube.Expr(),
				Cube:          dc.cube,
				PrimeExpr:     dc.primeCube.Expr(),
				PrimeCube:     dc.primeCube,
				OriginalPrime: dc.cube.Expr() == dc.primeCube.Expr(),
				ResidualPrime: true,
			}
			for _, idx := range dc.indices {
				block.Assignments = append(block.Assignments, u.assignmentForIndex(idx))
				ie := newRuns[idx].instances[key]
				if ie == nil {
					continue
				}
				chains := newGA.buildChains(newRuns[idx], ie)
				for _, chain := range chains {
					id := chainReg.add(chain)
					block.ChainIDs = append(block.ChainIDs, id)
					added.ChainIDs = append(added.ChainIDs, id)
				}
			}
			block.ChainIDs = dedupeSorted(block.ChainIDs)
			added.Blocks = append(added.Blocks, block)
			r.Blocks = append(r.Blocks, block)
			r.Stats.Blocks++
		}
		added.ChainIDs = dedupeSorted(added.ChainIDs)
		r.Added = append(r.Added, added)
		r.Stats.AddedInstances++
	}

	r.Chains = chainReg.report.Chains
	r.Stats.Chains = len(r.Chains)
	r.Coverage = buildCoverageProof(u, coverageRows, len(r.Added) == 0)
	return r, nil
}

func highKeySet(ar *assignmentAnalysis) map[InstanceKey]bool {
	out := map[InstanceKey]bool{}
	for k, ie := range ar.instances {
		if ie != nil && ie.High {
			out[k] = true
		}
	}
	return out
}

func sortInstanceKeys(keys []InstanceKey) {
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
}

func dedupeSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:0]
	var last string
	for i, s := range in {
		if i == 0 || s != last {
			out = append(out, s)
		}
		last = s
	}
	return out
}

func shortKey(k InstanceKey) string {
	s := k.Pkg + "-" + k.Digest + "-" + string(k.Phase)
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out = append(out, r)
		} else if len(out) == 0 || out[len(out)-1] != '_' {
			out = append(out, '_')
		}
	}
	if len(out) > 48 {
		return string(out[:48])
	}
	return string(out)
}

type chainRegistry struct {
	report *DiffReport
	seq    int
}

func (r *chainRegistry) add(c *GrantChain) string {
	r.seq++
	id := fmt.Sprintf("chain-%d", r.seq)
	c.ID = id
	r.report.Chains = append(r.report.Chains, c)
	return id
}

func buildCoverageProof(u *universe, rows []AssignmentCoverage, noAdded bool) CoverageProof {
	proof := CoverageProof{
		FeatureNames:       append([]string(nil), u.features...),
		LegalCondition:     u.legalSource,
		TotalCombinations:  u.count,
		LegalCombinations:  len(u.legal),
		CheckedAssignments: len(rows),
		NoAdded:            noAdded,
		Assignments:        rows,
	}
	type digestInput struct {
		Features    []string
		Legal       string
		Total       int
		LegalCount  int
		NoAdded     bool
		Assignments []AssignmentCoverage
	}
	buf, _ := json.Marshal(digestInput{
		Features: proof.FeatureNames, Legal: proof.LegalCondition,
		Total: proof.TotalCombinations, LegalCount: proof.LegalCombinations,
		NoAdded: noAdded, Assignments: rows,
	})
	sum := sha256.Sum256(buf)
	proof.Digest = hex.EncodeToString(sum[:])
	return proof
}

// VerifyCoverage re-enumerates the legal universe and checks the coverage digest.
func (r *DiffReport) VerifyCoverage() error {
	if r == nil {
		return fmt.Errorf("capgate: nil report")
	}
	features := append([]string(nil), r.Coverage.FeatureNames...)
	policy := Policy{Features: features, LegalCondition: r.Coverage.LegalCondition, MaxFeatures: len(features)}
	u, err := buildUniverse(nil, policy)
	if err != nil {
		return err
	}
	if u.count != r.Coverage.TotalCombinations {
		return fmt.Errorf("capgate: coverage total %d != enumerated %d", r.Coverage.TotalCombinations, u.count)
	}
	if len(u.legal) != r.Coverage.LegalCombinations || len(r.Coverage.Assignments) != len(u.legal) {
		return fmt.Errorf("capgate: coverage legal count mismatch")
	}
	if r.Coverage.CheckedAssignments != len(u.legal) {
		return fmt.Errorf("capgate: coverage checked count mismatch")
	}
	rebuilt := buildCoverageProof(u, r.Coverage.Assignments, r.Coverage.NoAdded)
	if rebuilt.Digest != r.Coverage.Digest {
		return fmt.Errorf("capgate: coverage digest mismatch")
	}

	for _, added := range r.Added {
		var blocks []ConditionBlock
		for _, b := range r.Blocks {
			if b.Instance == added.Instance {
				blocks = append(blocks, b)
			}
		}
		onset := new(big.Int)
		for _, a := range added.AddedAssignments {
			onset.SetBit(onset, u.index(a), 1)
		}

		covered := new(big.Int)
		var rawCubeSets []*big.Int
		residualForbidden := new(big.Int).AndNot(u.legalSet, onset)
		for _, b := range blocks {
			fixedMask, valueMask := b.Cube.masks(u.features)
			rawSet := u.cubeSet(fixedMask, valueMask)
			for _, prior := range rawCubeSets {
				if overlap := new(big.Int).And(rawSet, prior); overlap.BitLen() != 0 {
					return fmt.Errorf("capgate: raw condition cubes overlap for %s at index set", added.Instance)
				}
			}
			rawCubeSets = append(rawCubeSets, rawSet)

			legalCube := new(big.Int).And(rawSet, u.legalSet)
			if bad := new(big.Int).AndNot(legalCube, onset); bad.BitLen() != 0 {
				return fmt.Errorf("capgate: block %s covers a non-added legal assignment", b.ID)
			}
			if b.ResidualPrime {
				for i := 0; i < len(u.features); i++ {
					bit := 1 << i
					if fixedMask&bit == 0 {
						continue
					}
					candidateFixed := fixedMask &^ bit
					candidateValue := valueMask & candidateFixed
					candidate := u.cubeSet(candidateFixed, candidateValue)
					if hit := new(big.Int).And(candidate, residualForbidden); hit.BitLen() == 0 {
						return fmt.Errorf("capgate: block %s is not residual-prime: literal %s can be removed", b.ID, u.features[i])
					}
				}
			}
			residualForbidden.Or(residualForbidden, rawSet)
			covered.Or(covered, legalCube)

			for _, a := range b.Assignments {
				idx := u.index(a)
				if legalCube.Bit(idx) == 0 {
					return fmt.Errorf("capgate: block %s lists assignment outside its cube", b.ID)
				}
				if !b.Cube.Matches(a) || b.Expr != b.Cube.Expr() {
					return fmt.Errorf("capgate: block %s does not match its cube/expression", b.ID)
				}
			}
		}
		if covered.Cmp(onset) != 0 {
			return fmt.Errorf("capgate: block coverage does not exactly equal added assignments for %s", added.Instance)
		}

		wantPrimes := u.primeCubes(onset)
		wantPrimeExprs := map[string]bool{}
		for _, p := range wantPrimes {
			wantPrimeExprs[p.Expr()] = true
		}
		gotPrimeExprs := map[string]bool{}
		for _, e := range added.PrimeImplicants {
			gotPrimeExprs[e] = true
		}
		if len(wantPrimeExprs) != len(gotPrimeExprs) {
			return fmt.Errorf("capgate: prime implicant count mismatch for %s", added.Instance)
		}
		for e := range wantPrimeExprs {
			if !gotPrimeExprs[e] {
				return fmt.Errorf("capgate: missing prime implicant %q for %s", e, added.Instance)
			}
		}
	}

	for _, c := range r.Chains {
		if _, err := c.Replay(); err != nil {
			return fmt.Errorf("capgate: chain %s failed replay: %w", c.ID, err)
		}
	}
	return nil
}

// Verify is shorthand for VerifyCoverage.
func (r *DiffReport) Verify() error { return r.VerifyCoverage() }

// ReplayChains replays every emitted chain with the optional signature verifier.
func (r *DiffReport) ReplayChains(v SignatureVerifier) error {
	for _, c := range r.Chains {
		if _, err := c.ReplayWithVerifier(v); err != nil {
			return fmt.Errorf("capgate: chain %s: %w", c.ID, err)
		}
	}
	return nil
}
