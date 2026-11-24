package nvmerecov

import (
	"fmt"
	"sort"
)

type gedge struct {
	src    int
	dst    int
	req    int
	result Outcome
}

type gnode struct {
	progress   []int
	zones      []zstate
	parentEdge int
	edges      []int
	incoming   []int
}

type graph struct {
	m         *nmodel
	cons      *constraints
	finals    []zoneFinal
	nodes     []*gnode
	edges     []gedge
	nodeByKey map[string]int
	valid     []bool
	firstGood int
	good      []bool
	nextEdge  []int
}

func buildGraph(m *nmodel, cons *constraints, finals []zoneFinal, stopAtFirstValid bool) (*graph, error) {
	g := &graph{
		m:         m,
		cons:      cons,
		finals:    finals,
		nodeByKey: make(map[string]int),
		firstGood: -1,
	}
	if cons.contradiction {
		return g, nil
	}

	progress := make([]int, len(m.queues))
	zones := initialState(m, cons)
	root := &gnode{
		progress:   progress,
		zones:      zones,
		parentEdge: -1,
	}
	g.nodes = append(g.nodes, root)
	g.nodeByKey[stateKey(progress, zones)] = 0
	g.valid = append(g.valid, g.isBreakpoint(root))
	if g.valid[0] {
		g.firstGood = 0
		if stopAtFirstValid {
			return g, nil
		}
	}

	for cursor := 0; cursor < len(g.nodes); cursor++ {
		node := g.nodes[cursor]
		for qi := range m.queues {
			if node.progress[qi] >= len(m.queues[qi]) {
				continue
			}
			ri := m.queues[qi][node.progress[qi]]
			if !g.dependenciesDone(node.progress, ri) {
				continue
			}
			if cons.mustNotExecute[ri] {
				continue
			}

			nextProgress := append([]int(nil), node.progress...)
			nextProgress[qi]++
			nextZones := cloneZones(node.zones)
			ref := m.reqs[ri]
			result, ok := executeCommand(m, &nextZones, ref, activeRelevant(m, cons, nextProgress, ref.zone))
			if !ok {
				continue
			}
			if wanted, exists := cons.want[ri]; exists && !wanted.Equal(result) {
				continue
			}
			if !g.finalPrefixOK(nextProgress, nextZones) || !g.pendingReadPrefixOK(nextProgress, nextZones) {
				continue
			}

			key := stateKey(nextProgress, nextZones)
			dst, exists := g.nodeByKey[key]
			if !exists {
				dst = len(g.nodes)
				g.nodeByKey[key] = dst
				child := &gnode{
					progress:   nextProgress,
					zones:      nextZones,
					parentEdge: len(g.edges),
				}
				g.nodes = append(g.nodes, child)
				g.valid = append(g.valid, g.isBreakpoint(child))
				if g.valid[dst] && g.firstGood == -1 {
					g.firstGood = dst
				}
			}

			eid := len(g.edges)
			g.edges = append(g.edges, gedge{src: cursor, dst: dst, req: ri, result: result})
			node.edges = append(node.edges, eid)
			g.nodes[dst].incoming = append(g.nodes[dst].incoming, eid)

			if stopAtFirstValid && g.firstGood != -1 {
				return g, nil
			}
		}
	}
	return g, nil
}

func cloneZones(zones []zstate) []zstate {
	out := make([]zstate, len(zones))
	for i := range zones {
		out[i] = zones[i].clone()
	}
	return out
}

func (g *graph) dependenciesDone(progress []int, ri int) bool {
	for _, dep := range g.m.reqs[ri].deps {
		ref := g.m.reqs[dep]
		if progress[ref.q] <= ref.idx {
			return false
		}
	}
	return true
}

func executed(progress []int, ref reqRef) bool {
	return progress[ref.q] > ref.idx
}

func executeCommand(m *nmodel, zones *[]zstate, ref reqRef, relevant []interval) (Outcome, bool) {
	z := &(*zones)[ref.zone]
	switch ref.op {
	case OpAppend:
		lba, ok := z.append(m.zones[ref.zone], ref.data, relevant)
		if !ok {
			return Outcome{}, false
		}
		return AppendOutcome(lba), true
	case OpReset:
		z.reset()
		return ResetOutcome(), true
	case OpRead:
		blocks, ok := z.rangeAt(m.zones[ref.zone].capacity, ref.addr, ref.count)
		if !ok {
			return Outcome{}, false
		}
		return ReadOutcome(blocks), true
	default:
		return Outcome{}, false
	}
}

func (g *graph) isBreakpoint(node *gnode) bool {
	if g.cons.contradiction {
		return false
	}
	for ri := range g.m.reqs {
		ref := g.m.reqs[ri]
		isExecuted := executed(node.progress, ref)
		if g.cons.mustExecute[ri] && !isExecuted {
			return false
		}
		if g.cons.mustNotExecute[ri] && isExecuted {
			return false
		}
	}
	for _, final := range g.finals {
		z := &node.zones[final.zone]
		zone := g.m.zones[final.zone]
		if final.wpSet && z.wp != final.wp {
			return false
		}
		for _, sample := range final.samples {
			block, ok := z.blockAt(zone.capacity, sample.Address)
			if !ok || !block.Equal(sample.Block) {
				return false
			}
		}
		for _, observedRange := range final.ranges {
			length := observedRange.Blocks.MustLength()
			got, ok := z.rangeAt(zone.capacity, observedRange.Start, length)
			if !ok || !blocksEqual(got, observedRange.Blocks) {
				return false
			}
		}
	}
	return true
}

