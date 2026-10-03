package grid

// 覆盖连续性校验：纯整数拓扑证明一组单元的并集连续闭合、无空洞遗漏。
//
// 原理：把每个六边形的 6 条边计入多重集，被两个单元共享的边相互抵消，
// 剩下的即边界边。若边界边在顶点图上每个顶点度为 2 且整体连通，
// 则边界是单一闭合环 —— 集合连通、无洞、无重叠（空洞会产生第二个环，
// 不连通会产生多个环，重叠会产生重复单元）。

type edgeKey struct{ q1, r1, q2, r2 int64 }

// vertexKey 六边形顶点的整数坐标（3 倍缩放格点，避免分数）。
type vertexKey struct{ q, r int64 }

// vertexOf 单元 a 在方向 i 与方向 i+1 之间的顶点：V = 3a + e_i + e_{i+1}。
func vertexOf(a axial, i int) vertexKey {
	d1 := unitDirs[i%6]
	d2 := unitDirs[(i+1)%6]
	return vertexKey{3*a.q + d1.q + d2.q, 3*a.r + d1.r + d2.r}
}

func dirIndex(d axial) int {
	for i, u := range unitDirs {
		if u == d {
			return i
		}
	}
	return -1
}

// VerifyContiguousClosed 校验单元集合连续闭合无空洞。
// 返回边界边数与是否通过。要求所有单元同分辨率、同基单元、无重复。
// 半径 k 的完整 K-Ring 边界边数应为 12(k+1)-6。
func VerifyContiguousClosed(cells []Index) (boundaryEdges int, ok bool) {
	if len(cells) == 0 {
		return 0, false
	}
	res := cells[0].Res()
	bc := cells[0].BaseCell()
	edgeCount := make(map[edgeKey]int, len(cells)*3)
	seen := make(map[Index]struct{}, len(cells))
	for _, h := range cells {
		if !h.IsValid() || h.Res() != res || h.BaseCell() != bc {
			return 0, false
		}
		if _, dup := seen[h]; dup {
			return 0, false
		}
		seen[h] = struct{}{}
		t := localAxial(h)
		for d := 0; d < 6; d++ {
			dir := unitDirs[d]
			n := axial{t.q + dir.q, t.r + dir.r}
			var k edgeKey
			if t.q < n.q || (t.q == n.q && t.r < n.r) {
				k = edgeKey{t.q, t.r, n.q, n.r}
			} else {
				k = edgeKey{n.q, n.r, t.q, t.r}
			}
			edgeCount[k]++
		}
	}
	// 收集边界边，构建顶点邻接图。
	adj := make(map[vertexKey][]vertexKey)
	for k, c := range edgeCount {
		if c != 1 {
			continue
		}
		boundaryEdges++
		a := axial{k.q1, k.r1}
		d := dirIndex(axial{k.q2 - k.q1, k.r2 - k.r1})
		if d < 0 {
			return 0, false
		}
		v1 := vertexOf(a, d)
		v2 := vertexOf(a, (d+5)%6)
		adj[v1] = append(adj[v1], v2)
		adj[v2] = append(adj[v2], v1)
	}
	if boundaryEdges == 0 {
		return 0, false
	}
	// 每个边界顶点度必须为 2（否则边界分叉或自交）。
	for _, ns := range adj {
		if len(ns) != 2 {
			return boundaryEdges, false
		}
	}
	// 边界必须连通成单一环（BFS 覆盖全部顶点）。
	var start vertexKey
	for v := range adj {
		start = v
		break
	}
	visited := map[vertexKey]bool{start: true}
	queue := []vertexKey{start}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, n := range adj[v] {
			if !visited[n] {
				visited[n] = true
				queue = append(queue, n)
			}
		}
	}
	if len(visited) != len(adj) {
		return boundaryEdges, false
	}
	return boundaryEdges, true
}
