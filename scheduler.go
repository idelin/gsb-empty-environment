package handshake

import (
	"fmt"
	"sort"
)

// Constraint requires Before to be switched strictly earlier than After.
type Constraint struct {
	Before int
	After  int
}

// Batch is one atomic maintenance-window action.
type Batch struct {
	Window int
	Nodes  []int
}

// Plan is a sequence of batches assigned to strictly increasing windows.
type Plan struct {
	Batches     []Batch
	WindowCount int
}

// FrontierState is one independently checkable dead-end certificate entry.
type FrontierState struct {
	ReachableWith []Batch
	Switched      []int
	PendingGroups [][]int
	Reasons       []string
}

// Blocker explains why scheduling cannot proceed. Structured fields let an
// auditor re-check the claim without trusting this package's conclusion.
type Blocker struct {
	Kind      string
	Nodes     []int
	Group     []int
	Window    int
	Detail    string
	Frontier  []FrontierState
	Violation *Violation
}

// SchedulingInput is the release request handed to the publisher gate.
type SchedulingInput struct {
	Topology     *Topology
	Dependencies []Constraint
	AtomicGroups [][]int
	WindowNodes  [][]int
}

// SchedulingResult is either a legal minimum-window plan or a blocker.
type SchedulingResult struct {
	Plan             *Plan
	Blocker          *Blocker
	ZeroLegalClients bool
}

type preparedSchedule struct {
	topology      *Topology
	dependencies  []Constraint
	nodeOrder     []int
	nodeIndex     map[int]int
	groupNodes    [][]int
	groupMask     []uint16
	groupPrereq   []uint16
	windowAllowed []uint16
	groupWindows  [][]int
}

// Schedule finds the legal plan using the fewest windows, breaking ties by the
// lexicographically smallest sequence of per-batch sorted node-id lists. When
// no plan exists it returns a structured, independently checkable blocker.
func Schedule(input SchedulingInput) (*SchedulingResult, error) {
	prepared, err := prepareSchedule(input)
	if err != nil {
		return nil, err
	}
	result := &SchedulingResult{ZeroLegalClients: !prepared.topology.HasLegalClient()}

	if blocker := prepared.staticBlocker(); blocker != nil {
		result.Blocker = blocker
		return result, nil
	}
	plan, blocker := prepared.search()
	result.Plan = plan
	result.Blocker = blocker
	return result, nil
}

