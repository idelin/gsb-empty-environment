package dvsim

import "fmt"

// ReferenceDistances computes all-pairs shortest path metrics over the
// currently-up links with Bellman-Ford. Unreachable pairs get Infinity.
func ReferenceDistances(n *Network) map[string]map[string]int {
	ids := n.RouterIDs()
	dist := make(map[string]map[string]int, len(ids))
	for _, src := range ids {
		d := make(map[string]int, len(ids))
		for _, id := range ids {
			d[id] = Infinity
		}
		d[src] = 0
		for i := 0; i < len(ids)-1; i++ {
			changed := false
			for _, l := range n.links {
				if !l.Up {
					continue
				}
				for _, pair := range [][2]string{{l.A, l.B}, {l.B, l.A}} {
					a, b := pair[0], pair[1]
					if d[a]+l.Cost < d[b] && d[a] < Infinity {
						d[b] = d[a] + l.Cost
						changed = true
					}
				}
			}
			if !changed {
				break
			}
		}
		dist[src] = d
	}
	return dist
}

// VerifyAgainstReference compares every router's table with the reference
// shortest paths of the current topology. It returns a list of mismatches;
// an empty slice means the simulation converged to the reference.
func VerifyAgainstReference(n *Network) []string {
	ref := ReferenceDistances(n)
	var problems []string
	for _, id := range n.RouterIDs() {
		r := n.routers[id]
		for _, dest := range n.RouterIDs() {
			want := ref[id][dest]
			metric, ok := r.Metric(dest)
			if !ok {
				metric = Infinity
			}
			if want >= Infinity {
				if metric < Infinity {
					problems = append(problems, fmt.Sprintf(
						"%s: dest %s should be unreachable, has metric %d", id, dest, metric))
				}
				continue
			}
			if metric != want {
				problems = append(problems, fmt.Sprintf(
					"%s: dest %s metric %d, want %d", id, dest, metric, want))
				continue
			}
			if dest != id {
				next, _ := r.NextHop(dest)
				nb, ok := r.neighbors[next]
				if !ok || !nb.up {
					problems = append(problems, fmt.Sprintf(
						"%s: dest %s next hop %q is not an up neighbor", id, dest, next))
					continue
				}
				if nb.cost+ref[next][dest] != want {
					problems = append(problems, fmt.Sprintf(
						"%s: dest %s next hop %s is not on a shortest path", id, dest, next))
				}
			}
		}
	}
	return problems
}
