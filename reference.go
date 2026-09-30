package dvsim

// ReferenceDistances computes all-pairs shortest path costs over the
// links that are currently up, using Dijkstra from every node. It is
// the ground truth the distance-vector tables are checked against.
func (s *Sim) ReferenceDistances() map[int]map[int]int {
	out := make(map[int]map[int]int, len(s.nodes))
	for _, id := range s.NodeIDs() {
		out[id] = s.dijkstra(id)
	}
	return out
}

func (s *Sim) dijkstra(src int) map[int]int {
	dist := map[int]int{src: 0}
	done := make(map[int]bool, len(s.nodes))
	for {
		u, best := -1, 0
		for _, id := range s.NodeIDs() {
			d, ok := dist[id]
			if ok && !done[id] && (u == -1 || d < best) {
				u, best = id, d
			}
		}
		if u == -1 {
			return dist
		}
		done[u] = true
		for peer, l := range s.nodes[u].links {
			if !l.Up {
				continue
			}
			nd := best + l.Cost
			if cur, ok := dist[peer]; !ok || nd < cur {
				dist[peer] = nd
			}
		}
	}
}
