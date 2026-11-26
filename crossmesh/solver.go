package crossmesh

// columnKey identifies one (scope cell, atomic time interval) column. Each
// column is solved in complete isolation: an action bit can only propagate
// along edges that admit that same action in that cell and interval, so no
// time window or action from one chain is ever combined with another's.
type columnKey struct {
	cell int
	time int
}

// reachState is one per-column worklist state keyed by (node, remaining
// depth budget). The root marker (chainMarker) is an unlimited budget; every
// real edge produces a finite budget equal to min(incoming budget - 1, the
// edge's own MaxPathLen). Distinct budgets never share a state, so authority
// arriving through a shallow edge cannot borrow a deeper edge's reach.
type reachState struct {
	node      int
	remaining int
}

// solveColumns computes every node's authorized voxel set in polynomial time.
// There are O(cells * intervals) columns, each solved over O(nodes * budgets)
// states with sparse worklist propagation; no root-to-leaf path is enumerated.
func (e *Engine) solveColumns() {
	auth := make([]*vSet, len(e.nodes))
	for i := range auth {
		auth[i] = newVSet()
	}

	// Only columns touched by at least one edge can ever be authorized.
	columns := map[columnKey]actionMask{}
	for _, cov := range e.edgeCover {
		for cell := range cov.cells {
			for ti := cov.timeLo; ti < cov.timeHi; ti++ {
				columns[columnKey{cell: cell, time: ti}] |= cov.actions
			}
		}
	}

	for col := range columns {
		masks := e.solveOneColumn(col)
		for k, mask := range masks {
			if k.remaining == chainMarker || mask == 0 {
				continue
			}
			if _, isRoot := e.roots[k.node]; isRoot {
				// Trust anchors are authoritative by being anchors; incoming
				// edges never add delegated slices to a root.
				continue
			}
			auth[k.node].m[vKey{cell: col.cell, time: col.time}] |= mask
		}
	}

	e.auth = map[int]*vSet{}
	for i, vs := range auth {
		e.auth[i] = vs
	}
}

// solveOneColumn runs the budget-aware reachability fixed point for one
// column. The state key carries the remaining depth budget, so authority
// delivered through edges with different limits lands in distinct states and
// a shallow chain can never borrow another chain's propagation rights.
func (e *Engine) solveOneColumn(col columnKey) map[reachState]actionMask {
	masks := map[reachState]actionMask{}
	queue := make([]reachState, 0, len(e.roots))
	inQueue := map[reachState]bool{}
	enqueue := func(k reachState) {
		if !inQueue[k] {
			inQueue[k] = true
			queue = append(queue, k)
		}
	}

	// Trust anchors seed every action; the first edge intersection narrows the
	// mask to what that hop actually admits in this column.
	for r := range e.roots {
		marker := reachState{node: r, remaining: chainMarker}
		masks[marker] = e.allActions
		enqueue(marker)
	}

	for head := 0; head < len(queue); head++ {
		k := queue[head]
		inQueue[k] = false
		mask := masks[k]

		for _, out := range e.outAdj[k.node] {
			cov := out.cover
			if !cov.cells.contains(col.cell) || col.time < cov.timeLo || col.time >= cov.timeHi {
				continue
			}
			surviving := mask & cov.actions
			if surviving == 0 {
				continue
			}
			// Need at least one unit of budget to emit another edge, and the
			// edge's own limit caps the depth available below it.
			if k.remaining != chainMarker {
				if k.remaining < 1 {
					continue
				}
				next := k.remaining - 1
				if out.limit < next {
					next = out.limit
				}
				nk := reachState{node: out.target, remaining: next}
				if masks[nk]&surviving == surviving {
					continue
				}
				masks[nk] |= surviving
				if !inQueue[nk] {
					enqueue(nk)
				}
				continue
			}
			// First hop from an unlimited trust anchor: the edge limit sets
			// the finite budget for everything below.
			nk := reachState{node: out.target, remaining: out.limit}
			if masks[nk]&surviving == surviving {
				continue
			}
			masks[nk] |= surviving
			if !inQueue[nk] {
				enqueue(nk)
			}
		}
	}
	return masks
}
