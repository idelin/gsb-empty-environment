package hpastar

// SearchStats reports the work spent inside a search.
type SearchStats struct {
	// Expanded is the number of popped (settled) nodes.
	Expanded int
}

// FullAStar is the flat, exact reference search over every grid cell.
// It is used as the ground truth when validating the hierarchical search.
func FullAStar(g *Grid, start, goal Cell) (path []Cell, cost float64, ok bool) {
	path, cost, _, ok = FullAStarWithStats(g, start, goal)
	return path, cost, ok
}

// FullAStarWithStats is FullAStar plus node-expansion counters.
func FullAStarWithStats(g *Grid, start, goal Cell) ([]Cell, float64, SearchStats, bool) {
	var stats SearchStats
	if !g.InBounds(start.X, start.Y) || !g.InBounds(goal.X, goal.Y) {
		return nil, 0, stats, false
	}
	startID, goalID := g.ID(start.X, start.Y), g.ID(goal.X, goal.Y)
	if g.blocked[startID] || g.blocked[goalID] {
		return nil, 0, stats, false
	}
	if startID == goalID {
		return []Cell{start}, 0, stats, true
	}

	dist := map[int]float64{startID: 0}
	parent := map[int]int{}
	settled := map[int]bool{}
	pq := newPriorityQueue[int](func(a, b pqEntry[int]) bool {
		if a.priority != b.priority {
			return a.priority < b.priority
		}
		return a.item < b.item
	})
	pq.push(startID, octile(start, goal))

	neighborIDs := make([]int, 0, 8)
	for pq.len() > 0 {
		id, _ := pq.pop()
		if settled[id] {
			continue
		}
		cur := dist[id]
		settled[id] = true
		stats.Expanded++
		if id == goalID {
			return reconstructGridPath(g, parent, goalID), cur, stats, true
		}

		cell := g.CellOf(id)
		neighborIDs = g.neighbors(id, neighborIDs[:0])
		for _, nid := range neighborIDs {
			nd := cur + moveCost(cell, g.CellOf(nid))
			if old, exists := dist[nid]; !exists || nd < old {
				dist[nid] = nd
				parent[nid] = id
				pq.push(nid, nd+octile(g.CellOf(nid), goal))
			}
		}
	}
	return nil, 0, stats, false
}

func reconstructGridPath(g *Grid, parent map[int]int, goalID int) []Cell {
	rev := []int{goalID}
	for id := goalID; ; {
		p, ok := parent[id]
		if !ok || p == id {
			break
		}
		rev = append(rev, p)
		id = p
	}
	path := make([]Cell, len(rev))
	for i, id := range rev {
		path[len(rev)-1-i] = g.CellOf(id)
	}
	return path
}
