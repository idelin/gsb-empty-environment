package coupling

import "math/big"

// maxFlow runs Edmonds-Karp on the residual matrix res (mutated) with
// source s and sink t. All capacities are non-negative integers, so the
// computation is exact and always terminates. It returns the net flow
// matrix (antisymmetric: flow[u][v] = -flow[v][u]) and the set of nodes
// reachable from s in the final residual graph, which is the source
// side of a minimum cut.
func maxFlow(n, s, t int, res [][]*big.Int) (flow [][]*big.Int, reach []bool) {
	flow = make([][]*big.Int, n)
	for i := range flow {
		flow[i] = make([]*big.Int, n)
		for j := range flow[i] {
			flow[i][j] = new(big.Int)
		}
	}

	prev := make([]int, n)
	for {
		for i := range prev {
			prev[i] = -2
		}
		prev[s] = -1
		queue := []int{s}
		for len(queue) > 0 && prev[t] == -2 {
			u := queue[0]
			queue = queue[1:]
			for v := 0; v < n; v++ {
				if prev[v] == -2 && res[u][v].Sign() > 0 {
					prev[v] = u
					queue = append(queue, v)
				}
			}
		}
		if prev[t] == -2 {
			break
		}

		// Bottleneck capacity along the augmenting path.
		d := new(big.Int)
		for v := t; v != s; v = prev[v] {
			u := prev[v]
			if d.Sign() == 0 || res[u][v].Cmp(d) < 0 {
				d.Set(res[u][v])
			}
		}
		for v := t; v != s; v = prev[v] {
			u := prev[v]
			res[u][v].Sub(res[u][v], d)
			res[v][u].Add(res[v][u], d)
			flow[u][v].Add(flow[u][v], d)
			flow[v][u].Sub(flow[v][u], d)
		}
	}

	reach = make([]bool, n)
	reach[s] = true
	queue := []int{s}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for v := 0; v < n; v++ {
			if !reach[v] && res[u][v].Sign() > 0 {
				reach[v] = true
				queue = append(queue, v)
			}
		}
	}
	return flow, reach
}
