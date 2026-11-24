package migrationaudit

import "sort"

type groupExposure struct {
	groupIndex      int
	observedDigests []int
	lowCostDigests  []int
	exposedAccounts bitSet
	minimumCosts    []Cost
	minimumKnown    []bool
}

func (indexed *indexedProblem) exposureAt(state *auditState) []groupExposure {
	results := make([]groupExposure, len(indexed.problem.ObserverGroups))
	for groupIndex := range indexed.problem.ObserverGroups {
		result := groupExposure{
			groupIndex:      groupIndex,
			exposedAccounts: newBitSet(len(indexed.accountOrder)),
			minimumCosts:    make([]Cost, len(indexed.familySaltList)),
			minimumKnown:    make([]bool, len(indexed.familySaltList)),
		}
		for digestIndex, replicas := range state.observed[groupIndex] {
			if replicas.count() == 0 {
				continue
			}
			result.observedDigests = append(result.observedDigests, digestIndex)
			digest := indexed.problem.Digests[digestIndex]
			if digest.Cost >= indexed.problem.TargetCost {
				continue
			}
			result.lowCostDigests = append(result.lowCostDigests, digestIndex)
			for _, accountIndex := range indexed.directAccounts[digestIndex] {
				result.exposedAccounts.set(accountIndex)
			}
			key := FamilySalt{Family: digest.Family, SaltDomain: digest.SaltDomain}
			familyIndex := indexed.familySalt[key]
			if !result.minimumKnown[familyIndex] || digest.Cost < result.minimumCosts[familyIndex] {
				result.minimumCosts[familyIndex] = digest.Cost
				result.minimumKnown[familyIndex] = true
			}
		}

		for familyIndex, known := range result.minimumKnown {
			if !known {
				continue
			}
			membership := indexed.familyMembership(indexed.familySaltList[familyIndex])
			for _, accountID := range membership.Accounts {
				result.exposedAccounts.set(indexed.accounts[accountID])
			}
		}
		sort.Ints(result.observedDigests)
		sort.Ints(result.lowCostDigests)
		results[groupIndex] = result
	}
	return results
}

func (indexed *indexedProblem) familyMembership(key FamilySalt) FamilyMembership {
	for _, family := range indexed.problem.Families {
		if family.Family == key.Family && family.SaltDomain == key.SaltDomain {
			return family
		}
	}
	return FamilyMembership{}
}

func (indexed *indexedProblem) worstExposure(exposures []groupExposure) (int, int) {
	worstIndex := -1
	worstCount := -1
	for index, exposure := range exposures {
		count := exposure.exposedAccounts.count()
		if count > worstCount || (count == worstCount && indexed.groupOrderLess(index, worstIndex)) {
			worstCount = count
			worstIndex = index
		}
	}
	if worstIndex < 0 {
		return 0, -1
	}
	return worstCount, worstIndex
}

func (indexed *indexedProblem) groupOrderLess(left int, right int) bool {
	if right < 0 {
		return true
	}
	return indexed.problem.ObserverGroups[left].ID < indexed.problem.ObserverGroups[right].ID
}

func bitSetIndexes(values bitSet) []int {
	indexes := make([]int, 0, values.count())
	for blockIndex, block := range values {
		for bit := 0; bit < 64; bit++ {
			index := blockIndex*64 + bit
			if index >= 0 && block&(1<<uint(bit)) != 0 {
				indexes = append(indexes, index)
			}
		}
	}
	return indexes
}
