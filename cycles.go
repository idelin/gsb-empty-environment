package mvsg

// findElementaryCycles enumerates every elementary cycle in the directed
// graph given by adj, using Johnson's algorithm. Nodes are processed in the
// given order, which makes the output deterministic. Each returned cycle
// lists its transactions in edge order; the last one links back to the
// first.
func findElementaryCycles(nodes []TxnID, adj map[TxnID][]TxnID) [][]TxnID {
	index := map[TxnID]int{}
	for i, n := range nodes {
		index[n] = i
	}

	var cycles [][]TxnID
	blocked := map[TxnID]bool{}
	bmap := map[TxnID]map[TxnID]bool{}
	var stack []TxnID

	var unblock func(n TxnID)
	unblock = func(n TxnID) {
		delete(blocked, n)
		for w := range bmap[n] {
			if blocked[w] {
				unblock(w)
			}
		}
		bmap[n] = nil
	}

	var start TxnID
	var sub map[TxnID][]TxnID
	var circuit func(v TxnID) bool
	circuit = func(v TxnID) bool {
		found := false
		stack = append(stack, v)
		blocked[v] = true
		for _, w := range sub[v] {
			if w == start {
				cyc := make([]TxnID, len(stack))
				copy(cyc, stack)
				cycles = append(cycles, cyc)
				found = true
			} else if !blocked[w] {
				if circuit(w) {
					found = true
				}
			}
		}
		if found {
			unblock(v)
		} else {
			for _, w := range sub[v] {
				if bmap[w] == nil {
					bmap[w] = map[TxnID]bool{}
				}
				bmap[w][v] = true
			}
		}
		stack = stack[:len(stack)-1]
		return found
	}

	for i, s := range nodes {
		// Subgraph induced by nodes[i:]; every cycle is found exactly once,
		// rooted at its lowest-ordered member.
		sub = map[TxnID][]TxnID{}
		for _, n := range nodes[i:] {
			for _, m := range adj[n] {
				if index[m] >= i {
					sub[n] = append(sub[n], m)
				}
			}
		}
		start = s
		blocked = map[TxnID]bool{}
		bmap = map[TxnID]map[TxnID]bool{}
		stack = nil
		circuit(s)
	}
	return cycles
}
