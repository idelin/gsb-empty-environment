package markov

import (
	"fmt"
	"math/big"
	"sort"
)

// StateResult is the analysis outcome for one start state.
type StateResult struct {
	State int
	// HitProbability is the exact probability of ever reaching the
	// absorbing set. Zero means the set is never reached.
	HitProbability *big.Rat
	// ConditionalExpectedSteps is E[T | T < infinity], the expected
	// number of steps to reach the absorbing set conditioned on ever
	// reaching it. It is nil exactly when HitProbability is zero
	// (the chain never reaches the set, so conditioning is undefined).
	ConditionalExpectedSteps *big.Rat
}

// UnconditionalExpectedSteps returns E[T] with the convention
// T = infinity when the absorbing set is never reached. infinite is
// true exactly when HitProbability < 1; in that case value is nil.
func (r StateResult) UnconditionalExpectedSteps() (value *big.Rat, infinite bool) {
	if r.HitProbability.Cmp(big.NewRat(1, 1)) == 0 {
		return new(big.Rat).Set(r.ConditionalExpectedSteps), false
	}
	return nil, true
}

// Analysis is the result of Analyze.
type Analysis struct {
	// Absorbing is the validated target set, sorted ascending.
	Absorbing []int
	// Results[i] is the outcome for start state i.
	Results []StateResult
	// ClosedClasses lists the closed communicating classes disjoint
	// from the absorbing set (states trapped in them never reach it).
	// Each class is sorted ascending and the list is sorted by first
	// member, so the output is deterministic.
	ClosedClasses [][]int
}

// Analyze computes, for every start state, the hitting probability of
// the absorbing set and the conditional expected hitting time.
//
// Hitting probabilities h solve h_i = sum_j p_ij h_j with h = 1 on the
// absorbing set. When closed non-absorbing classes exist this system is
// singular; the hitting probability is its minimal non-negative
// solution, obtained here by restricting the linear system to the
// states that can reach the absorbing set (h = 0 on the rest), which
// makes the reduced matrix I-Q nonsingular.
//
// Conditional expectations use g_i = E_i[T * 1{T < infinity}], which
// solves g_i = h_i + sum_j p_ij g_j on the same reduced state set;
// then E_i[T | T < infinity] = g_i / h_i.
func (c *Chain) Analyze(absorbing []int) (*Analysis, error) {
	if len(absorbing) == 0 {
		return nil, fmt.Errorf("absorbing set must be non-empty")
	}
	inA := make([]bool, c.N)
	for _, s := range absorbing {
		if s < 0 || s >= c.N {
			return nil, fmt.Errorf("absorbing state %d out of range [0,%d)", s, c.N)
		}
		if inA[s] {
			return nil, fmt.Errorf("absorbing state %d listed twice", s)
		}
		inA[s] = true
	}
	target := append([]int(nil), absorbing...)
	sort.Ints(target)

	// States that can reach the absorbing set (reverse BFS).
	rev := make([][]int, c.N)
	for i := 0; i < c.N; i++ {
		for j := 0; j < c.N; j++ {
			if c.P[i][j].Sign() > 0 {
				rev[j] = append(rev[j], i)
			}
		}
	}
	reach := make([]bool, c.N)
	queue := append([]int(nil), absorbing...)
	for _, s := range queue {
		reach[s] = true
	}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, pre := range rev[s] {
			if !reach[pre] {
				reach[pre] = true
				queue = append(queue, pre)
			}
		}
	}

	// Unknowns: states that can reach the set but are not in it.
	idx := make([]int, c.N)
	var unknowns []int
	for i := 0; i < c.N; i++ {
		idx[i] = -1
		if reach[i] && !inA[i] {
			idx[i] = len(unknowns)
			unknowns = append(unknowns, i)
		}
	}

	one := big.NewRat(1, 1)

	h := make([]*big.Rat, c.N)
	g := make([]*big.Rat, c.N)
	for i := 0; i < c.N; i++ {
		h[i] = new(big.Rat)
		g[i] = new(big.Rat)
	}
	for _, s := range absorbing {
		h[s].Set(one)
	}

	if len(unknowns) > 0 {
		m := len(unknowns)
		// Shared coefficient matrix I - Q for both systems.
		mat := make([][]*big.Rat, m)
		bh := make([]*big.Rat, m)
		for r, i := range unknowns {
			mat[r] = make([]*big.Rat, m)
			for c2, j := range unknowns {
				mat[r][c2] = new(big.Rat).Neg(c.P[i][j])
			}
			mat[r][r].Add(mat[r][r], one)
			bh[r] = new(big.Rat)
			for j := 0; j < c.N; j++ {
				if inA[j] {
					bh[r].Add(bh[r], c.P[i][j])
				}
			}
		}
		hsol, err := solveLinear(mat, bh)
		if err != nil {
			return nil, fmt.Errorf("solving hitting probabilities: %v", err)
		}
		for r, i := range unknowns {
			h[i].Set(hsol[r])
		}
		// g_i = h_i + sum_j p_ij g_j, same matrix, right-hand side h.
		gsol, err := solveLinear(mat, hOf(h, unknowns))
		if err != nil {
			return nil, fmt.Errorf("solving expected steps: %v", err)
		}
		for r, i := range unknowns {
			g[i].Set(gsol[r])
		}
	}

	results := make([]StateResult, c.N)
	for i := 0; i < c.N; i++ {
		results[i].State = i
		results[i].HitProbability = h[i]
		if h[i].Sign() > 0 {
			results[i].ConditionalExpectedSteps = new(big.Rat).Quo(g[i], h[i])
		}
	}

	return &Analysis{
		Absorbing:     target,
		Results:       results,
		ClosedClasses: c.closedNonAbsorbingClasses(inA),
	}, nil
}

