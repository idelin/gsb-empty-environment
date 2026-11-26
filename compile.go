package hvsched

import (
	"fmt"
	"math"
	"sort"
)

type weightRef struct {
	varIdx int
	fixed  uint64
	isVar  bool
}

type stepInfo struct {
	heads       map[int]uint64
	obsLeaf     int
	obsLength   uint64
	noPacket    bool
	placeholder bool
}

type compiled struct {
	p       Problem
	nVar    int
	varLo   []int64
	varHi   []int64
	varName []string

	id       map[string]int
	parent   []int
	children [][]int
	root     int
	tie      []uint64
	weight   []weightRef
	layer    []string

	postorder []int
	descLeaf  [][]int
	pathEdges map[int][]edgeInfo

	steps []stepInfo

	groupVars   map[string][]int
	groupTarget map[string]int64
}

type edgeInfo struct {
	scheduler int
	child     int
}

type packetBatch struct {
	count  uint64
	length uint64
}

type packetQueue struct {
	batches []packetBatch
}

func (q *packetQueue) add(count, length uint64) {
	if count == 0 {
		return
	}
	if len(q.batches) > 0 && q.batches[len(q.batches)-1].length == length {
		q.batches[len(q.batches)-1].count += count
		return
	}
	q.batches = append(q.batches, packetBatch{count: count, length: length})
}

func (q *packetQueue) head() (uint64, bool) {
	if len(q.batches) == 0 || q.batches[0].count == 0 {
		return 0, false
	}
	return q.batches[0].length, true
}

func (q *packetQueue) pop() bool {
	if len(q.batches) == 0 {
		return false
	}
	q.batches[0].count--
	if q.batches[0].count == 0 {
		q.batches = q.batches[1:]
	}
	return true
}

