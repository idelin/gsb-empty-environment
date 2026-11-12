package netsec

import (
	"fmt"
	"sort"
)

// Solve computes the smallest qualified witness set for one artifact release.
//
// It first collapses organizations into control domains using transitive
// reachability over the control graph (chains, cycles and diamonds all merge).
// Within a domain every certificate is an alias and the domain may contribute
// at most one witness. Subset enumeration with iterative deepening then finds
// the minimum-size independent witness set satisfying every region/category
// threshold, or returns ErrNoQualifiedSet. The returned set never exceeds
// MaxWitnesses (protocol cap 20).
func Solve(in Input) (*Result, error) {
	if err := validate(in); err != nil {
		return nil, err
	}

	orgs, certs := indexInputs(in)
	domainOf := buildDomains(orgs, in.ControlEdges)
	atoms := buildAtoms(in.Signatures, certs, orgs, domainOf)
	domains := groupDomains(atoms)

	capWitnesses := in.MaxWitnesses
	if capWitnesses <= 0 {
		capWitnesses = hardWitnessCap
	}

	thr := in.Thresholds
	if minSetSize(thr) == 0 {
		return &Result{
			Witnesses:     []Witness{},
			ControlDomain: domainOf,
			EnumVisited:   0,
		}, nil
	}

	if impossibleCoverage(domains, thr) {
		return nil, ErrNoQualifiedSet
	}

	lower := minSetSize(thr)
	if lower > capWitnesses || len(domains) < lower {
		return nil, ErrNoQualifiedSet
	}

	suffix := precomputeSuffix(domains)
	state := &enumState{
		domains: domains,
		suffix:  suffix,
		thr:     thr,
		budget:  enumBudget,
	}

	visited := 0
	for k := lower; k <= capWitnesses && k <= len(domains); k++ {
		state.target = k
		picked, budgetExceeded := state.dfs(0, nil, &visited)
		if picked != nil {
			out := make([]Witness, 0, k)
			for _, idx := range picked {
				out = append(out, domains[idx].atom)
			}
			sort.Slice(out, func(i, j int) bool {
				return out[i].SignatureID < out[j].SignatureID
			})
			return &Result{
				Witnesses:     out,
				ControlDomain: domainOf,
				EnumVisited:   visited,
			}, nil
		}
		if budgetExceeded {
			return nil, fmt.Errorf("%w: enumeration budget exhausted", ErrNoQualifiedSet)
		}
	}
	return nil, ErrNoQualifiedSet
}

type enumState struct {
	domains []domainOption
	suffix  []suffixInfo
	thr     Thresholds
	target  int
	budget  int
}

// dfs enumerates subsets over domain indices. At each domain it either skips
// it or takes its single witness; taking two witnesses from one control
// domain is therefore structurally impossible.
// It returns the chosen indices on success, or nil together with
// budgetExceeded when the enumeration budget is exhausted.
func (s *enumState) dfs(pos int, picked []int, visited *int) (chosen []int, budgetExceeded bool) {
	*visited++
	if *visited > s.budget {
		return nil, true
	}

	need := s.target - len(picked)
	if need < 0 || need > len(s.domains)-pos {
		return nil, false
	}
	if need == 0 {
		if meetsThresholds(picked, s.domains, s.thr) {
			return picked, false
		}
		return nil, false
	}
	if !s.feasible(picked, pos, need) {
		return nil, false
	}

	// Copy before appending: the skip branch above retains the original
	// backing array, so reusing it would corrupt its recorded picks.
	next := make([]int, len(picked)+1)
	copy(next, picked)
	next[len(picked)] = pos
	// Take-before-skip yields the lexicographically smallest index
	// combination at each target size, hence deterministic minimal output.
	if got, stop := s.dfs(pos+1, next, visited); got != nil || stop {
		return got, stop
	}
	return s.dfs(pos+1, picked, visited)
}

