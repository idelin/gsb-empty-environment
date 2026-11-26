package reshard

import "math/big"

// edge is one legal primitive together with its exact, LCM-scaled cost.
type edge struct {
	op     Op
	next   state
	weight *big.Int
}

type solver struct {
	problem Problem
	lcm     int64
}

func newSolver(p Problem) *solver {
	lcm := int64(1)
	for _, m := range p.Mesh.Ranks {
		lcm = lcm / gcd(lcm, m) * m
	}
	return &solver{problem: p, lcm: lcm}
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// canShard reports whether tensor dimension d can be split along mesh axis a
// without producing non-integral shards.
func (solver *solver) canShard(d, a int) bool {
	return solver.problem.Shape[d]%solver.problem.Mesh.Ranks[a] == 0
}

// scaledCost turns a primitive volume (factor * bytes * (M_a-1)/M_a * w_a)
// into the LCM-scaled integer factor * bytes * (M_a-1) * (LCM/M_a) * w_a.
func (solver *solver) scaledCost(a int, bytes *big.Int, factor int64) *big.Int {
	m := solver.problem.Mesh.Ranks[a]
	cost := new(big.Int).Set(bytes)
	cost.Mul(cost, big.NewInt(m-1))
	cost.Mul(cost, big.NewInt(solver.lcm/m))
	cost.Mul(cost, big.NewInt(solver.problem.Mesh.Weights[a]))
	if factor != 1 {
		cost.Mul(cost, big.NewInt(factor))
	}
	return cost
}

// edges enumerates every legal single-step primitive from s.
func (solver *solver) edges(s state) []edge {
	var out []edge
	meshAxes := len(solver.problem.Mesh.Ranks)
	dims := len(s.shard)

	for a := 0; a < meshAxes; a++ {
		switch {
		case s.partial[a]:
			// AllReduce: clear Partial while keeping full replication.
			next := s.clone()
			next.partial[a] = false
			out = append(out, edge{
				op:     Op{Type: AllReduce, A: a},
				next:   next,
				weight: solver.scaledCost(a, solver.perCardBytes(s), 2),
			})
			// ReduceScatter: clear Partial and shard one replicated,
			// integrally splittable dimension along the same axis.
			for d2 := 0; d2 < dims; d2++ {
				if s.shard[d2] != -1 || !solver.canShard(d2, a) {
					continue
				}
				next := s.clone()
				next.partial[a] = false
				next.shard[d2] = a
				out = append(out, edge{
					op:     Op{Type: ReduceScatter, A: a, D2: d2},
					next:   next,
					weight: solver.scaledCost(a, solver.perCardBytes(s), 1),
				})
			}
		case axisShard(s, a) >= 0:
			d1 := axisShard(s, a)
			// AllGather: drop the sharding along axis a.
			nextAG := s.clone()
			nextAG.shard[d1] = -1
			out = append(out, edge{
				op:     Op{Type: AllGather, A: a, D1: d1},
				next:   nextAG,
				weight: solver.scaledCost(a, fullBytes(solver.problem), 1),
			})
			// AllToAll: dimension d1, currently sharded along axis a, becomes
			// dimension d2 sharded along any free axis b whose size splits
			// d2 integrally. The communication happens over axis a and moves
			// the current per-card bytes once. b == a models a same-axis
			// sharding swap; b != a models moving the shard across mesh axes.
			for b := 0; b < meshAxes; b++ {
				if s.partial[b] || (b != a && axisShard(s, b) >= 0) {
					continue
				}
				for d2 := 0; d2 < dims; d2++ {
					if d2 == d1 || s.shard[d2] != -1 || !solver.canShard(d2, b) {
						continue
					}
					next := s.clone()
					next.shard[d1] = -1
					next.shard[d2] = b
					out = append(out, edge{
						op:     Op{Type: AllToAll, A: a, B: b, D1: d1, D2: d2},
						next:   next,
						weight: solver.scaledCost(a, solver.perCardBytes(s), 1),
					})
				}
			}
		default:
			// Free axis: LocalSlice introduces axis-a sharding on one
			// replicated, integrally splittable dimension at zero cost.
			// It remains a legal no-op when the axis holds a single device.
			for d2 := 0; d2 < dims; d2++ {
				if s.shard[d2] != -1 || !solver.canShard(d2, a) {
					continue
				}
				next := s.clone()
				next.shard[d2] = a
				out = append(out, edge{
					op:     Op{Type: LocalSlice, A: a, D2: d2},
					next:   next,
					weight: new(big.Int),
				})
			}
		}
	}
	return out
}

func axisShard(s state, a int) int {
	for d, ax := range s.shard {
		if ax == a {
			return d
		}
	}
	return -1
}