func compileProblem(p Problem) (*compiled, error) {
	c := &compiled{p: p, nVar: len(p.Vars)}
	if c.nVar == 0 {
		return nil, fmt.Errorf("hvsched: at least one variable is required (use Min == Max for fixed weights)")
	}

	c.varLo = make([]int64, c.nVar)
	c.varHi = make([]int64, c.nVar)
	c.varName = make([]string, c.nVar)
	varByName := make(map[string]int, c.nVar)
	layerByName := make(map[string]string, c.nVar)
	for i, v := range p.Vars {
		if v.Name == "" {
			return nil, fmt.Errorf("hvsched: variable %d has empty name", i)
		}
		if _, exists := varByName[v.Name]; exists {
			return nil, fmt.Errorf("hvsched: duplicate variable name %q", v.Name)
		}
		if v.Min == 0 || v.Max == 0 || v.Min > v.Max {
			return nil, fmt.Errorf("hvsched: variable %q has invalid bounds [%d,%d]", v.Name, v.Min, v.Max)
		}
		if v.Max > MaxSupportedWeight {
			return nil, fmt.Errorf("hvsched: variable %q upper bound %d exceeds %d", v.Name, v.Max, MaxSupportedWeight)
		}
		c.varLo[i] = int64(v.Min)
		c.varHi[i] = int64(v.Max)
		c.varName[i] = v.Name
		varByName[v.Name] = i
		layerByName[v.Name] = v.Layer
	}

	if len(p.Nodes) == 0 {
		return nil, fmt.Errorf("hvsched: at least one root node is required")
	}
	c.id = make(map[string]int, len(p.Nodes))
	for i, n := range p.Nodes {
		if n.ID == "" {
			return nil, fmt.Errorf("hvsched: node %d has empty ID", i)
		}
		if _, exists := c.id[n.ID]; exists {
			return nil, fmt.Errorf("hvsched: duplicate node ID %q", n.ID)
		}
		c.id[n.ID] = i
	}

	c.parent = make([]int, len(p.Nodes))
	c.children = make([][]int, len(p.Nodes))
	c.tie = make([]uint64, len(p.Nodes))
	c.weight = make([]weightRef, len(p.Nodes))
	c.layer = make([]string, len(p.Nodes))
	c.root = -1
	rootCount := 0
	usedVar := make(map[int]string)
	seenTie := make(map[uint64]string)

	for i, n := range p.Nodes {
		c.tie[i] = n.Tie
		c.layer[i] = n.Layer
		if n.Parent == "" {
			c.parent[i] = -1
			c.root = i
			rootCount++
		} else {
			parent, ok := c.id[n.Parent]
			if !ok {
				return nil, fmt.Errorf("hvsched: node %q references unknown parent %q", n.ID, n.Parent)
			}
			c.parent[i] = parent
			c.children[parent] = append(c.children[parent], i)
		}

		if n.WeightVar != "" {
			idx, ok := varByName[n.WeightVar]
			if !ok {
				return nil, fmt.Errorf("hvsched: node %q references unknown variable %q", n.ID, n.WeightVar)
			}
			c.weight[i] = weightRef{varIdx: idx, isVar: true}
			usedVar[idx] = n.ID
			if layerByName[n.WeightVar] == "" && n.Layer != "" {
				layerByName[n.WeightVar] = n.Layer
			}
			if n.Layer != "" && p.Vars[idx].Layer != "" && p.Vars[idx].Layer != n.Layer {
				return nil, fmt.Errorf("hvsched: variable %q layer %q conflicts with node %q layer %q", n.WeightVar, p.Vars[idx].Layer, n.ID, n.Layer)
			}
		} else if i != c.root {
			if n.FixedWeight == 0 {
				return nil, fmt.Errorf("hvsched: non-root node %q needs a positive fixed weight or variable", n.ID)
			}
			c.weight[i] = weightRef{fixed: n.FixedWeight}
		}

		if i != c.root {
			if prior, ok := seenTie[n.Tie]; ok {
				return nil, fmt.Errorf("hvsched: duplicate tenant/tie number %d on %q and %q", n.Tie, prior, n.ID)
			}
			seenTie[n.Tie] = n.ID
		}
	}
	if rootCount != 1 {
		return nil, fmt.Errorf("hvsched: expected exactly one root node, got %d", rootCount)
	}

	for i := range p.Vars {
		if _, ok := usedVar[i]; !ok {
			return nil, fmt.Errorf("hvsched: variable %q is not used by any node", p.Vars[i].Name)
		}
	}

	if err := c.checkCycles(); err != nil {
		return nil, err
	}
	c.buildWalkInfo()

	c.groupVars = make(map[string][]int)
	c.groupTarget = make(map[string]int64)
	fixedByLayer := make(map[string]uint64)
	for i, n := range p.Nodes {
		if i == c.root || n.Layer == "" {
			continue
		}
		if !c.weight[i].isVar && c.weight[i].fixed > 0 {
			fixedByLayer[n.Layer] += c.weight[i].fixed
		}
	}
	for i, v := range p.Vars {
		layer := v.Layer
		if layer == "" {
			layer = layerByName[v.Name]
		}
		if layer == "" {
			continue
		}
		c.groupVars[layer] = append(c.groupVars[layer], i)
	}
	for layer, total := range p.LayerSums {
		if total > math.MaxInt64 {
			return nil, fmt.Errorf("hvsched: layer %q total %d is too large", layer, total)
		}
		fixed := fixedByLayer[layer]
		if fixed > total {
			return nil, fmt.Errorf("hvsched: layer %q fixed weights %d exceed total %d", layer, fixed, total)
		}
		c.groupTarget[layer] = int64(total - fixed)
		if len(c.groupVars[layer]) == 0 && total != fixed {
			return nil, fmt.Errorf("hvsched: layer %q has no free variables but total %d does not match fixed sum %d", layer, total, fixed)
		}
	}

	m := len(p.Observations)
	byBefore := make([][]ArrivalSpec, m+1)
	queues := make(map[int]*packetQueue)
	for leafID := range c.id {
		idx := c.id[leafID]
		if len(c.children[idx]) == 0 {
			queues[idx] = &packetQueue{}
		}
	}
	for _, a := range p.Arrivals {
		idx, ok := c.id[a.Leaf]
		if !ok {
			return nil, fmt.Errorf("hvsched: arrival references unknown leaf %q", a.Leaf)
		}
		if len(c.children[idx]) != 0 {
			return nil, fmt.Errorf("hvsched: arrival targets non-leaf node %q", a.Leaf)
		}
		if a.Before < 0 {
			return nil, fmt.Errorf("hvsched: arrival Before=%d must be nonnegative", a.Before)
		}
		if a.Length == 0 {
			return nil, fmt.Errorf("hvsched: arrival to %q has zero packet length", a.Leaf)
		}
		if a.Count == 0 {
			continue
		}
		if a.Before <= m {
			byBefore[a.Before] = append(byBefore[a.Before], a)
		}
	}

	c.steps = make([]stepInfo, m)
	for t, obs := range p.Observations {
		for _, a := range byBefore[t] {
			queues[c.id[a.Leaf]].add(a.Count, a.Length)
		}

		heads := make(map[int]uint64)
		for leaf, q := range queues {
			if length, ok := q.head(); ok {
				heads[leaf] = length
			}
		}
		if obs.Leaf == "" {
			c.steps[t] = stepInfo{heads: heads, obsLeaf: -1, placeholder: true}
			continue
		}
		obsIdx, ok := c.id[obs.Leaf]
		if !ok {
			return nil, fmt.Errorf("hvsched: observation %d references unknown leaf %q", t, obs.Leaf)
		}
		if len(c.children[obsIdx]) != 0 {
			return nil, fmt.Errorf("hvsched: observation %d references non-leaf node %q", t, obs.Leaf)
		}

		length, active := heads[obsIdx]
		step := stepInfo{heads: heads, obsLeaf: obsIdx, obsLength: length, noPacket: !active}
		if active {
			queues[obsIdx].pop()
		}
		c.steps[t] = step
	}

	return c, nil
}

