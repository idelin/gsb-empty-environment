package pathsec

import "sort"

// EnumerateSimplePaths returns every simple path beginning at observer. When
// target is non-zero, only paths ending at target are returned. The size bound
// is enforced by the graph (<=12 ASes) and maxHops.
func (g *AttackGraph) EnumerateSimplePaths(observer, target uint32, maxHops int) [][]uint32 {
	if maxHops <= 0 || maxHops >= MaxAdversarialAS {
		maxHops = MaxAdversarialAS - 1
	}
	var paths [][]uint32
	var dfs func([]uint32)
	dfs = func(path []uint32) {
		if target == 0 || path[len(path)-1] == target {
			paths = append(paths, append([]uint32(nil), path...))
		}
		if len(path)-1 >= maxHops {
			return
		}
		last := path[len(path)-1]
		for _, next := range g.Neighbors(last) {
			if containsAS(path, next) {
				continue
			}
			dfs(append(append([]uint32(nil), path...), next))
		}
	}
	dfs([]uint32{observer})
	sort.Slice(paths, func(i, j int) bool {
		if len(paths[i]) != len(paths[j]) {
			return len(paths[i]) < len(paths[j])
		}
		return joinAS(paths[i]) < joinAS(paths[j])
	})
	return paths
}

// EnumerateAllAttackChains is the small-graph exhaustive reference model. It
// independently enumerates all simple paths, then applies the same explicit
// attack transformations and admission checks.
func (g *AttackGraph) EnumerateAllAttackChains(req SearchRequest) (*SearchResult, error) {
	if err := g.CheckBounds(); err != nil {
		return nil, err
	}
	cfg := normalizeSearchRequest(req, g)
	cfg.Limit = 0
	result := &SearchResult{NodeCount: len(g.Nodes), Exhaustive: true}
	seen := make(map[string]bool)

	for _, observer := range cfg.observers(g) {
		target := cfg.VictimASN
		paths := g.EnumerateSimplePaths(observer, target, cfg.MaxHops)
		for _, path := range paths {
			result.ExploredPaths++
			for _, chain := range chainsForNodePath(g, path, cfg) {
				key := chain.canonicalKey()
				if seen[key] {
					continue
				}
				seen[key] = true
				result.Chains = append(result.Chains, chain)
			}

			// When targeting a victim, EnumerateSimplePaths returns only full
			// victim paths. Also evaluate every prefix so false-origin attacks
			// at earlier corrupted nodes match BFS prefix processing.
			if target != 0 {
				for end := 2; end < len(path); end++ {
					prefixPath := path[:end]
					result.ExploredPaths++
					for _, chain := range chainsForNodePath(g, prefixPath, cfg) {
						key := chain.canonicalKey()
						if seen[key] {
							continue
						}
						seen[key] = true
						result.Chains = append(result.Chains, chain)
					}
				}
			}
		}
	}
	finalizeResult(result, 0)
	return result, nil
}

// CrossValidationReport compares bounded search with exhaustive enumeration.
type CrossValidationReport struct {
	Match           bool          `json:"match"`
	SearchCount     int           `json:"searchCount"`
	ReferenceCount  int           `json:"referenceCount"`
	MissingInSearch []string      `json:"missingInSearch,omitempty"`
	ExtraInSearch   []string      `json:"extraInSearch,omitempty"`
	Search          *SearchResult `json:"search,omitempty"`
	Reference       *SearchResult `json:"reference,omitempty"`
}

// CrossValidate runs Search and the exhaustive reference and compares chain
// identity sets. It is intended for tests and small (<=12 AS) topologies.
func (g *AttackGraph) CrossValidate(req SearchRequest) (*CrossValidationReport, error) {
	searchReq := req
	searchReq.Limit = 0
	searchResult, err := g.Search(searchReq)
	if err != nil {
		return nil, err
	}
	reference, err := g.EnumerateAllAttackChains(req)
	if err != nil {
		return nil, err
	}
	searchKeys := chainKeySet(searchResult.Chains)
	referenceKeys := chainKeySet(reference.Chains)
	report := &CrossValidationReport{
		SearchCount:    len(searchKeys),
		ReferenceCount: len(referenceKeys),
		Search:         searchResult,
		Reference:      reference,
	}
	for key := range referenceKeys {
		if !searchKeys[key] {
			report.MissingInSearch = append(report.MissingInSearch, key)
		}
	}
	for key := range searchKeys {
		if !referenceKeys[key] {
			report.ExtraInSearch = append(report.ExtraInSearch, key)
		}
	}
	sort.Strings(report.MissingInSearch)
	sort.Strings(report.ExtraInSearch)
	report.Match = len(report.MissingInSearch) == 0 && len(report.ExtraInSearch) == 0
	return report, nil
}

func chainKeySet(chains []AttackChain) map[string]bool {
	out := make(map[string]bool, len(chains))
	for _, chain := range chains {
		out[chain.canonicalKey()] = true
	}
	return out
}
