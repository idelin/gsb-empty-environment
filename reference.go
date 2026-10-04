package hexgrid

// ReferenceBFS 是不分区的参考实现：直接在平面单元表上 BFS，
// 用于交叉验证分区引擎的结果（可达集、最短步数、决胜路径必须完全一致）。
func ReferenceBFS(cells map[Hex]bool, start Hex, k int, target *Hex) (Result, error) {
	if k < 0 {
		return Result{}, nil
	}
	passable := func(h Hex) (bool, error) { return cells[h], nil }
	dist, paths, err := bfs(start, k, passable)
	if err != nil {
		return Result{}, err
	}
	reachable := make([]Hex, 0, len(dist))
	for h := range dist {
		reachable = append(reachable, h)
	}
	SortHexes(reachable)
	res := Result{Reachable: reachable, Dist: dist, Steps: -1}
	if target != nil {
		if d, ok := dist[*target]; ok {
			res.Steps = d
			res.Path = paths[*target]
		}
	}
	return res, nil
}