func (c *compiled) checkCycles() error {
	state := make([]byte, len(c.p.Nodes))
	var visit func(int, []int) error
	visit = func(i int, stack []int) error {
		if state[i] == 1 {
			return fmt.Errorf("hvsched: cycle in node parent references near %q", c.p.Nodes[i].ID)
		}
		if state[i] == 2 {
			return nil
		}
		state[i] = 1
		if c.parent[i] >= 0 {
			if err := visit(c.parent[i], append(stack, i)); err != nil {
				return err
			}
		}
		state[i] = 2
		return nil
	}
	for i := range c.p.Nodes {
		if err := visit(i, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *compiled) buildWalkInfo() {
	visited := make([]bool, len(c.p.Nodes))
	var dfs func(int)
	dfs = func(i int) {
		if visited[i] {
			return
		}
		visited[i] = true
		for _, child := range c.children[i] {
			dfs(child)
		}
		c.postorder = append(c.postorder, i)
	}
	dfs(c.root)

	c.descLeaf = make([][]int, len(c.p.Nodes))
	for _, node := range c.postorder {
		if len(c.children[node]) == 0 {
			c.descLeaf[node] = []int{node}
			continue
		}
		seen := make(map[int]struct{})
		for _, ch := range c.children[node] {
			for _, leaf := range c.descLeaf[ch] {
				if _, ok := seen[leaf]; !ok {
					seen[leaf] = struct{}{}
					c.descLeaf[node] = append(c.descLeaf[node], leaf)
				}
			}
		}
		sort.Ints(c.descLeaf[node])
	}

	c.pathEdges = make(map[int][]edgeInfo)
	for leaf := range c.p.Nodes {
		if len(c.children[leaf]) != 0 {
			continue
		}
		var rev []edgeInfo
		cur := leaf
		for c.parent[cur] >= 0 {
			rev = append(rev, edgeInfo{scheduler: c.parent[cur], child: cur})
			cur = c.parent[cur]
		}
		edges := make([]edgeInfo, 0, len(rev))
		for i := len(rev) - 1; i >= 0; i-- {
			edges = append(edges, rev[i])
		}
		c.pathEdges[leaf] = edges
	}
}

func (c *compiled) activeNodes(heads map[int]uint64) []bool {
	active := make([]bool, len(c.p.Nodes))
	for node := range c.p.Nodes {
		if len(c.children[node]) == 0 {
			_, active[node] = heads[node]
		}
	}
	for _, node := range c.postorder {
		if len(c.children[node]) == 0 {
			continue
		}
		for _, ch := range c.children[node] {
			if active[ch] {
				active[node] = true
				break
			}
		}
	}
	return active
}
