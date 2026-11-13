package netsec

import "sort"

// BranchJoin records the post-dominance merge point used for one equality
// branch. IntroducesPC is false when both successors are the same node.
// Unique is false when the graph is irreducible and has no single nearest
// common post-dominator; Join is then the virtual exit.
type BranchJoin struct {
	Node         int    `json:"node"`
	Successors   [2]int `json:"successors"`
	Join         int    `json:"join"`
	Unique       bool   `json:"unique"`
	IntroducesPC bool   `json:"introduces_pc"`
}

// PostDomInfo exposes the post-dominance facts used by the label analysis.
// Node identifiers in PostDominators may include VirtualExit. The virtual
// exit is one past the last real node.
type PostDomInfo struct {
	VirtualExit      int                `json:"virtual_exit"`
	PostDominators   [][]int            `json:"post_dominators"`
	ImmediatePostDom []int              `json:"immediate_post_dom"`
	Branches         []BranchJoin       `json:"branches"`
	BranchByNode     map[int]BranchJoin `json:"-"`
}

// PostDominance computes post-dominators for a finite CFG. Returns, returns,
// nodes without a successor, and abnormal exits are connected to a virtual
// exit. An infinite loop that cannot otherwise reach that exit is also given
// a virtual-exit edge, which keeps the analysis conservative at out-of-graph
// loop exits.
func PostDominance(program any) (PostDomInfo, error) {
	p, err := asProgram(program)
	if err != nil {
		return PostDomInfo{}, err
	}
	if err := validateProgram(p); err != nil {
		return PostDomInfo{}, err
	}
	return computePostDominance(p)
}

// PostDominators is an alias for PostDominance.
func PostDominators(program any) (PostDomInfo, error) {
	return PostDominance(program)
}

// NearestCommonPostDominator returns the unique nearest common
// post-dominator of a and b. When the CFG is irreducible and multiple nearest
// common post-dominators exist, unique is false and join is the virtual exit.
func NearestCommonPostDominator(program any, a, b int) (join int, unique bool, err error) {
	p, err := asProgram(program)
	if err != nil {
		return 0, false, err
	}
	if err := validateProgram(p); err != nil {
		return 0, false, err
	}
	if a < 0 || a > len(p.Nodes) || b < 0 || b > len(p.Nodes) {
		return 0, false, ErrInvalidProgram
	}
	pd, exit, err := postDomSets(p)
	if err != nil {
		return 0, false, err
	}
	join, unique = nearestCommonPostdom(a, b, pd, exit)
	return join, unique, nil
}

type bitSet uint64

func allBits(count int) bitSet {
	if count >= 64 {
		return ^bitSet(0)
	}
	return (bitSet(1) << uint(count)) - 1
}

func (b bitSet) has(index int) bool {
	return index >= 0 && index < 64 && b&(bitSet(1)<<uint(index)) != 0
}

func (b bitSet) with(index int) bitSet {
	if index < 0 || index >= 64 {
		return b
	}
	return b | (bitSet(1) << uint(index))
}

func (b bitSet) without(index int) bitSet {
	if index < 0 || index >= 64 {
		return b
	}
	return b & ^(bitSet(1) << uint(index))
}

func (b bitSet) intersect(other bitSet) bitSet {
	return b & other
}

func (b bitSet) count() int {
	count := 0
	for b != 0 {
		b &= b - 1
		count++
	}
	return count
}

func (b bitSet) elements(limit int) []int {
	result := make([]int, 0, b.count())
	for index := 0; index < limit; index++ {
		if b.has(index) {
			result = append(result, index)
		}
	}
	return result
}

