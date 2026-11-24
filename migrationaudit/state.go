package migrationaudit

import (
	"strconv"
	"strings"
)

type auditState struct {
	executed    bitSet
	slotDigests []int
	policies    [][]bitSet
	observed    [][]bitSet
}

type stateKey struct {
	executed string
	slots    string
	policies string
	observed string
}

func (state *auditState) structuredKey() stateKey {
	return stateKey{
		executed: encodeBitSet(state.executed),
		slots:    state.slotKey(),
		policies: state.policyKey(),
		observed: state.observedKey(),
	}
}

func (key stateKey) String() string {
	return strings.Join([]string{key.executed, key.slots, key.policies, key.observed}, "|")
}

func (state *auditState) slotKey() string {
	slots := make([]string, len(state.slotDigests))
	for index, digest := range state.slotDigests {
		slots[index] = strconv.Itoa(digest)
	}
	return strings.Join(slots, ",")
}

func (state *auditState) policyKey() string {
	policies := make([]string, len(state.policies))
	for groupIndex, group := range state.policies {
		encoded := make([]string, len(group))
		for policyIndex, replicas := range group {
			encoded[policyIndex] = encodeBitSet(replicas)
		}
		policies[groupIndex] = strings.Join(encoded, ",")
	}
	return strings.Join(policies, ";")
}

func (state *auditState) observedKey() string {
	observed := make([]string, len(state.observed))
	for groupIndex, group := range state.observed {
		encoded := make([]string, len(group))
		for digestIndex, replicas := range group {
			encoded[digestIndex] = encodeBitSet(replicas)
		}
		observed[groupIndex] = strings.Join(encoded, ",")
	}
	return strings.Join(observed, ";")
}

func (indexed *indexedProblem) initialState() *auditState {
	slotCount := len(indexed.problem.Slots)
	digestCount := len(indexed.problem.Digests)
	groupCount := len(indexed.problem.ObserverGroups)

	state := &auditState{
		executed:    newBitSet(len(indexed.problem.Actions)),
		slotDigests: make([]int, slotCount),
		policies:    make([][]bitSet, groupCount),
		observed:    make([][]bitSet, groupCount),
	}
	for slotIndex, slot := range indexed.problem.Slots {
		if slot.InitialDigest == "" {
			state.slotDigests[slotIndex] = -1
		} else {
			state.slotDigests[slotIndex] = indexed.digests[slot.InitialDigest]
		}
	}
	for groupIndex, group := range indexed.problem.ObserverGroups {
		state.policies[groupIndex] = make([]bitSet, 1)
		state.observed[groupIndex] = make([]bitSet, digestCount)
		state.policies[groupIndex][0] = newBitSet(len(indexed.replicaOrder))
		for digestIndex := range state.observed[groupIndex] {
			state.observed[groupIndex][digestIndex] = newBitSet(len(indexed.replicaOrder))
		}
		for _, replicaID := range group.InitialReplicas {
			replicaIndex := indexed.replicas[replicaID]
			state.policies[groupIndex][0].set(replicaIndex)
		}
	}
	for groupIndex := range state.observed {
		visible := state.visibility(indexed, groupIndex)
		for digestIndex, replicas := range visible {
			state.observed[groupIndex][digestIndex].union(replicas)
		}
	}
	return state
}

func (state *auditState) clone() *auditState {
	cloned := &auditState{
		executed:    cloneBitSet(state.executed),
		slotDigests: append([]int(nil), state.slotDigests...),
		policies:    make([][]bitSet, len(state.policies)),
		observed:    make([][]bitSet, len(state.observed)),
	}
	for groupIndex := range state.policies {
		cloned.policies[groupIndex] = make([]bitSet, len(state.policies[groupIndex]))
		cloned.observed[groupIndex] = make([]bitSet, len(state.observed[groupIndex]))
		for slotIndex, values := range state.policies[groupIndex] {
			cloned.policies[groupIndex][slotIndex] = cloneBitSet(values)
		}
		for digestIndex, values := range state.observed[groupIndex] {
			cloned.observed[groupIndex][digestIndex] = cloneBitSet(values)
		}
	}
	return cloned
}

func (indexed *indexedProblem) legalActions(state *auditState) []int {
	legal := make([]int, 0)
	for actionIndex := range indexed.problem.Actions {
		if state.executed.contains(actionIndex) || !indexed.dependenciesReady(state, actionIndex) {
			continue
		}
		action := indexed.problem.Actions[actionIndex]
		switch action.Kind {
		case ActionMove:
			source := indexed.slots[action.Source]
			target := indexed.slots[action.Target]
			digest := indexed.digests[action.Digest]
			if state.slotDigests[source] == digest && state.slotDigests[target] == -1 {
				legal = append(legal, actionIndex)
			}
		case ActionOverwrite, ActionErase:
			target := indexed.slots[action.Target]
			sourceReady := action.Source == ""
			if action.Source != "" {
				source := indexed.slots[action.Source]
				sourceReady = state.slotDigests[source] == indexed.digests[action.Digest]
			}
			if action.Kind == ActionOverwrite && state.slotDigests[target] >= 0 && sourceReady {
				legal = append(legal, actionIndex)
			}
			if action.Kind == ActionErase && state.slotDigests[target] == indexed.digests[action.Digest] {
				legal = append(legal, actionIndex)
			}
		case ActionChangePolicy:
			legal = append(legal, actionIndex)
		}
	}
	return legal
}