func prepareSchedule(input SchedulingInput) (*preparedSchedule, error) {
	if input.Topology == nil {
		return nil, fmt.Errorf("handshake: scheduling requires a topology")
	}
	if len(input.WindowNodes) == 0 {
		return nil, fmt.Errorf("handshake: at least one maintenance window is required")
	}
	topology := input.Topology
	nodeOrder := topology.NodeIDs()
	nodeIndex := map[int]int{}
	for index, id := range nodeOrder {
		if id < 0 {
			return nil, fmt.Errorf("handshake: node ids must be non-negative, got %d", id)
		}
		nodeIndex[id] = index
	}

	groupOf := map[int]int{}
	var groupNodes [][]int
	for groupID, rawGroup := range input.AtomicGroups {
		group := append([]int(nil), rawGroup...)
		sort.Ints(group)
		if len(group) < 2 {
			return nil, fmt.Errorf("handshake: atomic group %d must contain at least two nodes", groupID)
		}
		for _, nodeID := range group {
			if _, ok := nodeIndex[nodeID]; !ok {
				return nil, fmt.Errorf("handshake: atomic group references unknown node %d", nodeID)
			}
			if other, exists := groupOf[nodeID]; exists {
				return nil, fmt.Errorf("handshake: node %d belongs to groups %d and %d", nodeID, other, groupID)
			}
			groupOf[nodeID] = groupID
		}
		groupNodes = append(groupNodes, group)
	}

	for _, dependency := range input.Dependencies {
		before, beforeOK := nodeIndex[dependency.Before]
		after, afterOK := nodeIndex[dependency.After]
		if !beforeOK || !afterOK {
			return nil, fmt.Errorf("handshake: dependency references unknown node")
		}
		if before == after {
			return nil, fmt.Errorf("handshake: dependency cannot reference the same node %d", dependency.Before)
		}
	}
	if cycle := findDependencyCycle(nodeOrder, input.Dependencies); cycle != nil {
		return nil, fmt.Errorf("handshake: dependency cycle among nodes %v", cycle)
	}

	windowAllowed := make([]uint16, len(input.WindowNodes))
	seenInWindow := map[int]bool{}
	for windowIndex, nodes := range input.WindowNodes {
		for _, nodeID := range nodes {
			index, ok := nodeIndex[nodeID]
			if !ok {
				return nil, fmt.Errorf("handshake: window %d references unknown node %d", windowIndex, nodeID)
			}
			windowAllowed[windowIndex] |= 1 << index
			seenInWindow[nodeID] = true
		}
	}

	groupCount := len(groupNodes)
	groupMask := make([]uint16, groupCount)
	for groupID, nodes := range groupNodes {
		for _, nodeID := range nodes {
			groupMask[groupID] |= 1 << nodeIndex[nodeID]
		}
	}
	groupPrereq := make([]uint16, groupCount)
	for _, dependency := range input.Dependencies {
		afterGroup, grouped := groupOf[dependency.After]
		if !grouped {
			continue
		}
		if beforeGroup, ok := groupOf[dependency.Before]; ok {
			if beforeGroup == afterGroup {
				continue
			}
			groupPrereq[afterGroup] |= groupMask[beforeGroup]
		} else {
			groupPrereq[afterGroup] |= 1 << nodeIndex[dependency.Before]
		}
	}

	groupWindows := make([][]int, groupCount)
	for groupID := range groupNodes {
		mask := groupMask[groupID]
		for windowIndex, allowed := range windowAllowed {
			if mask&^allowed == 0 {
				groupWindows[groupID] = append(groupWindows[groupID], windowIndex)
			}
		}
	}

	return &preparedSchedule{
		topology:      topology,
		dependencies:  append([]Constraint(nil), input.Dependencies...),
		nodeOrder:     nodeOrder,
		nodeIndex:     nodeIndex,
		groupNodes:    groupNodes,
		groupMask:     groupMask,
		groupPrereq:   groupPrereq,
		windowAllowed: windowAllowed,
		groupWindows:  groupWindows,
	}, nil
}

// earliestWindow returns the first window containing every node in mask and
// strictly later than afterWindow. It returns -1 when none exists.
func (p *preparedSchedule) earliestWindow(mask uint16, afterWindow int) int {
	for windowIndex := afterWindow + 1; windowIndex < len(p.windowAllowed); windowIndex++ {
		if mask&^p.windowAllowed[windowIndex] == 0 {
			return windowIndex
		}
	}
	return -1
}

func findDependencyCycle(nodeOrder []int, dependencies []Constraint) []int {
	adjacency := map[int][]int{}
	for _, dependency := range dependencies {
		adjacency[dependency.Before] = append(adjacency[dependency.Before], dependency.After)
	}
	state := map[int]int{}
	var stack []int
	var visit func(int) bool
	visit = func(node int) bool {
		state[node] = 1
		stack = append(stack, node)
		for _, next := range adjacency[node] {
			if state[next] == 1 {
				cycleStart := 0
				for stack[cycleStart] != next {
					cycleStart++
				}
				return true
			}
			if state[next] == 0 && visit(next) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		state[node] = 2
		return false
	}
	for _, node := range nodeOrder {
		if state[node] == 0 && visit(node) {
			return append([]int(nil), stack...)
		}
	}
	return nil
}

func (p *preparedSchedule) staticBlocker() *Blocker {
	groupOf := map[int]int{}
	for groupID, nodes := range p.groupNodes {
		for _, nodeID := range nodes {
			groupOf[nodeID] = groupID
		}
	}
	for _, nodeID := range p.nodeOrder {
		mask := uint16(1) << p.nodeIndex[nodeID]
		if _, grouped := groupOf[nodeID]; !grouped && p.earliestWindow(mask, -1) < 0 {
			return &Blocker{
				Kind:   "node-has-no-window",
				Nodes:  []int{nodeID},
				Detail: "no maintenance window allows this node to switch",
			}
		}
	}
	for groupID, nodes := range p.groupNodes {
		if len(p.groupWindows[groupID]) == 0 {
			return &Blocker{
				Kind:   "group-has-no-window",
				Group:  append([]int(nil), nodes...),
				Detail: "no single maintenance window contains every node in the atomic group",
			}
		}
	}
	return nil
}
