package migrationaudit

import (
	"errors"
	"fmt"
	"sort"
)

type FamilySalt struct {
	Family     FamilyID
	SaltDomain SaltDomain
}

type indexedProblem struct {
	problem        Problem
	digests        map[DigestID]int
	slots          map[SlotID]int
	replicas       map[ReplicaID]int
	replicaOrder   []ReplicaID
	groups         map[ObserverGroupID]int
	accounts       map[AccountID]int
	accountOrder   []AccountID
	familySalt     map[FamilySalt]int
	familySaltList []FamilySalt
	directAccounts [][]int
	validator      bitSet
	dependencies   [][]int
}

func prepareProblem(problem Problem) (*indexedProblem, error) {
	if len(problem.Actions) > 18 {
		return nil, errors.New("at most 18 actions are supported")
	}
	indexed := &indexedProblem{
		problem:    problem,
		digests:    make(map[DigestID]int),
		slots:      make(map[SlotID]int),
		replicas:   make(map[ReplicaID]int),
		groups:     make(map[ObserverGroupID]int),
		accounts:   make(map[AccountID]int),
		familySalt: make(map[FamilySalt]int),
	}
	if err := indexed.indexDigests(); err != nil {
		return nil, err
	}
	if err := indexed.indexSlots(); err != nil {
		return nil, err
	}
	if err := indexed.indexFamilies(); err != nil {
		return nil, err
	}
	if err := indexed.indexGroups(); err != nil {
		return nil, err
	}
	if err := indexed.indexValidators(); err != nil {
		return nil, err
	}
	if err := indexed.indexActions(); err != nil {
		return nil, err
	}
	return indexed, nil
}

func (indexed *indexedProblem) indexDigests() error {
	seen := make(map[DigestID]bool)
	for index, digest := range indexed.problem.Digests {
		if digest.ID == "" {
			return fmt.Errorf("digest at index %d has an empty ID", index)
		}
		if seen[digest.ID] {
			return fmt.Errorf("duplicate digest ID %q", digest.ID)
		}
		seen[digest.ID] = true
		indexed.digests[digest.ID] = index
		accountIndexes := make([]int, 0, len(digest.Accounts))
		for _, account := range digest.Accounts {
			accountIndex, ok := indexed.accountIndex(account)
			if !ok {
				return fmt.Errorf("digest %q references unknown account %q", digest.ID, account)
			}
			accountIndexes = append(accountIndexes, accountIndex)
		}
		sort.Ints(accountIndexes)
		indexed.directAccounts = append(indexed.directAccounts, uniqueInts(accountIndexes))
	}
	return nil
}

func (indexed *indexedProblem) accountIndex(account AccountID) (int, bool) {
	if account == "" {
		return 0, false
	}
	if index, ok := indexed.accounts[account]; ok {
		return index, true
	}
	index := len(indexed.accountOrder)
	indexed.accounts[account] = index
	indexed.accountOrder = append(indexed.accountOrder, account)
	return index, true
}

func (indexed *indexedProblem) indexSlots() error {
	seen := make(map[SlotID]bool)
	for index, slot := range indexed.problem.Slots {
		if slot.ID == "" {
			return fmt.Errorf("slot at index %d has an empty ID", index)
		}
		if seen[slot.ID] {
			return fmt.Errorf("duplicate slot ID %q", slot.ID)
		}
		seen[slot.ID] = true
		indexed.slots[slot.ID] = index
		if slot.Replica == "" {
			return fmt.Errorf("slot %q has an empty replica", slot.ID)
		}
		if _, ok := indexed.replicas[slot.Replica]; !ok {
			indexed.replicas[slot.Replica] = len(indexed.replicaOrder)
			indexed.replicaOrder = append(indexed.replicaOrder, slot.Replica)
		}
		if slot.InitialDigest != "" {
			if _, ok := indexed.digests[slot.InitialDigest]; !ok {
				return fmt.Errorf("slot %q references unknown digest %q", slot.ID, slot.InitialDigest)
			}
		}
	}
	return nil
}

func (indexed *indexedProblem) indexFamilies() error {
	seen := make(map[FamilySalt]bool)
	for _, family := range indexed.problem.Families {
		if family.Family == "" {
			return errors.New("family membership has an empty family ID")
		}
		if family.SaltDomain == "" {
			return fmt.Errorf("family %q has an empty salt domain", family.Family)
		}
		key := FamilySalt{Family: family.Family, SaltDomain: family.SaltDomain}
		if seen[key] {
			return fmt.Errorf("duplicate family/salt membership %q/%q", family.Family, family.SaltDomain)
		}
		seen[key] = true
		index := len(indexed.familySaltList)
		indexed.familySalt[key] = index
		indexed.familySaltList = append(indexed.familySaltList, key)
		for _, account := range family.Accounts {
			if _, ok := indexed.accountIndex(account); !ok {
				return fmt.Errorf("family %q salt %q references unknown account %q", family.Family, family.SaltDomain, account)
			}
		}
	}
	for _, digest := range indexed.problem.Digests {
		key := FamilySalt{Family: digest.Family, SaltDomain: digest.SaltDomain}
		if _, ok := indexed.familySalt[key]; !ok {
			return fmt.Errorf("digest %q has no matching family/salt membership", digest.ID)
		}
	}
	return nil
}