func hOf(h []*big.Rat, unknowns []int) []*big.Rat {
	b := make([]*big.Rat, len(unknowns))
	for r, i := range unknowns {
		b[r] = new(big.Rat).Set(h[i])
	}
	return b
}

// closedNonAbsorbingClasses returns the closed communicating classes
// (Tarjan SCCs with no outgoing edge) disjoint from the absorbing set.
func (c *Chain) closedNonAbsorbingClasses(inA []bool) [][]int {
	sccs := c.stronglyConnectedComponents()
	var classes [][]int
	for _, scc := range sccs {
		member := make([]bool, c.N)
		disjoint := true
		for _, s := range scc {
			member[s] = true
			if inA[s] {
				disjoint = false
			}
		}
		if !disjoint {
			continue
		}
		closed := true
		for _, s := range scc {
			for j := 0; j < c.N; j++ {
				if c.P[s][j].Sign() > 0 && !member[j] {
					closed = false
				}
			}
		}
		if closed {
			sort.Ints(scc)
			classes = append(classes, scc)
		}
	}
	sort.Slice(classes, func(a, b int) bool { return classes[a][0] < classes[b][0] })
	return classes
}

// stronglyConnectedComponents runs Tarjan's algorithm on the transition
// graph (edge i->j when p_ij > 0).
func (c *Chain) stronglyConnectedComponents() [][]int {
	n := c.N
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	counter := 0
	var sccs [][]int

	var visit func(v int)
	visit = func(v int) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for w := 0; w < n; w++ {
			if c.P[v][w].Sign() == 0 {
				continue
			}
			if index[w] == -1 {
				visit(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			var scc []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}
	for v := 0; v < n; v++ {
		if index[v] == -1 {
			visit(v)
		}
	}
	return sccs
}

// solveLinear solves A x = b exactly over the rationals by Gauss-Jordan
// elimination. A and b are not modified.
func solveLinear(a [][]*big.Rat, b []*big.Rat) ([]*big.Rat, error) {
	n := len(b)
	m := make([][]*big.Rat, n)
	for i := range m {
		m[i] = make([]*big.Rat, n+1)
		for j := 0; j < n; j++ {
			m[i][j] = new(big.Rat).Set(a[i][j])
		}
		m[i][n] = new(big.Rat).Set(b[i])
	}
	for col := 0; col < n; col++ {
		piv := -1
		for r := col; r < n; r++ {
			if m[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			return nil, fmt.Errorf("coefficient matrix is singular")
		}
		m[col], m[piv] = m[piv], m[col]
		inv := new(big.Rat).Inv(m[col][col])
		for j := col; j <= n; j++ {
			m[col][j].Mul(m[col][j], inv)
		}
		for r := 0; r < n; r++ {
			if r == col || m[r][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(m[r][col])
			for j := col; j <= n; j++ {
				m[r][j].Sub(m[r][j], new(big.Rat).Mul(f, m[col][j]))
			}
		}
	}
	x := make([]*big.Rat, n)
	for i := range x {
		x[i] = m[i][n]
	}
	return x, nil
}
