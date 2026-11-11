package hpastar

import "fmt"

// Temporary abstract nodes used to insert a query's start and goal.
const (
	tempStartID = -1
	tempGoalID  = -2
)

// cluster holds the precomputed abstract data of one map partition.
type cluster struct {
	cx, cy int

	// entrances are the boundary cells that connect this cluster to a
	// neighbouring cluster through at least one legal step.
	entrances []int

	// intra[src][dst] is the exact in-cluster shortest-path cost between
	// two entrance cells.
	intra map[int]map[int]float64

	// parent[src] is the Dijkstra parent tree rooted at entrance src,
	// covering every cell reachable from src inside the cluster. It is the
	// refinement data used after an abstract path has been found.
	parent map[int]map[int]int
}

// Hierarchy is the two-level pathfinding structure built on top of a Grid.
type Hierarchy struct {
	grid        *Grid
	clusterSize int
	cols        int
	rows        int
	clusters    [][]cluster

	// rebuildCount is the number of cluster rebuilds since NewHierarchy.
	rebuildCount int
}

// NewHierarchy partitions the grid into clusterSize x clusterSize tiles and
// precomputes every cluster's entrance graph.
func NewHierarchy(g *Grid, clusterSize int) *Hierarchy {
	if clusterSize <= 0 {
		clusterSize = 8
	}
	h := &Hierarchy{
		grid:        g,
		clusterSize: clusterSize,
		cols:        (g.W + clusterSize - 1) / clusterSize,
		rows:        (g.H + clusterSize - 1) / clusterSize,
	}
	h.clusters = make([][]cluster, h.rows)
	for cy := range h.clusters {
		h.clusters[cy] = make([]cluster, h.cols)
		for cx := range h.clusters[cy] {
			h.clusters[cy][cx] = cluster{
				cx:     cx,
				cy:     cy,
				intra:  map[int]map[int]float64{},
				parent: map[int]map[int]int{},
			}
		}
	}
	h.Rebuild()
	return h
}

// Rebuild recomputes every cluster from scratch.
func (h *Hierarchy) Rebuild() {
	h.rebuildCount = 0
	for cy := 0; cy < h.rows; cy++ {
		for cx := 0; cx < h.cols; cx++ {
			h.rebuildCluster(cx, cy)
		}
	}
}

// SetBlocked flips the blocked flag of one cell.
func (g *Grid) SetBlocked(x, y int, blocked bool) {
	g.blocked[g.ID(x, y)] = blocked
}

// UpdateObstacles applies blocked/walkable changes and rebuilds only the
// clusters affected by them (plus their immediate neighbours, whose boundary
// entrance sets may change as well).
func (h *Hierarchy) UpdateObstacles(changes map[Cell]bool) error {
	g := h.grid
	dirty := map[[2]int]bool{}
	for cell, blocked := range changes {
		if !g.InBounds(cell.X, cell.Y) {
			return fmt.Errorf("hpastar: cell (%d,%d) is out of bounds", cell.X, cell.Y)
		}
		g.SetBlocked(cell.X, cell.Y, blocked)
		cx, cy := h.clusterIndexAt(cell.X, cell.Y)
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				ncx, ncy := cx+dx, cy+dy
				if ncx >= 0 && ncx < h.cols && ncy >= 0 && ncy < h.rows {
					dirty[[2]int{ncx, ncy}] = true
				}
			}
		}
	}
	for key := range dirty {
		h.rebuildCluster(key[0], key[1])
	}
	return nil
}

func (h *Hierarchy) clusterIndexAt(x, y int) (int, int) {
	return x / h.clusterSize, y / h.clusterSize
}

func (h *Hierarchy) clusterBounds(cx, cy int) (x0, y0, x1, y1 int) {
	cs := h.clusterSize
	x0, y0 = cx*cs, cy*cs
	x1 = min(x0+cs, h.grid.W)
	y1 = min(y0+cs, h.grid.H)
	return x0, y0, x1, y1
}

// rebuildCluster rescans one cluster's entrances and reruns its intra-cluster
// Dijkstra precomputation.
func (h *Hierarchy) rebuildCluster(cx, cy int) {
	g := h.grid
	x0, y0, x1, y1 := h.clusterBounds(cx, cy)
	c := &h.clusters[cy][cx]
	c.entrances = c.entrances[:0]
	c.intra = map[int]map[int]float64{}
	c.parent = map[int]map[int]int{}
	h.rebuildCount++

	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			id := g.ID(x, y)
			if g.blocked[id] {
				continue
			}
			if h.hasCrossClusterNeighbor(id, cx, cy) {
				c.entrances = append(c.entrances, id)
			}
		}
	}

	for _, src := range c.entrances {
		dist, par := h.dijkstraInCluster(src, x0, y0, x1, y1)
		c.parent[src] = par
		edges := make(map[int]float64, len(c.entrances))
		for _, dst := range c.entrances {
			if d, ok := dist[dst]; ok {
				edges[dst] = d
			}
		}
		c.intra[src] = edges
	}
}