func computePostDominance(p *Program) (PostDomInfo, error) {
	pd, exit, err := postDomSets(p)
	if err != nil {
		return PostDomInfo{}, err
	}
	info := PostDomInfo{
		VirtualExit:      exit,
		PostDominators:   make([][]int, len(p.Nodes)),
		ImmediatePostDom: make([]int, len(p.Nodes)),
		BranchByNode:     make(map[int]BranchJoin),
	}
	for node := range p.Nodes {
		info.PostDominators[node] = pd[node].elements(exit + 1)
		info.ImmediatePostDom[node] = immediatePostDom(node, pd, exit)
	}
	for node, instruction := range p.Nodes {
		if instruction.Instr.Op != OpEqBranch {
			continue
		}
		targets, _ := instruction.outgoing()
		join := BranchJoin{Node: node, Join: -1}
		if len(targets) >= 2 {
			join.Successors = [2]int{targets[0], targets[1]}
			if targets[0] == targets[1] {
				join.Join = targets[0]
				join.Unique = true
				join.IntroducesPC = false
			} else {
				join.Join, join.Unique = nearestCommonPostdom(targets[0], targets[1], pd, exit)
				join.IntroducesPC = true
			}
			info.Branches = append(info.Branches, join)
			info.BranchByNode[node] = join
		}
	}
	sort.Slice(info.Branches, func(i, j int) bool {
		return info.Branches[i].Node < info.Branches[j].Node
	})
	return info, nil
}

func immediatePostDom(node int, pd []bitSet, exit int) int {
	strict := pd[node].without(node)
	if strict == 0 {
		return -1
	}
	best := -1
	bestSize := -1
	tied := false
	for _, candidate := range strict.elements(exit + 1) {
		if candidate == node {
			continue
		}
		size := pd[candidate].count()
		switch {
		case size > bestSize:
			best = candidate
			bestSize = size
			tied = false
		case size == bestSize:
			tied = true
		}
	}
	if best < 0 || tied {
		return -1
	}
	return best
}

func nearestCommonPostdom(a, b int, pd []bitSet, exit int) (int, bool) {
	if a == b {
		return a, true
	}
	common := pd[a].intersect(pd[b])
	if common == 0 {
		return exit, false
	}
	candidates := make([]int, 0, common.count())
	for _, candidate := range common.elements(exit + 1) {
		nearest := true
		for _, other := range common.elements(exit + 1) {
			if other == candidate {
				continue
			}
			if !pd[candidate].has(other) {
				nearest = false
				break
			}
		}
		if nearest {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}
	return exit, false
}

func postDomSets(p *Program) ([]bitSet, int, error) {
	nodes := len(p.Nodes)
	exit := nodes
	all := allBits(nodes + 1)

	succ := make([][]int, nodes+1)
	for index, node := range p.Nodes {
		if node.terminal() {
			succ[index] = []int{exit}
			continue
		}
		targets, _ := node.outgoing()
		if len(targets) == 0 {
			succ[index] = []int{exit}
			continue
		}
		succ[index] = append([]int(nil), targets...)
	}

	reachExit := reachableToExit(succ, exit)
	for index := 0; index < nodes; index++ {
		if !reachExit[index] {
			succ[index] = append(succ[index], exit)
		}
	}

	pd := make([]bitSet, nodes+1)
	for index := 0; index < nodes; index++ {
		pd[index] = all
	}
	pd[exit] = bitSet(1) << uint(exit)

	for changed := true; changed; {
		changed = false
		for index := 0; index < nodes; index++ {
			next := all
			successors := succ[index]
			if len(successors) == 0 {
				successors = []int{exit}
			}
			for _, target := range successors {
				next = next.intersect(pd[target])
			}
			next = next.with(index)
			if next != pd[index] {
				pd[index] = next
				changed = true
			}
		}
	}
	return pd, exit, nil
}

func reachableToExit(succ [][]int, exit int) []bool {
	n := len(succ)
	reverse := make([][]int, n)
	for from, targets := range succ {
		for _, to := range targets {
			if to >= 0 && to < n {
				reverse[to] = append(reverse[to], from)
			}
		}
	}
	seen := make([]bool, n)
	queue := []int{exit}
	seen[exit] = true
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, predecessor := range reverse[node] {
			if !seen[predecessor] {
				seen[predecessor] = true
				queue = append(queue, predecessor)
			}
		}
	}
	return seen
}