func (indexed *indexedProblem) dependenciesReady(state *auditState, actionIndex int) bool {
	for _, dependency := range indexed.dependencies[actionIndex] {
		if !state.executed.contains(dependency) {
			return false
		}
	}
	return true
}

func (indexed *indexedProblem) applyAction(state *auditState, actionIndex int) *auditState {
	action := indexed.problem.Actions[actionIndex]
	next := state.clone()
	preVisibility := [][]bitSet(nil)
	if action.Kind == ActionChangePolicy {
		preVisibility = make([][]bitSet, len(indexed.problem.ObserverGroups))
		groupIndex := indexed.groups[action.Group]
		replicaSet := policyReplicaSet(indexed, action)
		if policyNarrows(state.policies[groupIndex][0], replicaSet) {
			preVisibility[groupIndex] = state.visibility(indexed, groupIndex)
		}
	}

	switch action.Kind {
	case ActionMove:
		source := indexed.slots[action.Source]
		target := indexed.slots[action.Target]
		digest := indexed.digests[action.Digest]
		next.slotDigests[target] = digest
		next.slotDigests[source] = -1
	case ActionOverwrite:
		target := indexed.slots[action.Target]
		next.slotDigests[target] = indexed.digests[action.Digest]
		if action.Source != "" {
			source := indexed.slots[action.Source]
			next.slotDigests[source] = -1
		}
	case ActionErase:
		target := indexed.slots[action.Target]
		next.slotDigests[target] = -1
	case ActionChangePolicy:
		groupIndex := indexed.groups[action.Group]
		replicaSet := policyReplicaSet(indexed, action)
		next.policies[groupIndex][0] = cloneBitSet(replicaSet)
	}

	next.executed.set(actionIndex)
	for groupIndex := range next.observed {
		if preVisibility != nil && preVisibility[groupIndex] != nil {
			next.observeVisible(groupIndex, preVisibility[groupIndex])
		}
		next.observeVisible(groupIndex, next.visibility(indexed, groupIndex))
	}
	return next
}

func policyReplicaSet(indexed *indexedProblem, action Action) bitSet {
	replicaSet := newBitSet(len(indexed.replicaOrder))
	for _, replicaID := range action.Replicas {
		replicaSet.set(indexed.replicas[replicaID])
	}
	return replicaSet
}

func policyNarrows(current bitSet, next bitSet) bool {
	for blockIndex := range current {
		if current[blockIndex]&^next[blockIndex] != 0 {
			return true
		}
	}
	return false
}

func (state *auditState) observeVisible(groupIndex int, visible []bitSet) {
	for digestIndex, replicas := range visible {
		state.observed[groupIndex][digestIndex].union(replicas)
	}
}

func (state *auditState) visibility(indexed *indexedProblem, groupIndex int) []bitSet {
	visible := make([]bitSet, len(state.observed[groupIndex]))
	for digestIndex := range visible {
		visible[digestIndex] = newBitSet(len(state.policies[groupIndex][0]))
	}
	for slotIndex, digestIndex := range state.slotDigests {
		if digestIndex < 0 {
			continue
		}
		replicaIndex := indexed.replicas[indexed.problem.Slots[slotIndex].Replica]
		if state.policies[groupIndex][0].contains(replicaIndex) {
			visible[digestIndex].set(replicaIndex)
		}
	}
	return visible
}

func (indexed *indexedProblem) validatorAccepts(state *auditState) bool {
	for slotIndex := range state.slotDigests {
		if indexed.validator.contains(slotIndex) && state.slotDigests[slotIndex] >= 0 {
			return true
		}
	}
	return false
}

func (state *auditState) key() string {
	return state.structuredKey().String()
}

func (indexed *indexedProblem) decodeState(encoded string) (*auditState, bool) {
	parts := strings.Split(encoded, "|")
	if len(parts) != 4 {
		return nil, false
	}
	executed, ok := decodeBitSet(parts[0], len(indexed.problem.Actions))
	if !ok {
		return nil, false
	}
	slotTokens := strings.Split(parts[1], ",")
	if len(slotTokens) != len(indexed.problem.Slots) {
		return nil, false
	}
	state := &auditState{
		executed:    executed,
		slotDigests: make([]int, len(slotTokens)),
		policies:    make([][]bitSet, len(indexed.problem.ObserverGroups)),
		observed:    make([][]bitSet, len(indexed.problem.ObserverGroups)),
	}
	for index, token := range slotTokens {
		digest, err := strconv.Atoi(token)
		if err != nil || digest < -1 || digest >= len(indexed.problem.Digests) {
			return nil, false
		}
		state.slotDigests[index] = digest
	}
	if !indexed.decodePolicyOrObserved(parts[2], state.policies, 1) {
		return nil, false
	}
	if !indexed.decodePolicyOrObserved(parts[3], state.observed, len(indexed.problem.Digests)) {
		return nil, false
	}
	return state, true
}

func (indexed *indexedProblem) decodePolicyOrObserved(encoded string, target [][]bitSet, innerSize int) bool {
	groupTokens := strings.Split(encoded, ";")
	if len(groupTokens) != len(target) {
		return false
	}
	for groupIndex, groupToken := range groupTokens {
		itemTokens := strings.Split(groupToken, ",")
		if len(itemTokens) != innerSize {
			return false
		}
		target[groupIndex] = make([]bitSet, innerSize)
		for itemIndex, token := range itemTokens {
			values, ok := decodeBitSet(token, len(indexed.replicaOrder))
			if !ok {
				return false
			}
			target[groupIndex][itemIndex] = values
		}
	}
	return true
}
