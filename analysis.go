package main

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
)

// ClassInfo describes one closed communicating class.
type ClassInfo struct {
	ID        int   `json:"id"`
	States    []int `json:"states"`
	Absorbing bool  `json:"absorbing"`
}

// ReachEvidence is a witness that a closed class is reachable from a state.
type ReachEvidence struct {
	ClassID int   `json:"classId"`
	Path    []int `json:"path"`
}

// StateResult holds the per-state analysis outcome.
type StateResult struct {
	State         int               `json:"state"`
	InClosedClass bool              `json:"inClosedClass"`
	ExpectedSteps string            `json:"expectedSteps"` // exact rational, "Infinity", or "Undefined"
	Absorption    map[string]string `json:"absorptionProbabilities"`
	Reachable     []ReachEvidence   `json:"reachableClosedClasses"`
}

// Check is one independent substitution-back verification result.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Analysis is the full analyzer output.
type Analysis struct {
	ClosedClasses []ClassInfo   `json:"closedClasses"`
	States        []StateResult `json:"states"`
	Verification  []Check       `json:"verification"`
}

func adjacency(c *Chain) [][]int {
	g := make([][]int, c.N)
	for i := 0; i < c.N; i++ {
		for j := 0; j < c.N; j++ {
			if c.P[i][j].Sign() > 0 {
				g[i] = append(g[i], j)
			}
		}
	}
	return g
}

