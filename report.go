package netsec

import "sort"

// buildReport 走增量路径：先按可达性求未覆盖 (分支, 块) 对，再仅对
// 未覆盖对枚举具体路径，避免在大图上枚举全部路径。
func buildReport(dag *DAG, targets BlockSet, accepted, irrelevant map[ScopeKey]Proof, rejected []RejectedProof) *VerifyReport {
	report := &VerifyReport{
		RejectedProofs:   cloneRejected(rejected),
		AcceptedProofs:   sortedProofs(accepted),
		IrrelevantProofs: sortedProofs(irrelevant),
	}
	report.Stats.Branches = len(dag.Branches)
	report.Stats.Targets = len(targets)
	report.Stats.AcceptedProofs = len(accepted)
	report.Stats.RejectedProofs = len(rejected)
	report.Stats.IrrelevantProofs = len(irrelevant)

	everReachable := make(map[BlockID]struct{})
	uncovered := make(map[ScopeKey]struct{})
	uncoveredByBranch := make(map[string][]BlockID)
	for _, branch := range dag.Branches {
		reachable, err := ReachableBlocks(dag, branch.Head)
		if err != nil {
			continue
		}
		for target := range targets {
			if _, ok := reachable[target]; !ok {
				continue
			}
			everReachable[target] = struct{}{}
			report.Stats.ReachablePairs++
			if !isCovered(accepted, branch.Name, target) {
				key := ScopeKey{Scope: branch.Name, Block: target}
				uncovered[key] = struct{}{}
				uncoveredByBranch[branch.Name] = append(uncoveredByBranch[branch.Name], target)
			}
		}
	}

	for key := range uncovered {
		report.UncoveredKeys = append(report.UncoveredKeys, key)
	}
	sortScopeKeys(report.UncoveredKeys)

	for _, branch := range dag.Branches {
		branchTargets := NewBlockSet(uncoveredByBranch[branch.Name]...)
		if len(branchTargets) == 0 {
			continue
		}
		paths, err := EnumeratePaths(dag, branch, branchTargets)
		if err != nil {
			continue
		}
		report.UncoveredPaths = append(report.UncoveredPaths, paths...)
	}
	sortPaths(report.UncoveredPaths)

	for _, target := range targets.Sorted() {
		if _, ok := everReachable[target]; !ok {
			report.UnreachableTargets = append(report.UnreachableTargets, target)
		}
	}

	report.Stats.UncoveredPairs = len(uncovered)
	report.Stats.UncoveredPaths = len(report.UncoveredPaths)
	report.Complete = len(uncovered) == 0
	return report
}

// buildFullScanReport 走全遍历路径：枚举每条分支到任意目标块的全部简单
// 路径，再由路径反推覆盖对，与增量实现独立计算同一结论。
func buildFullScanReport(dag *DAG, targets BlockSet, accepted, irrelevant map[ScopeKey]Proof, rejected []RejectedProof) *VerifyReport {
	report := &VerifyReport{
		RejectedProofs:   cloneRejected(rejected),
		AcceptedProofs:   sortedProofs(accepted),
		IrrelevantProofs: sortedProofs(irrelevant),
	}
	report.Stats.Branches = len(dag.Branches)
	report.Stats.Targets = len(targets)
	report.Stats.AcceptedProofs = len(accepted)
	report.Stats.RejectedProofs = len(rejected)
	report.Stats.IrrelevantProofs = len(irrelevant)

	seenPairs := make(map[ScopeKey]struct{})
	uncovered := make(map[ScopeKey]struct{})
	everReachable := make(map[BlockID]struct{})
	for _, branch := range dag.Branches {
		paths, err := EnumeratePaths(dag, branch, targets)
		if err != nil {
			continue
		}
		report.Stats.TotalPaths += len(paths)
		for _, path := range paths {
			target := path.Target()
			everReachable[target] = struct{}{}
			pair := ScopeKey{Scope: branch.Name, Block: target}
			if _, seen := seenPairs[pair]; !seen {
				seenPairs[pair] = struct{}{}
				report.Stats.ReachablePairs++
				if !isCovered(accepted, branch.Name, target) {
					uncovered[pair] = struct{}{}
					report.UncoveredKeys = append(report.UncoveredKeys, pair)
				}
			}
			if _, miss := uncovered[pair]; miss {
				report.UncoveredPaths = append(report.UncoveredPaths, path)
			}
		}
	}
	sortPaths(report.UncoveredPaths)
	sortScopeKeys(report.UncoveredKeys)

	for _, target := range targets.Sorted() {
		if _, ok := everReachable[target]; !ok {
			report.UnreachableTargets = append(report.UnreachableTargets, target)
		}
	}

	report.Stats.UncoveredPairs = len(uncovered)
	report.Stats.UncoveredPaths = len(report.UncoveredPaths)
	report.Complete = len(uncovered) == 0
	return report
}

