package paretopath

func BruteForceParetoPaths(g *Graph, src, dst int) ([]Path, error) {
	if err := checkEndpoints(g, src, dst); err != nil {
		return nil, err
	}
	if src == dst {
		return []Path{{}}, nil
	}

	var labels []Label
	var walk func(l Label)
	walk = func(l Label) {
		if l.At == dst {
			labels = append(labels, l)
			return
		}
		for _, e := range g.adj[l.At] {
			bit := uint16(1) << uint(e.To)
			if l.Visited&bit != 0 {
				continue
			}
			edges := make([]Edge, len(l.Edges)+1)
			copy(edges, l.Edges)
			edges[len(l.Edges)] = e
			walk(Label{
				At:      e.To,
				Time:    l.Time + e.Time,
				Cost:    l.Cost + e.Cost,
				Visited: l.Visited | bit,
				Edges:   edges,
			})
		}
	}
	walk(Label{At: src, Visited: 1 << uint(src)})
	return paretoFront(labels), nil
}