// Analyze computes, for every state, the exact absorption probabilities into
// each closed class and the expected number of steps to absorption. States
// that can reach a non-absorbing closed class get ExpectedSteps "Infinity";
// singular systems are never solved into fake finite numbers.
func Analyze(c *Chain) *Analysis {
	n := c.N
	g := adjacency(c)

	comps := stronglyConnectedComponents(g)
	compOf := make([]int, n)
	for id, comp := range comps {
		for _, v := range comp {
			compOf[v] = id
		}
	}
	var closedComps [][]int
	for _, comp := range comps {
		isClosed := true
		for _, v := range comp {
			for _, w := range g[v] {
				if compOf[w] != compOf[v] {
					isClosed = false
					break
				}
			}
			if !isClosed {
				break
			}
		}
		if isClosed {
			sort.Ints(comp)
			closedComps = append(closedComps, comp)
		}
	}
	sort.Slice(closedComps, func(a, b int) bool { return closedComps[a][0] < closedComps[b][0] })

	k := len(closedComps)
	cid := make([]int, n)
	for i := range cid {
		cid[i] = -1
	}
	classes := make([]ClassInfo, k)
	for id, comp := range closedComps {
		abs := len(comp) == 1 && c.Absorbing[comp[0]]
		classes[id] = ClassInfo{ID: id, States: comp, Absorbing: abs}
		for _, v := range comp {
			cid[v] = id
		}
	}

	var transient []int
	for s := 0; s < n; s++ {
		if cid[s] == -1 {
			transient = append(transient, s)
		}
	}

	// Absorption probabilities h^C: solve (I - Q) h = r over transient
	// states, with boundary h=1 on C and h=0 on other closed classes.
	h := make([][]*big.Rat, k)
	singularH := false
	for cl := 0; cl < k; cl++ {
		h[cl] = make([]*big.Rat, n)
		inClass := make(map[int]bool)
		for _, v := range closedComps[cl] {
			inClass[v] = true
		}
		for s := 0; s < n; s++ {
			if inClass[s] {
				h[cl][s] = ratOne()
			} else {
				h[cl][s] = ratZero()
			}
		}
		m := len(transient)
		A := make([][]*big.Rat, m)
		b := make([]*big.Rat, m)
		for i, s := range transient {
			A[i] = make([]*big.Rat, m)
			for j := range A[i] {
				A[i][j] = ratZero()
			}
			A[i][i] = ratOne()
			for j, t := range transient {
				if c.P[s][t].Sign() > 0 {
					A[i][j] = new(big.Rat).Sub(A[i][j], c.P[s][t])
				}
			}
			b[i] = ratZero()
			for v := range inClass {
				b[i].Add(b[i], c.P[s][v])
			}
		}
		x, sing := solveLinear(A, b)
		if sing {
			singularH = true
			continue
		}
		for i, s := range transient {
			h[cl][s] = x[i]
		}
	}

	// States from which a non-absorbing closed class is reachable have
	// infinite expected absorption time. Reverse BFS from those classes.
	inBadClass := make([]bool, n)
	for _, cl := range classes {
		if !cl.Absorbing {
			for _, v := range cl.States {
				inBadClass[v] = true
			}
		}
	}
	rev := make([][]int, n)
	for i := 0; i < n; i++ {
		for _, j := range g[i] {
			rev[j] = append(rev[j], i)
		}
	}
	reachBad := make([]bool, n)
	var queue []int
	for s := 0; s < n; s++ {
		if inBadClass[s] {
			reachBad[s] = true
			queue = append(queue, s)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, u := range rev[v] {
			if !reachBad[u] {
				reachBad[u] = true
				queue = append(queue, u)
			}
		}
	}

	// Expected steps t: 0 on absorbing states, Infinity where a
	// non-absorbing closed class is reachable, otherwise solve
	// (I - Q) t = 1 over the remaining transient states.
	t := make([]*big.Rat, n)
	infinite := make([]bool, n)
	for s := 0; s < n; s++ {
		switch {
		case cid[s] >= 0 && classes[cid[s]].Absorbing:
			t[s] = ratZero()
		case cid[s] >= 0 || reachBad[s]:
			infinite[s] = true
		}
	}
	var finite []int
	for s := 0; s < n; s++ {
		if cid[s] == -1 && !reachBad[s] {
			finite = append(finite, s)
		}
	}
	singularT := false
	if len(finite) > 0 {
		fidx := make(map[int]int)
		for i, s := range finite {
			fidx[s] = i
		}
		m := len(finite)
		A := make([][]*big.Rat, m)
		b := make([]*big.Rat, m)
		for i, s := range finite {
			A[i] = make([]*big.Rat, m)
			for j := range A[i] {
				A[i][j] = ratZero()
			}
			A[i][i] = ratOne()
			for j, u := range finite {
				if c.P[s][u].Sign() > 0 {
					A[i][j] = new(big.Rat).Sub(A[i][j], c.P[s][u])
				}
			}
			b[i] = ratOne()
		}
		x, sing := solveLinear(A, b)
		if sing {
			singularT = true
		} else {
			for i, s := range finite {
				t[s] = x[i]
			}
		}
	}

	// Assemble per-state results with reachability evidence.
	states := make([]StateResult, n)
	for s := 0; s < n; s++ {
		sr := StateResult{
			State:         s,
			InClosedClass: cid[s] >= 0,
			Absorption:    make(map[string]string),
		}
		switch {
		case infinite[s]:
			sr.ExpectedSteps = "Infinity"
		case t[s] != nil:
			sr.ExpectedSteps = t[s].RatString()
		default:
			sr.ExpectedSteps = "Undefined"
		}
		for cl := 0; cl < k; cl++ {
			if singularH {
				sr.Absorption[strconv.Itoa(cl)] = "Undefined"
			} else {
				sr.Absorption[strconv.Itoa(cl)] = h[cl][s].RatString()
			}
			target := make(map[int]bool)
			for _, v := range closedComps[cl] {
				target[v] = true
			}
			if path := shortestPathToSet(g, s, target); path != nil {
				sr.Reachable = append(sr.Reachable, ReachEvidence{ClassID: cl, Path: path})
			}
		}
		states[s] = sr
	}

	checks := verify(c, classes, cid, h, t, infinite, reachBad, singularH, singularT)
	return &Analysis{ClosedClasses: classes, States: states, Verification: checks}
}

// verify independently substitutes the computed solutions back into the
// defining equations and checks exact consistency.
func verify(c *Chain, classes []ClassInfo, cid []int, h [][]*big.Rat, t []*big.Rat, infinite, reachBad []bool, singularH, singularT bool) []Check {
	n := c.N
	var checks []Check

	// 1. Every transition row sums to exactly 1.
	ok, detail := true, ""
	for i := 0; i < n; i++ {
		sum := ratZero()
		for j := 0; j < n; j++ {
			sum.Add(sum, c.P[i][j])
		}
		if sum.Cmp(ratOne()) != 0 {
			ok = false
			detail = fmt.Sprintf("row %d sums to %s", i, sum.RatString())
			break
		}
	}
	checks = append(checks, Check{Name: "transition rows sum to 1", OK: ok, Detail: detail})

	// 2. Absorption probabilities satisfy h = P h exactly.
	if singularH {
		checks = append(checks, Check{Name: "absorption equations h = P h", OK: false, Detail: "coefficient matrix singular; no solution reported"})
	} else {
		ok, detail = true, ""
	outer:
		for cl := range classes {
			for s := 0; s < n; s++ {
				rhs := ratZero()
				for j := 0; j < n; j++ {
					rhs.Add(rhs, new(big.Rat).Mul(c.P[s][j], h[cl][j]))
				}
				if h[cl][s].Cmp(rhs) != 0 {
					ok = false
					detail = fmt.Sprintf("class %d state %d: h=%s but P*h=%s", cl, s, h[cl][s].RatString(), rhs.RatString())
					break outer
				}
			}
		}
		checks = append(checks, Check{Name: "absorption equations h = P h", OK: ok, Detail: detail})

		// 3. Boundary values: 1 on own class, 0 on other closed classes.
		ok, detail = true, ""
		for cl, info := range classes {
			for _, v := range info.States {
				if h[cl][v].Cmp(ratOne()) != 0 {
					ok = false
					detail = fmt.Sprintf("state %d in class %d has h=%s, want 1", v, cl, h[cl][v].RatString())
				}
			}
		}
		checks = append(checks, Check{Name: "absorption boundary values", OK: ok, Detail: detail})

		// 4. Probabilities lie in [0,1] and sum to 1 per state.
		ok, detail = true, ""
		for s := 0; s < n; s++ {
			total := ratZero()
			for cl := range classes {
				if h[cl][s].Sign() < 0 || h[cl][s].Cmp(ratOne()) > 0 {
					ok = false
					detail = fmt.Sprintf("state %d class %d probability %s outside [0,1]", s, cl, h[cl][s].RatString())
				}
				total.Add(total, h[cl][s])
			}
			if total.Cmp(ratOne()) != 0 {
				ok = false
				detail = fmt.Sprintf("state %d absorption probabilities sum to %s", s, total.RatString())
			}
		}
		checks = append(checks, Check{Name: "absorption probabilities in [0,1] and sum to 1", OK: ok, Detail: detail})
	}

	// 5. Finite expected steps satisfy t = 1 + P t exactly, and no finite
	// expectation was emitted where a non-absorbing closed class is reachable.
	if singularT {
		checks = append(checks, Check{Name: "expectation equations t = 1 + P t", OK: false, Detail: "coefficient matrix singular; finite expectations withheld"})
	} else {
		ok, detail = true, ""
		for s := 0; s < n; s++ {
			if infinite[s] || t[s] == nil {
				continue
			}
			if cid[s] >= 0 && classes[cid[s]].Absorbing {
				// Boundary condition on absorbing states: t = 0.
				if t[s].Sign() != 0 {
					ok = false
					detail = fmt.Sprintf("absorbing state %d has t=%s, want 0", s, t[s].RatString())
					break
				}
				continue
			}
			rhs := ratOne()
			for j := 0; j < n; j++ {
				if c.P[s][j].Sign() == 0 {
					continue
				}
				if t[j] == nil {
					ok = false
					detail = fmt.Sprintf("finite-expectation state %d has edge to infinite-expectation state %d", s, j)
					break
				}
				rhs.Add(rhs, new(big.Rat).Mul(c.P[s][j], t[j]))
			}
			if !ok {
				break
			}
			if t[s].Cmp(rhs) != 0 {
				ok = false
				detail = fmt.Sprintf("state %d: t=%s but 1+P*t=%s", s, t[s].RatString(), rhs.RatString())
				break
			}
		}
		checks = append(checks, Check{Name: "expectation equations t = 1 + P t", OK: ok, Detail: detail})
	}

	ok, detail = true, ""
	for s := 0; s < n; s++ {
		if reachBad[s] && !infinite[s] {
			ok = false
			detail = fmt.Sprintf("state %d reaches a non-absorbing closed class but was not marked Infinity", s)
			break
		}
	}
	checks = append(checks, Check{Name: "no finite expectation across non-absorbing closed classes", OK: ok, Detail: detail})

	return checks
}