// hasCrossClusterNeighbor reports whether id has a legal walkable neighbour
// (orthogonal or corner-cut-free diagonal) that belongs to another cluster.
func (h *Hierarchy) hasCrossClusterNeighbor(id, cx, cy int) bool {
	g := h.grid
	for _, nid := range g.neighbors(id, nil) {
		nc := g.CellOf(nid)
		ncx, ncy := h.clusterIndexAt(nc.X, nc.Y)
		if ncx != cx || ncy != cy {
			return true
		}
	}
	return false
}

// dijkstraInCluster runs an exact Dijkstra search restricted to one cluster,
// rooted at src. It returns settled distances and the parent tree.
func (h *Hierarchy) dijkstraInCluster(src, x0, y0, x1, y1 int) (map[int]float64, map[int]int) {
	g := h.grid
	dist := map[int]float64{src: 0}
	parent := map[int]int{}
	settled := map[int]bool{}
	pq := newPriorityQueue[int](func(a, b pqEntry[int]) bool {
		if a.priority != b.priority {
			return a.priority < b.priority
		}
		return a.item < b.item
	})
	pq.push(src, 0)

	neighborIDs := make([]int, 0, 8)
	for pq.len() > 0 {
		id, _ := pq.pop()
		if settled[id] {
			continue
		}
		settled[id] = true
		cell := g.CellOf(id)
		neighborIDs = g.neighbors(id, neighborIDs[:0])
		for _, nid := range neighborIDs {
			next := g.CellOf(nid)
			if next.X < x0 || next.X >= x1 || next.Y < y0 || next.Y >= y1 {
				continue
			}
			nd := dist[id] + moveCost(cell, next)
			if old, ok := dist[nid]; !ok || nd < old {
				dist[nid] = nd
				parent[nid] = id
				pq.push(nid, nd)
			}
		}
	}
	return dist, parent
}

// FindPath runs a hierarchical query: start/goal are inserted into the
// abstract graph with local Dijkstra searches, an abstract A* picks the
// entrance sequence, and the stored parent trees refine it into cell steps.
// The returned cost equals the exact full-map A* cost.
func (h *Hierarchy) FindPath(start, goal Cell) ([]Cell, float64, SearchStats, bool) {
	var stats SearchStats
	g := h.grid
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

	scx, scy := h.clusterIndexAt(start.X, start.Y)
	gcx, gcy := h.clusterIndexAt(goal.X, goal.Y)
	sx0, sy0, sx1, sy1 := h.clusterBounds(scx, scy)
	gx0, gy0, gx1, gy1 := h.clusterBounds(gcx, gcy)

	// Local insertion: exact distances from the endpoints to every cell of
	// their own clusters.
	sDist, sParent := h.dijkstraInCluster(startID, sx0, sy0, sx1, sy1)
	gDist, gParent := h.dijkstraInCluster(goalID, gx0, gy0, gx1, gy1)
	stats.Expanded = len(sDist) + len(gDist)

	// Same cluster: the local search already holds the exact answer.
	if scx == gcx && scy == gcy {
		if d, ok := sDist[goalID]; ok {
			return treePathCells(g, sParent, startID, goalID), d, stats, true
		}
	}

	// Reachable entrance edges out of the temporary start node.
	startEntrances := map[int]float64{}
	for _, e := range h.clusters[scy][scx].entrances {
		if d, ok := sDist[e]; ok {
			startEntrances[e] = d
		}
	}

	heuristic := func(node int) float64 {
		switch node {
		case tempStartID:
			return octile(start, goal)
		case tempGoalID:
			return 0
		default:
			return octile(g.CellOf(node), goal)
		}
	}

	// abstractEdges emits every abstract edge leaving node, generated lazily
	// from the precomputed clusters and the query-local insertion data.
	abstractEdges := func(node int, emit func(to int, weight float64)) {
		if node == tempStartID {
			for e, d := range startEntrances {
				emit(e, d)
			}
			if scx == gcx && scy == gcy {
				if d, ok := sDist[goalID]; ok {
					emit(tempGoalID, d)
				}
			}
			return
		}
		cell := g.CellOf(node)
		ccx, ccy := h.clusterIndexAt(cell.X, cell.Y)
		c := &h.clusters[ccy][ccx]

		// Intra-cluster entrance-to-entrance edges.
		for to, weight := range c.intra[node] {
			emit(to, weight)
		}

		// Inter-cluster edges. Every legal grid step of an entrance that
		// crosses a cluster border (orthogonal or diagonal corner crossing)
		// becomes an abstract edge, so no legal move (including diagonal
		// corner crossings) is lost.
		for _, nid := range g.neighbors(node, nil) {
			nc := g.CellOf(nid)
			ncx, ncy := h.clusterIndexAt(nc.X, nc.Y)
			if ncx == ccx && ncy == ccy {
				continue
			}
			emit(nid, moveCost(cell, nc))
		}

		// Goal insertion edge.
		if ccx == gcx && ccy == gcy {
			if d, ok := gDist[node]; ok {
				emit(tempGoalID, d)
			}
		}
	}

	gScore := map[int]float64{tempStartID: 0}
	cameFrom := map[int]int{}
	settledAbs := map[int]bool{}
	pq := newPriorityQueue[int](func(a, b pqEntry[int]) bool {
		if a.priority != b.priority {
			return a.priority < b.priority
		}
		return a.item < b.item
	})
	pq.push(tempStartID, octile(start, goal))

	for pq.len() > 0 {
		node, _ := pq.pop()
		if settledAbs[node] {
			continue
		}
		settledAbs[node] = true
		stats.Expanded++
		if node == tempGoalID {
			sequence := []int{tempGoalID}
			for cur := tempGoalID; cur != tempStartID; {
				cur = cameFrom[cur]
				sequence = append(sequence, cur)
			}
			for i, j := 0, len(sequence)-1; i < j; i, j = i+1, j-1 {
				sequence[i], sequence[j] = sequence[j], sequence[i]
			}
			path := h.refine(sequence, startID, goalID, sParent, gParent)
			return path, gScore[tempGoalID], stats, true
		}

		cur := gScore[node]
		abstractEdges(node, func(to int, weight float64) {
			nd := cur + weight
			if old, ok := gScore[to]; !ok || nd < old {
				gScore[to] = nd
				cameFrom[to] = node
				pq.push(to, nd+heuristic(to))
			}
		})
	}
	return nil, 0, stats, false
}

