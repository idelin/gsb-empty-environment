package dynconn

import "fmt"

// SolveBruteForce answers the same queries as Solve by rebuilding the
// graph from scratch at every queried point in time. It is the
// reference implementation used to cross-check Solve.
func SolveBruteForce(numVertices int, ops []Op, queries []Query) ([]bool, error) {
	if err := validate(numVertices, ops, queries); err != nil {
		return nil, err
	}
	ans := make([]bool, len(queries))
	counts := make(map[edge]int)
	for qi, q := range queries {
		for k := range counts {
			delete(counts, k)
		}
		for i := 0; i < q.Time; i++ {
			op := ops[i]
			e := canonical(op.U, op.V)
			switch op.Kind {
			case AddEdge:
				counts[e]++
			case RemoveEdge:
				if counts[e] == 0 {
					return nil, fmt.Errorf("dynconn: op %d removes edge (%d,%d) with no matching add", i+1, op.U, op.V)
				}
				counts[e]--
				if counts[e] == 0 {
					delete(counts, e)
				}
			default:
				return nil, fmt.Errorf("dynconn: op %d has unknown kind %d", i+1, op.Kind)
			}
		}
		d := newRollbackDSU(numVertices)
		for e := range counts {
			d.union(e.u, e.v)
		}
		ans[qi] = d.connected(q.U, q.V)
	}
	return ans, nil
}
