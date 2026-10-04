package main

import "fmt"

// solveAll runs the full pipeline: close the cost table, compute the
// Zhang-Shasha distance against the closed costs, backtrace an optimal
// mapping and generate an executable edit script of primitive operations.
func solveAll(source, target *Node, raw *CostTable) (dist int, ops []Op, m *rawMapping, err error) {
	universe := raw.labelUniverse(source, target)
	cc := raw.close(universe)
	t1 := indexTree(source)
	t2 := indexTree(target)
	eng := newEngine(t1, t2, cc.eff)
	dist = eng.distance()
	m = eng.backtrace()
	ops, err = generateScript(t1, t2, m, cc, raw)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("script generation: %w", err)
	}
	return dist, ops, m, nil
}
