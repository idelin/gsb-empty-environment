package skew

// maxDist is the +infinity sentinel. Labels never legitimately approach
// it on real traces; all path arithmetic saturates against it.
const maxDist = int64(^uint64(0) >> 1)

func addSat(a, b int64) int64 {
	if a == maxDist || b == maxDist {
		return maxDist
	}
	r := a + b
	if (a > 0 && b > 0 && r < a) || (a < 0 && b < 0 && r > a) {
		if a+b >= 0 {
			return maxDist
		}
		return -maxDist
	}
	return r
}

func subSat(a, b int64) int64 {
	if b == maxDist {
		return -maxDist
	}
	if a == maxDist {
		return maxDist
	}
	return addSat(a, -b)
}

// edgeSlack is the corrected duration (to-from) of an edge under the
// given node offsets. Feasibility requires every slack >= 0.
func edgeSlack(e cedge, x map[string]int64) int64 {
	xu := x[e.uNode]
	xv := x[e.vNode]
	return subSat(addSat(e.vTS, xv), addSat(e.uTS, xu))
}

// bellmanFord computes shortest-path labels from src on the difference
// constraint graph (x_v - x_u <= w). Unreachable nodes stay at maxDist.
func bellmanFord(n int, edges []cedge, src int) []int64 {
	d := make([]int64, n)
	for i := range d {
		d[i] = maxDist
	}
	d[src] = 0
	for i := 0; i < n; i++ {
		changed := false
		for _, e := range edges {
			if d[e.src] == maxDist {
				continue
			}
			if nd := addSat(d[e.src], e.w); nd < d[e.dst] {
				d[e.dst] = nd
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return d
}

// bellmanFordReversed computes, for every node u, the shortest weight of
// a path u -> src on the original graph, i.e. shortest labels from src on
// the reversed graph.
func bellmanFordReversed(n int, edges []cedge, src int) []int64 {
	rev := make([]cedge, len(edges))
	for i, e := range edges {
		rev[i] = cedge{src: e.dst, dst: e.src, w: e.w}
	}
	return bellmanFord(n, rev, src)
}

// bellmanFordVirtual runs Bellman-Ford with an implicit source reaching
// every node at distance 0. This finds negative cycles anywhere and also
// returns feasible potentials (labels) when none exist.
func bellmanFordVirtual(n int, edges []cedge) ([]int64, []Chain) {
	d := make([]int64, n) // implicit source -> v at 0
	pred := make([]int, n)
	pedge := make([]int, n)
	for i := range pred {
		pred[i] = -1
		pedge[i] = -1
	}

	for i := 0; i < n; i++ {
		changed := false
		for ei, e := range edges {
			if d[e.src] == maxDist {
				continue
			}
			if nd := addSat(d[e.src], e.w); nd < d[e.dst] {
				d[e.dst] = nd
				pred[e.dst] = e.src
				pedge[e.dst] = ei
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	seenCycle := make(map[string]bool)
	var chains []Chain
	for _, e := range edges {
		if d[e.src] == maxDist {
			continue
		}
		if addSat(d[e.src], e.w) >= d[e.dst] {
			continue
		}
		edgeIdxs, ok := walkCycle(e.dst, pred, pedge, n)
		if !ok {
			continue
		}
		// walkCycle follows predecessor links, so edges return in
		// reverse causal order; flip them to cause -> effect order.
		for i, j := 0, len(edgeIdxs)-1; i < j; i, j = i+1, j-1 {
			edgeIdxs[i], edgeIdxs[j] = edgeIdxs[j], edgeIdxs[i]
		}
		key := cycleKey(edgeIdxs)
		if seenCycle[key] {
			continue
		}
		seenCycle[key] = true

		ce := make([]ChainEdge, len(edgeIdxs))
		sum := int64(0)
		for i, idx := range edgeIdxs {
			ce[i] = chainEdgeOf(edges[idx])
			sum = addSat(sum, edges[idx].w)
		}
		rotateChainToEarliest(ce)
		chains = append(chains, Chain{Edges: ce, Sum: sum})
	}
	return d, chains
}

// walkCycle follows pred/pedge links from start until it revisits a
// node, returning the edge indices belonging to the cycle (in the
// predecessor-walk order). A -1 predecessor means no cycle was reached.
func walkCycle(start int, pred, pedge []int, n int) ([]int, bool) {
	visitedAt := make([]int, n)
	for i := range visitedAt {
		visitedAt[i] = -1
	}
	cur := start
	var nodeOrder []int
	var edgeOrder []int
	steps := 0
	for visitedAt[cur] == -1 {
		visitedAt[cur] = steps
		nodeOrder = append(nodeOrder, cur)
		if pred[cur] == -1 {
			return nil, false
		}
		edgeOrder = append(edgeOrder, pedge[cur])
		cur = pred[cur]
		steps++
		if steps > n {
			return nil, false
		}
	}
	cycStart := visitedAt[cur]
	return edgeOrder[cycStart:], true
}

// cycleKey identifies a cycle by its edge-index set, so rotations of the
// same negative cycle are reported only once.
func cycleKey(edgeIdxs []int) string {
	mn := edgeIdxs[0]
	for _, idx := range edgeIdxs {
		if idx < mn {
			mn = idx
		}
	}
	// Start the key at the smallest edge index, then walk the cycle
	// forward and backward; lexicographically smaller rotation wins.
	k := len(edgeIdxs)
	pos := 0
	for i, idx := range edgeIdxs {
		if idx == mn {
			pos = i
			break
		}
	}
	at := func(i int) int { return edgeIdxs[(pos+i)%k] }
	atRev := func(i int) int { return edgeIdxs[(pos-i+k)%k] }
	reverse := false
	for i := 1; i < k; i++ {
		if atRev(i) < at(i) {
			reverse = true
			break
		}
		if atRev(i) > at(i) {
			break
		}
	}
	key := make([]byte, 0, 4*k)
	if !reverse {
		for i := 0; i < k; i++ {
			key = append(key, byte(at(i)>>24), byte(at(i)>>16), byte(at(i)>>8), byte(at(i)))
		}
	} else {
		for i := 0; i < k; i++ {
			v := atRev(i)
			key = append(key, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
		}
	}
	return string(key)
}

func chainEdgeOf(e cedge) ChainEdge {
	return ChainEdge{
		From:   e.uNode,
		To:     e.vNode,
		FromTS: e.uTS,
		ToTS:   e.vTS,
		Weight: e.w,
	}
}

// rotateChainToEarliest rotates a closed causal chain so it starts at the
// edge with the smallest observed source timestamp (typically the request
// send of a round trip), making conflict chains read naturally.
func rotateChainToEarliest(ce []ChainEdge) {
	k := 0
	for i := 1; i < len(ce); i++ {
		if ce[i].FromTS < ce[k].FromTS ||
			(ce[i].FromTS == ce[k].FromTS && ce[i].From < ce[k].From) {
			k = i
		}
	}
	// In-place left rotation by k.
	reverse := func(a, b int) {
		for i, j := a, b; i < j; i, j = i+1, j-1 {
			ce[i], ce[j] = ce[j], ce[i]
		}
	}
	reverse(0, k-1)
	reverse(k, len(ce)-1)
	reverse(0, len(ce)-1)
}
