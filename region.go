package floodsim

// Point 为栅格平面坐标点。
type Point struct {
	X, Y float64
}

// Region 为某一时刻的一片积水连通区域（4 邻接）。
type Region struct {
	// Cells 为区域内单元线性索引集合。
	Cells []int
	// Boundary 为区域外边界多边形顶点环（逆时针，首尾不重复闭合）。
	Boundary []Point
	// Holes 为区域内部孔洞边界环（顺时针）。
	Holes [][]Point
}

// RegionsAt 计算历史时刻 t 的全部淹没连通区域及其边界多边形。
// t 越界时返回 nil。
func (s *Simulator) RegionsAt(t int) []Region {
	snap := s.SnapshotAt(t)
	if snap == nil {
		return nil
	}
	return ExtractRegions(s.Grid, snap.Depth, s.FloodThreshold)
}

// ExtractRegions 对给定水深场做连通域划分与边界提取，便于独立测试。
func ExtractRegions(g *Grid, depth []float64, threshold float64) []Region {
	n := g.Width * g.Height
	flooded := make([]bool, n)
	for i := 0; i < n; i++ {
		flooded[i] = depth[i] > threshold
	}
	visited := make([]bool, n)
	var regions []Region
	for start := 0; start < n; start++ {
		if !flooded[start] || visited[start] {
			continue
		}
		// BFS 收集连通单元
		var cells []int
		queue := []int{start}
		visited[start] = true
		inRegion := make(map[int]bool)
		for len(queue) > 0 {
			c := queue[0]
			queue = queue[1:]
			cells = append(cells, c)
			inRegion[c] = true
			x, y := c%g.Width, c/g.Width
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				j := g.Index(x+d[0], y+d[1])
				if j >= 0 && flooded[j] && !visited[j] {
					visited[j] = true
					queue = append(queue, j)
				}
			}
		}
		r := Region{Cells: cells}
		r.Boundary, r.Holes = traceBoundary(g, inRegion)
		regions = append(regions, r)
	}
	return regions
}

// traceBoundary 提取区域边界环。
// 对每个区域单元，凡邻居不属于区域的边即为边界边；
// 所有边界边按“内部在左”定向后首尾相接成环：
// 外边界为逆时针环，孔洞为顺时针环。
func traceBoundary(g *Grid, inRegion map[int]bool) (outer []Point, holes [][]Point) {
	type vertex struct{ x, y int }
	type edge struct{ from, to vertex }
	var edges []edge
	for c := range inRegion {
		x, y := c%g.Width, c/g.Width
		// 底边 (x,y)->(x+1,y)
		if !inRegion[g.Index(x, y-1)] {
			edges = append(edges, edge{vertex{x, y}, vertex{x + 1, y}})
		}
		// 右边 (x+1,y)->(x+1,y+1)
		if !inRegion[g.Index(x+1, y)] {
			edges = append(edges, edge{vertex{x + 1, y}, vertex{x + 1, y + 1}})
		}
		// 顶边 (x+1,y+1)->(x,y+1)
		if !inRegion[g.Index(x, y+1)] {
			edges = append(edges, edge{vertex{x + 1, y + 1}, vertex{x, y + 1}})
		}
		// 左边 (x,y+1)->(x,y)
		if !inRegion[g.Index(x-1, y)] {
			edges = append(edges, edge{vertex{x, y + 1}, vertex{x, y}})
		}
	}
	// 以起点为键链接成环
	next := make(map[vertex][]vertex)
	for _, e := range edges {
		next[e.from] = append(next[e.from], e.to)
	}
	var loops [][]Point
	for len(next) > 0 {
		var start vertex
		for v := range next {
			start = v
			break
		}
		loop := []Point{{float64(start.x), float64(start.y)}}
		cur := start
		for {
			tos := next[cur]
			if len(tos) == 0 {
				break
			}
			nx := tos[0]
			next[cur] = tos[1:]
			if len(next[cur]) == 0 {
				delete(next, cur)
			}
			if nx == start {
				break
			}
			loop = append(loop, Point{float64(nx.x), float64(nx.y)})
			cur = nx
		}
		loops = append(loops, loop)
	}
	// 面积最大（且为正/逆时针）的环为外边界，其余为孔洞
	best, bestArea := -1, 0.0
	for i, lp := range loops {
		a := ringArea(lp)
		if a > bestArea {
			bestArea, best = a, i
		}
	}
	if best >= 0 {
		outer = loops[best]
		for i, lp := range loops {
			if i != best {
				holes = append(holes, lp)
			}
		}
	}
	return outer, holes
}

// ringArea 返回环的有向面积（逆时针为正）。
func ringArea(ring []Point) float64 {
	a := 0.0
	for i := 0; i < len(ring); i++ {
		j := (i + 1) % len(ring)
		a += ring[i].X*ring[j].Y - ring[j].X*ring[i].Y
	}
	return a / 2
}