// feasible prunes branches whose remaining domains (positions >= pos) cannot
// possibly supply the required total count, distinct regions, distinct
// categories or pairs. need is the number of witnesses still to pick.
func (s *enumState) feasible(picked []int, pos, need int) bool {
	regionSet := map[string]struct{}{}
	categorySet := map[string]struct{}{}
	pairCount := map[string]int{}
	total := 0

	for _, idx := range picked {
		w := s.domains[idx].atom
		total++
		regionSet[w.Region] = struct{}{}
		categorySet[w.Category] = struct{}{}
		pairCount[pairKey(w.Region, w.Category)]++
	}

	info := s.suffix[pos]
	remainingDomains := len(s.domains) - pos
	if total+remainingDomains < s.thr.MinWitnesses {
		return false
	}
	reachableRegions := len(regionSet)
	for region := range info.regionSet {
		if _, ok := regionSet[region]; !ok {
			reachableRegions++
		}
	}
	if reachableRegions < s.thr.MinRegions {
		return false
	}
	reachableCategories := len(categorySet)
	for category := range info.categorySet {
		if _, ok := categorySet[category]; !ok {
			reachableCategories++
		}
	}
	if reachableCategories < s.thr.MinCategories {
		return false
	}
	for pair, want := range s.thr.MinPairs {
		if pairCount[pair]+info.pairs[pair] < want {
			return false
		}
	}
	return true
}

type suffixInfo struct {
	regionSet   map[string]struct{}
	categorySet map[string]struct{}
	pairs       map[string]int
}

// precomputeSuffix stores, for each domain position, the distinct regions,
// distinct categories and pair availability offered by that position onward.
func precomputeSuffix(domains []domainOption) []suffixInfo {
	out := make([]suffixInfo, len(domains)+1)
	regionSet := map[string]struct{}{}
	categorySet := map[string]struct{}{}
	pairAvail := map[string]int{}

	for i := len(domains) - 1; i >= 0; i-- {
		w := domains[i].atom
		regionSet[w.Region] = struct{}{}
		categorySet[w.Category] = struct{}{}
		pairAvail[pairKey(w.Region, w.Category)]++

		regionsCopy := make(map[string]struct{}, len(regionSet))
		for key := range regionSet {
			regionsCopy[key] = struct{}{}
		}
		categoriesCopy := make(map[string]struct{}, len(categorySet))
		for key := range categorySet {
			categoriesCopy[key] = struct{}{}
		}
		pairsCopy := make(map[string]int, len(pairAvail))
		for key, value := range pairAvail {
			pairsCopy[key] = value
		}
		out[i] = suffixInfo{
			regionSet:   regionsCopy,
			categorySet: categoriesCopy,
			pairs:       pairsCopy,
		}
	}
	return out
}

func meetsThresholds(picked []int, domains []domainOption, thr Thresholds) bool {
	if len(picked) < thr.MinWitnesses {
		return false
	}
	regions := map[string]struct{}{}
	categories := map[string]struct{}{}
	pairs := map[string]int{}
	for _, idx := range picked {
		w := domains[idx].atom
		regions[w.Region] = struct{}{}
		categories[w.Category] = struct{}{}
		pairs[pairKey(w.Region, w.Category)]++
	}
	if len(regions) < thr.MinRegions || len(categories) < thr.MinCategories {
		return false
	}
	for pair, want := range thr.MinPairs {
		if pairs[pair] < want {
			return false
		}
	}
	return true
}

func minSetSize(thr Thresholds) int {
	minimum := thr.MinWitnesses
	if thr.MinRegions > minimum {
		minimum = thr.MinRegions
	}
	if thr.MinCategories > minimum {
		minimum = thr.MinCategories
	}
	for _, want := range thr.MinPairs {
		if want > minimum {
			minimum = want
		}
	}
	return minimum
}

// impossibleCoverage reports whether a region/category pair requirement can
// never be met because not enough available domains carry that combination.
func impossibleCoverage(domains []domainOption, thr Thresholds) bool {
	available := map[string]int{}
	for _, option := range domains {
		w := option.atom
		available[pairKey(w.Region, w.Category)]++
	}
	for pair, want := range thr.MinPairs {
		if available[pair] < want {
			return true
		}
	}
	return false
}