func (g *graph) markGood() {
	g.good = make([]bool, len(g.nodes))
	g.nextEdge = make([]int, len(g.nodes))
	for i := range g.nextEdge {
		g.nextEdge[i] = -1
	}
	queue := make([]int, 0)
	for i, valid := range g.valid {
		if valid {
			g.good[i] = true
			queue = append(queue, i)
		}
	}
	for head := 0; head < len(queue); head++ {
		nodeID := queue[head]
		for _, eid := range g.nodes[nodeID].incoming {
			edge := g.edges[eid]
			if !g.good[edge.src] {
				g.good[edge.src] = true
				g.nextEdge[edge.src] = eid
				queue = append(queue, edge.src)
			}
		}
	}
}

func (g *graph) pathTo(nodeID int) []int {
	var reversed []int
	for nodeID != 0 {
		eid := g.nodes[nodeID].parentEdge
		if eid < 0 {
			panic("nvmerecov: broken parent chain")
		}
		reversed = append(reversed, eid)
		nodeID = g.edges[eid].src
	}
	out := make([]int, len(reversed))
	for i := range reversed {
		out[len(reversed)-1-i] = reversed[i]
	}
	return out
}

func (g *graph) prefixFromEdges(edgeIDs []int) Prefix {
	steps := make([]Step, 0, len(edgeIDs))
	for _, eid := range edgeIDs {
		edge := g.edges[eid]
		ref := g.m.reqs[edge.req]
		steps = append(steps, Step{
			Request: ref.id,
			Op:      ref.op,
			Zone:    ref.zone,
			Result:  edge.result,
		})
	}
	return Prefix{Steps: steps}
}

func (g *graph) prefixToNode(nodeID int) Prefix {
	return g.prefixFromEdges(g.pathTo(nodeID))
}

func (g *graph) prefixThroughEdge(eid int) Prefix {
	edges := g.pathTo(g.edges[eid].src)
	edges = append(edges, eid)
	nodeID := g.edges[eid].dst
	for g.nextEdge[nodeID] != -1 {
		next := g.nextEdge[nodeID]
		edges = append(edges, next)
		nodeID = g.edges[next].dst
	}
	if !g.valid[nodeID] {
		panic("nvmerecov: good state does not reach a breakpoint")
	}
	return g.prefixFromEdges(edges)
}

func (g *graph) firstValidPrefix() (Prefix, bool) {
	if g.firstGood < 0 {
		return Prefix{}, false
	}
	return g.prefixToNode(g.firstGood), true
}

type witnessedOutcome struct {
	outcome Outcome
	witness Prefix
}

func outcomeKey(outcome Outcome) string {
	switch outcome.Kind {
	case OutcomeNotExecuted:
		return "N"
	case OutcomeAppend:
		return "A" + string(uint64Bytes(outcome.LBA))
	case OutcomeReset:
		return "R"
	case OutcomeRead:
		return "D" + blocksKey(outcome.Blocks)
	default:
		return fmt.Sprintf("?%d", outcome.Kind)
	}
}

func blocksKey(blocks Blocks) string {
	buf := make([]byte, 0, 64)
	buf = append(buf, uint64Bytes(uint64(len(blocks)))...)
	for _, run := range blocks {
		if run.Empty {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
		buf = append(buf, uint64Bytes(run.Count)...)
		buf = append(buf, uint64Bytes(uint64(len(run.Value)))...)
		buf = append(buf, run.Value...)
	}
	return string(buf)
}

// analyzeGraph returns the complete set of missing outcomes and witnesses.
func analyzeGraph(g *graph, missing []int) []MissingResult {
	g.markGood()
	results := make([]MissingResult, 0, len(missing))

	for _, ri := range missing {
		ref := g.m.reqs[ri]
		seen := make(map[string]witnessedOutcome)
		notExecutedWitnessSet := false
		var notExecutedWitness Prefix

		for nodeID, node := range g.nodes {
			if !g.valid[nodeID] {
				continue
			}
			if !executed(node.progress, ref) && !notExecutedWitnessSet {
				notExecutedWitnessSet = true
				notExecutedWitness = g.prefixToNode(nodeID)
			}
		}
		if notExecutedWitnessSet {
			seen[outcomeKey(NotExecuted)] = witnessedOutcome{
				outcome: NotExecuted,
				witness: notExecutedWitness,
			}
		}

		for eid := range g.edges {
			edge := g.edges[eid]
			if edge.req != ri || !g.good[edge.src] || !g.good[edge.dst] {
				continue
			}
			key := outcomeKey(edge.result)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = witnessedOutcome{
				outcome: edge.result,
				witness: g.prefixThroughEdge(eid),
			}
		}

		entries := make([]witnessedOutcome, 0, len(seen))
		for _, entry := range seen {
			entries = append(entries, entry)
		}
		sort.Slice(entries, func(i, j int) bool {
			return outcomeLess(entries[i].outcome, entries[j].outcome)
		})

		executedCount := 0
		for _, entry := range entries {
			if entry.outcome.Kind != OutcomeNotExecuted {
				executedCount++
			}
		}
		missingResult := MissingResult{
			Request:       ref.id,
			Op:            ref.op,
			Possible:      make([]Outcome, 0, len(entries)),
			Witnesses:     make([]Prefix, 0, len(entries)),
			Publishable:   executedCount == 1,
			Deterministic: len(entries) == 1,
		}
		for _, entry := range entries {
			missingResult.Possible = append(missingResult.Possible, entry.outcome)
			missingResult.Witnesses = append(missingResult.Witnesses, entry.witness)
		}
		results = append(results, missingResult)
	}
	return results
}

func outcomeLess(a, b Outcome) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	switch a.Kind {
	case OutcomeAppend:
		return a.LBA < b.LBA
	case OutcomeRead:
		return blocksKey(a.Blocks) < blocksKey(b.Blocks)
	default:
		return false
	}
}
