package absexec

// brutePath is one concrete bounded path and the bytes written along it.
type brutePath struct {
	path    []int
	written bitset
}

// enumerateBrute performs exhaustive concrete path enumeration over the same
// (node, back-edge-count) unfolding used by the analyzer. It is a deliberately
// simple oracle for tests on small buffers.
func enumerateBrute(c *CFG) ([]brutePath, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	backByEdge := map[[2]int]int{}
	for i, be := range c.BackEdges {
		backByEdge[[2]int{be.From, be.To}] = i
	}
	nodeByID := map[int]Node{}
	for _, n := range c.Nodes {
		nodeByID[n.ID] = n
	}

	var out []brutePath

	var dfs func(nodeID int, counts []int, path []int, written bitset)
	dfs = func(nodeID int, counts []int, path []int, written bitset) {
		n := nodeByID[nodeID]
		written = written.clone()
		switch n.Op {
		case OpWrite:
			written.set(n.Lo, n.Hi)
		case OpDynWrite:
			if n.HiMin > n.LoMax {
				written.set(n.LoMax, n.HiMin)
			}
		}
		path = append(append([]int(nil), path...), nodeID)

		if n.Op == OpSend || len(n.Succ) == 0 {
			out = append(out, brutePath{path: path, written: written.clone()})
		}
		for _, succ := range n.Succ {
			nextCounts := counts
			if bi, ok := backByEdge[[2]int{nodeID, succ}]; ok {
				if counts[bi] >= c.BackEdges[bi].MaxCount {
					continue
				}
				nextCounts = cloneCounts(counts)
				nextCounts[bi]++
			}
			dfs(succ, nextCounts, path, written)
		}
	}

	dfs(c.Entry, make([]int, len(c.BackEdges)), nil, newBitset(c.BufferSize))
	return out, nil
}

// mustSetAt intersects the written sets of every brute path ending at sendID.
func mustSetAt(c *CFG, paths []brutePath, sendID int) (bitset, bool) {
	must := newBitset(c.BufferSize)
	reached := false
	for _, p := range paths {
		if p.path[len(p.path)-1] != sendID {
			continue
		}
		if !reached {
			must = p.written.clone()
			reached = true
			continue
		}
		must.intersectInPlace(p.written)
	}
	return must, reached
}
