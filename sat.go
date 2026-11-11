package netsec

// solve finds a satisfying assignment for clauses over numVars variables
// using DPLL with unit propagation. The second return value reports
// whether the clause set is satisfiable. Unassigned variables in a
// satisfying leaf default to false.
func solve(numVars int, clauses []Clause) (Assignment, bool) {
	vals := make([]int8, numVars) // 0 unset, 1 true, -1 false
	if !dpll(clauses, vals) {
		return Assignment{}, false
	}
	out := Assignment{Values: make([]bool, numVars)}
	for i, v := range vals {
		out.Values[i] = v == 1
	}
	return out, true
}

func dpll(clauses []Clause, vals []int8) bool {
	// Unit propagation until fixpoint.
	for {
		progressed := false
		for _, c := range clauses {
			satisfied := false
			open := 0
			for _, lit := range c {
				v := vals[lit.Var]
				switch {
				case v == 0:
					open++
				case (v == 1) != lit.Neg:
					satisfied = true
				}
				if satisfied {
					break
				}
			}
			if satisfied {
				continue
			}
			if open == 0 {
				return false // conflicting clause (empty clauses land here)
			}
			if open == 1 {
				for _, lit := range c {
					if vals[lit.Var] == 0 {
						if lit.Neg {
							vals[lit.Var] = -1
						} else {
							vals[lit.Var] = 1
						}
						break
					}
				}
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}

	pick := -1
	for i := range vals {
		if vals[i] == 0 {
			pick = i
			break
		}
	}
	if pick == -1 {
		return true
	}
	for _, v := range []int8{1, -1} {
		child := make([]int8, len(vals))
		copy(child, vals)
		child[pick] = v
		if dpll(clauses, child) {
			copy(vals, child)
			return true
		}
	}
	return false
}
