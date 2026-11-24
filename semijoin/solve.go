package semijoin

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type dpChoice struct {
	page   int
	cost   int64
	branch map[string]*dpNode
}

type dpNode struct {
	groups map[string]int64
	leaf   bool
	choice *dpChoice
	cost   int64
}

type solverState struct {
	m     *model
	super *superComponent
	memo  map[string]*dpNode
}

func Solve(problem *Problem) (*Result, error) {
	if err := problem.validate(); err != nil {
		return nil, err
	}
	m, err := buildModel(problem)
	if err != nil {
		return nil, err
	}
	if err := m.enumerateComponents(); err != nil {
		return nil, err
	}
	m.buildSuperComponents()

	result := &Result{
		Determined:           true,
		MinimumWorstReadCost: 0,
		DeterministicOutputs: make(map[string]int64),
	}
	for _, comp := range m.comps {
		if comp.ambiguous {
			continue
		}
		for group, delta := range comp.outputs[0] {
			result.DeterministicOutputs[group] = delta
		}
	}

	for _, super := range m.supers {
		if witness := m.impossibleWitness(super); witness != nil {
			result.Determined = false
			result.MinimumWorstReadCost = -1
			result.Witness = witness
			return result, nil
		}
		state := &solverState{
			m:     m,
			super: super,
			memo:  make(map[string]*dpNode),
		}
		belief := make([]int, len(super.worlds))
		for i := range belief {
			belief[i] = i
		}
		root, err := state.solve(belief, map[int]bool{})
		if err != nil {
			return nil, err
		}
		if root == nil || root.cost < 0 {
			if witness := m.impossibleWitness(super); witness != nil {
				result.Determined = false
				result.MinimumWorstReadCost = -1
				result.Witness = witness
				return result, nil
			}
			return nil, fmt.Errorf("internal error: no adaptive read tree for component with groups %v", super.groups)
		}
		policy := state.toPolicy(root)
		super.policy = policy
		result.Policies = append(result.Policies, *policy)
		result.MinimumWorstReadCost += root.cost
	}
	return result, nil
}

func (s *solverState) solve(belief []int, used map[int]bool) (*dpNode, error) {
	if len(belief) == 0 {
		return nil, nil
	}
	key := beliefKey(belief, used)
	if cached, ok := s.memo[key]; ok {
		return cached, nil
	}

	outputs := make(map[string]int64)
	uniform := true
	for wi, worldIndex := range belief {
		output := s.super.outputs[worldIndex]
		if wi == 0 {
			for group, delta := range output {
				outputs[group] = delta
			}
			continue
		}
		if len(output) != len(outputs) {
			uniform = false
			break
		}
		for group, delta := range output {
			if outputs[group] != delta {
				uniform = false
				break
			}
		}
		if !uniform {
			break
		}
	}
	if uniform {
		node := &dpNode{leaf: true, groups: outputs, cost: 0}
		s.memo[key] = node
		return node, nil
	}

	best := &dpNode{cost: -1}
	for _, pageIndex := range s.super.pages {
		if used[pageIndex] {
			continue
		}
		page := s.m.problem.Pages[pageIndex]
		groups := make(map[string][]int)
		for _, worldIndex := range belief {
			signature := pageGroupKey(s.super.worlds[worldIndex].pages[page.ID])
			groups[signature] = append(groups[signature], worldIndex)
		}
		if len(groups) == 1 {
			continue
		}
		children := make(map[string]*dpNode)
		var childCost int64
		valid := true
		keys := make([]string, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			nextUsed := copyUsed(used)
			nextUsed[pageIndex] = true
			child, err := s.solve(groups[key], nextUsed)
			if err != nil {
				return nil, err
			}
			if child == nil || child.cost < 0 {
				valid = false
				break
			}
			children[key] = child
			if child.cost > childCost {
				childCost = child.cost
			}
		}
		if !valid {
			continue
		}
		candidateCost := page.Cost + childCost
		if best.cost < 0 || candidateCost < best.cost || (candidateCost == best.cost && pageIndex < best.choice.page) {
			best = &dpNode{
				cost: candidateCost,
				choice: &dpChoice{
					page:   pageIndex,
					cost:   candidateCost,
					branch: children,
				},
			}
		}
	}
	s.memo[key] = best
	return best, nil
}

func beliefKey(belief []int, used map[int]bool) string {
	bytes := make([]byte, 0, len(belief)*4+len(used)*2)
	for _, value := range belief {
		bytes = appendVarInt(bytes, int64(value))
	}
	bytes = append(bytes, '|')
	keys := make([]int, 0, len(used))
	for key, value := range used {
		if value {
			keys = append(keys, key)
		}
	}
	sort.Ints(keys)
	for _, key := range keys {
		bytes = appendVarInt(bytes, int64(key))
	}
	return string(bytes)
}

func copyUsed(values map[int]bool) map[int]bool {
	result := make(map[int]bool, len(values)+1)
	for key, value := range values {
		result[key] = value
	}
	return result
}

func appendVarInt(bytes []byte, value int64) []byte {
	for value >= 0x80 {
		bytes = append(bytes, byte(value)|0x80)
		value >>= 7
	}
	return append(bytes, byte(value))
}

