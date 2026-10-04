package strsolver

// BruteForce enumerates every assignment within the variable bounds and
// returns the smallest one satisfying all constraints, using the same
// ordering as Solve. It is exponential and intended for cross-checking
// Solve on small instances.
func BruteForce(p *Problem) Result {
	n := len(p.Vars)
	var alpha []byte
	var seen [256]bool
	for i := 0; i < len(p.Alphabet); i++ {
		if !seen[p.Alphabet[i]] {
			seen[p.Alphabet[i]] = true
			alpha = append(alpha, p.Alphabet[i])
		}
	}
	var best []string
	var states int64
	cur := make([]string, n)
	var rec func(v int)
	rec = func(v int) {
		if v == n {
			states++
			if Verify(p, cur) == nil {
				cand := append([]string(nil), cur...)
				if best == nil || lessAssign(cand, best) {
					best = cand
				}
			}
			return
		}
		vb := p.Vars[v]
		for l := vb.Min; l <= vb.Max; l++ {
			buf := make([]byte, l)
			var gen func(pos int)
			gen = func(pos int) {
				if pos == l {
					cur[v] = string(buf)
					rec(v + 1)
					return
				}
				for _, ch := range alpha {
					buf[pos] = ch
					gen(pos + 1)
				}
			}
			gen(0)
		}
	}
	rec(0)
	if best == nil {
		return Result{Status: Unsat, States: states}
	}
	return Result{Status: Sat, Assignment: best, States: states}
}
