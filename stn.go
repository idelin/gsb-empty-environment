package tempsched

import "math"

// edge is a simple temporal constraint: x[to] <= x[from] + w.
type edge struct {
	from, to int
	w        float64
}

// floydWarshall computes all-pairs shortest paths over the constraint graph.
// d[i][j] is the tightest upper bound on x[j]-x[i]. It returns ok=false when
// the network contains a negative cycle, i.e. the constraints are inconsistent.
func floydWarshall(n int, edges []edge) (d [][]float64, ok bool) {
	d = make([][]float64, n)
	for i := range d {
		d[i] = make([]float64, n)
		for j := range d[i] {
			d[i][j] = math.Inf(1)
		}
		d[i][i] = 0
	}
	for _, e := range edges {
		if e.w < d[e.from][e.to] {
			d[e.from][e.to] = e.w
		}
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			if math.IsInf(d[i][k], 1) {
				continue
			}
			for j := 0; j < n; j++ {
				if math.IsInf(d[k][j], 1) {
					continue
				}
				if nd := d[i][k] + d[k][j]; nd < d[i][j] {
					d[i][j] = nd
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		if d[i][i] < 0 {
			return nil, false
		}
	}
	return d, true
}