// isCovered 体现核心覆盖语义：全局证明覆盖所有分支；分支作用域证明
// 只覆盖该分支。共享块在其他分支上的引用不会被单分支证明覆盖。
func isCovered(accepted map[ScopeKey]Proof, branch string, block BlockID) bool {
	if _, ok := accepted[ScopeKey{Scope: GlobalScope, Block: block}]; ok {
		return true
	}
	if _, ok := accepted[ScopeKey{Scope: branch, Block: block}]; ok {
		return true
	}
	return false
}

func compareReports(incremental, fullScan *VerifyReport) *CrossCheckResult {
	result := &CrossCheckResult{
		IncrementalUncovered: clonePaths(incremental.UncoveredPaths),
		FullScanUncovered:    clonePaths(fullScan.UncoveredPaths),
	}
	incSet := pathSet(incremental.UncoveredPaths)
	fullSet := pathSet(fullScan.UncoveredPaths)
	for key, path := range fullSet {
		if _, ok := incSet[key]; !ok {
			result.MissingFromIncremental = append(result.MissingFromIncremental, path)
		}
	}
	for key, path := range incSet {
		if _, ok := fullSet[key]; !ok {
			result.ExtraInIncremental = append(result.ExtraInIncremental, path)
		}
	}
	sortPaths(result.MissingFromIncremental)
	sortPaths(result.ExtraInIncremental)
	result.Matched = len(result.MissingFromIncremental) == 0 && len(result.ExtraInIncremental) == 0 &&
		incremental.Stats.UncoveredPairs == fullScan.Stats.UncoveredPairs
	return result
}

func branchExists(dag *DAG, name string) bool {
	for _, branch := range dag.Branches {
		if branch.Name == name {
			return true
		}
	}
	return false
}

func sameProof(a, b Proof) bool {
	return a.BlockID == b.BlockID &&
		a.Kind == b.Kind &&
		a.Scope == b.Scope &&
		a.SignerID == b.SignerID &&
		a.Reference == b.Reference &&
		bytesEqual(a.Signature, b.Signature)
}

func sortScopeKeys(keys []ScopeKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Scope != keys[j].Scope {
			return keys[i].Scope < keys[j].Scope
		}
		return keys[i].Block < keys[j].Block
	})
}

func sortedProofs(store map[ScopeKey]Proof) []Proof {
	keys := make([]ScopeKey, 0, len(store))
	for key := range store {
		keys = append(keys, key)
	}
	sortScopeKeys(keys)
	out := make([]Proof, 0, len(keys))
	for _, key := range keys {
		out = append(out, store[key])
	}
	return out
}

func pathSet(paths []Path) map[string]Path {
	set := make(map[string]Path, len(paths))
	for _, path := range paths {
		set[path.String()] = path
	}
	return set
}

func clonePaths(paths []Path) []Path {
	out := make([]Path, len(paths))
	for i, path := range paths {
		out[i] = Path{Branch: path.Branch, Blocks: append([]BlockID(nil), path.Blocks...)}
	}
	return out
}

func cloneProofMap(in map[ScopeKey]Proof) map[ScopeKey]Proof {
	out := make(map[ScopeKey]Proof, len(in))
	for key, proof := range in {
		out[key] = proof
	}
	return out
}

func cloneRejected(in []RejectedProof) []RejectedProof {
	if len(in) == 0 {
		return nil
	}
	out := make([]RejectedProof, len(in))
	copy(out, in)
	return out
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