func pageGroupKey(values PageValues) string {
	keys := make([]PageVariable, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].VariableID != keys[j].VariableID {
			return keys[i].VariableID < keys[j].VariableID
		}
		return keys[i].State < keys[j].State
	})
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key.VariableID)
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(int(key.State)))
		builder.WriteByte('=')
		builder.WriteString(strconv.FormatInt(values[key], 10))
		builder.WriteByte(';')
	}
	return builder.String()
}

func (s *solverState) toPolicy(root *dpNode) *Policy {
	groups := append([]string(nil), s.super.groups...)
	sort.Strings(groups)
	return &Policy{
		Groups: groups,
		Root:   s.toPolicyNode(root),
		Worst:  root.cost,
	}
}

func (s *solverState) toPolicyNode(node *dpNode) *PolicyNode {
	if node.leaf {
		return &PolicyNode{Leaf: true, Remaining: copyOutput(node.groups)}
	}
	page := s.m.problem.Pages[node.choice.page]
	policyNode := &PolicyNode{ReadPage: page.ID}
	keys := make([]string, 0, len(node.choice.branch))
	for key := range node.choice.branch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		policyNode.Branches = append(policyNode.Branches, PolicyBranch{
			Observed: branchValues(page, key),
			Child:    s.toPolicyNode(node.choice.branch[key]),
		})
	}
	return policyNode
}

func branchValues(page Page, key string) PageValues {
	values := make(PageValues)
	for _, part := range strings.Split(strings.TrimSuffix(key, ";"), ";") {
		leftAndValue := strings.Split(part, "=")
		idAndState := strings.Split(leftAndValue[0], ":")
		stateNumber, _ := strconv.Atoi(idAndState[1])
		value, _ := strconv.ParseInt(leftAndValue[1], 10, 64)
		values[PageVariable{VariableID: idAndState[0], State: State(stateNumber)}] = value
	}
	return values
}

func copyOutput(values map[string]int64) map[string]int64 {
	result := make(map[string]int64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func (m *model) impossibleWitness(super *superComponent) *Witness {
	readable := make(map[PageVariable]bool)
	readablePages := make(map[string]bool)
	for _, pi := range super.pages {
		page := m.problem.Pages[pi]
		readablePages[page.ID] = true
		for _, rv := range page.Reveals {
			readable[rv] = true
		}
	}
	bestI, bestJ := -1, -1
	bestScore := -1
	for i := 0; i < len(super.worlds); i++ {
		for j := i + 1; j < len(super.worlds); j++ {
			if !sameReadable(super.worlds[i], super.worlds[j], readablePages) {
				continue
			}
			if sameOutput(super.outputs[i], super.outputs[j]) {
				continue
			}
			score := witnessScore(super.worlds[i].values, super.worlds[j].values, readable)
			if bestScore < 0 || score < bestScore || (score == bestScore && lexWorlds(super.worlds[i].values, super.worlds[j].values, bestI, bestJ, super)) {
				bestI, bestJ, bestScore = i, j, score
			}
		}
	}
	if bestI < 0 {
		return nil
	}
	pageIDs := make([]string, 0, len(readablePages))
	for id := range readablePages {
		pageIDs = append(pageIDs, id)
	}
	sort.Strings(pageIDs)
	return &Witness{
		First:        Database{Variables: copyOutput(super.worlds[bestI].values), Pages: copyPages(super.worlds[bestI].pages)},
		Second:       Database{Variables: copyOutput(super.worlds[bestJ].values), Pages: copyPages(super.worlds[bestJ].pages)},
		Pages:        pageIDs,
		FirstOutput:  copyOutput(super.outputs[bestI]),
		SecondOutput: copyOutput(super.outputs[bestJ]),
	}
}

func sameReadable(first, second world, pages map[string]bool) bool {
	for pageID := range pages {
		firstPages := first.pages[pageID]
		secondPages := second.pages[pageID]
		if len(firstPages) != len(secondPages) {
			return false
		}
		for key, value := range firstPages {
			if secondPages[key] != value {
				return false
			}
		}
	}
	return true
}

func sameOutput(first, second map[string]int64) bool {
	if len(first) != len(second) {
		return false
	}
	for key, value := range first {
		if second[key] != value {
			return false
		}
	}
	return true
}

func witnessScore(first, second Assignment, readable map[PageVariable]bool) int {
	score := 0
	keys := sortedKeys(first)
	for _, key := range keys {
		if first[key] != second[key] {
			score++
		}
	}
	return score
}

func lexWorlds(first, second Assignment, bestI, bestJ int, super *superComponent) bool {
	if bestI < 0 {
		return true
	}
	bestFirst := super.worlds[bestI].values
	bestSecond := super.worlds[bestJ].values
	keys := sortedKeys(first)
	for _, key := range keys {
		if first[key] != bestFirst[key] {
			return first[key] < bestFirst[key]
		}
		if second[key] != bestSecond[key] {
			return second[key] < bestSecond[key]
		}
	}
	return false
}

func copyPages(values map[string]PageValues) map[string]PageValues {
	result := make(map[string]PageValues, len(values))
	for page, observed := range values {
		copied := make(PageValues, len(observed))
		for key, value := range observed {
			copied[key] = value
		}
		result[page] = copied
	}
	return result
}

func sortedKeys(values map[string]int64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