func (indexed *indexedProblem) indexGroups() error {
	seen := make(map[ObserverGroupID]bool)
	for index, group := range indexed.problem.ObserverGroups {
		if group.ID == "" {
			return fmt.Errorf("observer group at index %d has an empty ID", index)
		}
		if seen[group.ID] {
			return fmt.Errorf("duplicate observer group ID %q", group.ID)
		}
		seen[group.ID] = true
		indexed.groups[group.ID] = index
		for _, replica := range group.InitialReplicas {
			if _, ok := indexed.replicas[replica]; !ok {
				return fmt.Errorf("group %q references unknown replica %q", group.ID, replica)
			}
		}
	}
	return nil
}

func (indexed *indexedProblem) indexValidators() error {
	indexed.validator = newBitSet(len(indexed.problem.Slots))
	for _, slotID := range indexed.problem.ValidatorSlots {
		index, ok := indexed.slots[slotID]
		if !ok {
			return fmt.Errorf("unknown validator slot %q", slotID)
		}
		indexed.validator.set(index)
	}
	if indexed.validator.count() == 0 {
		return errors.New("at least one validator slot is required")
	}
	return nil
}

func (indexed *indexedProblem) indexActions() error {
	if len(indexed.problem.Actions) == 0 {
		return errors.New("at least one action is required")
	}
	actionIDs := make(map[int]bool)
	for index, action := range indexed.problem.Actions {
		if action.ID < 0 {
			return fmt.Errorf("action at index %d has a negative ID", index)
		}
		if actionIDs[action.ID] {
			return fmt.Errorf("duplicate action ID %d", action.ID)
		}
		actionIDs[action.ID] = true
	}
	for _, action := range indexed.problem.Actions {
		switch action.Kind {
		case ActionMove:
			if err := indexed.checkDataAction(action, true); err != nil {
				return err
			}
		case ActionOverwrite, ActionErase:
			if err := indexed.checkDataAction(action, false); err != nil {
				return err
			}
		case ActionChangePolicy:
			if err := indexed.checkPolicyAction(action); err != nil {
				return err
			}
		default:
			return fmt.Errorf("action %d has unknown kind %q", action.ID, action.Kind)
		}
	}
	indexed.dependencies = make([][]int, len(indexed.problem.Actions))
	for index, action := range indexed.problem.Actions {
		dependencyIndexes := make([]int, 0, len(action.DependsOn))
		for _, dependencyID := range action.DependsOn {
			for candidateIndex, candidate := range indexed.problem.Actions {
				if candidate.ID == dependencyID {
					dependencyIndexes = append(dependencyIndexes, candidateIndex)
					break
				}
			}
		}
		sort.Ints(dependencyIndexes)
		indexed.dependencies[index] = uniqueInts(dependencyIndexes)
	}
	if hasCycle(indexed.dependencies) {
		return errors.New("action dependencies contain a cycle")
	}
	return nil
}

func (indexed *indexedProblem) checkDataAction(action Action, requireSource bool) error {
	if action.Group != "" || len(action.Replicas) != 0 {
		return fmt.Errorf("data action %d must not reference policy fields", action.ID)
	}
	if _, ok := indexed.digests[action.Digest]; !ok {
		return fmt.Errorf("action %d references unknown digest %q", action.ID, action.Digest)
	}
	if _, ok := indexed.slots[action.Target]; !ok {
		return fmt.Errorf("action %d references unknown target slot %q", action.ID, action.Target)
	}
	if requireSource && action.Source == action.Target {
		return fmt.Errorf("action %d cannot move within one slot", action.ID)
	}
	if requireSource {
		if _, ok := indexed.slots[action.Source]; !ok {
			return fmt.Errorf("action %d references unknown source slot %q", action.ID, action.Source)
		}
	}
	if !requireSource && action.Kind == ActionOverwrite && action.Source != "" {
		if _, ok := indexed.slots[action.Source]; !ok {
			return fmt.Errorf("action %d references unknown source slot %q", action.ID, action.Source)
		}
		if action.Source == action.Target {
			return fmt.Errorf("overwrite action %d cannot use its target as source", action.ID)
		}
	}
	return nil
}

func (indexed *indexedProblem) checkPolicyAction(action Action) error {
	if _, ok := indexed.groups[action.Group]; !ok {
		return fmt.Errorf("action %d references unknown observer group %q", action.ID, action.Group)
	}
	for _, replica := range action.Replicas {
		if _, ok := indexed.replicas[replica]; !ok {
			return fmt.Errorf("action %d references unknown replica %q", action.ID, replica)
		}
	}
	if action.Target != "" || action.Source != "" || action.Digest != "" {
		return fmt.Errorf("policy action %d must not reference slots or digests", action.ID)
	}
	return nil
}

func uniqueInts(values []int) []int {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func hasCycle(graph [][]int) bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, len(graph))
	var visit func(int) bool
	visit = func(node int) bool {
		color[node] = gray
		for _, next := range graph[node] {
			if color[next] == gray || (color[next] == white && visit(next)) {
				return true
			}
		}
		color[node] = black
		return false
	}
	for node := range graph {
		if color[node] == white && visit(node) {
			return true
		}
	}
	return false
}