// refine expands an abstract entrance sequence into concrete grid cells using
// the precomputed Dijkstra parent trees.
func (h *Hierarchy) refine(sequence []int, startID, goalID int, sParent, gParent map[int]int) []Cell {
	g := h.grid
	firstEntrance := sequence[1]
	cells := treePathCells(g, sParent, startID, firstEntrance)

	for i := 1; i < len(sequence)-1; i++ {
		from, to := sequence[i], sequence[i+1]
		if to == tempGoalID {
			cells = appendUnique(cells, treePathToRoot(g, gParent, from))
			continue
		}
		fc, tc := g.CellOf(from), g.CellOf(to)
		fcx, fcy := h.clusterIndexAt(fc.X, fc.Y)
		tcx, tcy := h.clusterIndexAt(tc.X, tc.Y)
		if fcx == tcx && fcy == tcy {
			tree := h.clusters[fcy][fcx].parent[from]
			cells = appendUnique(cells, treePathCells(g, tree, from, to)[1:])
		} else {
			cells = appendUnique(cells, []Cell{tc})
		}
	}
	return cells
}

// appendUnique appends extra cells while skipping any that repeat the current
// tail. Consecutive abstract inter-edges can touch the same boundary cell.
func appendUnique(dst, extra []Cell) []Cell {
	for _, c := range extra {
		if len(dst) > 0 && dst[len(dst)-1] == c {
			continue
		}
		dst = append(dst, c)
	}
	return dst
}

// treePathCells reconstructs the cell sequence from root to target by walking
// the Dijkstra parent tree upward from target.
func treePathCells(g *Grid, parent map[int]int, root, target int) []Cell {
	rev := []int{target}
	for cur := target; cur != root; {
		p, ok := parent[cur]
		if !ok {
			break
		}
		rev = append(rev, p)
		cur = p
	}
	cells := make([]Cell, len(rev))
	for i, id := range rev {
		cells[len(rev)-1-i] = g.CellOf(id)
	}
	return cells
}

// treePathToRoot walks a parent tree upward from target and returns the cell
// sequence target -> root in traversal order. It is used for the goal-side
// insertion tree, whose root is the query goal.
func treePathToRoot(g *Grid, parent map[int]int, target int) []Cell {
	ids := []int{target}
	for cur := target; ; {
		p, ok := parent[cur]
		if !ok {
			break
		}
		ids = append(ids, p)
		cur = p
	}
	cells := make([]Cell, len(ids))
	for i, id := range ids {
		cells[i] = g.CellOf(id)
	}
	return cells
}

// RebuildCount reports the total number of cluster rebuilds performed.
func (h *Hierarchy) RebuildCount() int { return h.rebuildCount }

// ClusterDims reports the number of cluster columns and rows.
func (h *Hierarchy) ClusterDims() (cols, rows int) { return h.cols, h.rows }
