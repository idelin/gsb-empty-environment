package netsec

import "fmt"

// SatResult reports the outcome of one satisfiability query.
type SatResult uint8

const (
	SatUnknown SatResult = iota // budget exhausted before deciding
	SatUnsat
	SatSat
)

// lit is a SAT literal: even numbers are positive polarity, odd negative.
// Variable ids start at 1; literal 0 is reserved.
type lit int32

func mkLit(v int32, neg bool) lit {
	l := lit(v << 1)
	if neg {
		l |= 1
	}
	return l
}

func (l lit) neg() lit { return l ^ 1 }
func (l lit) varID() int32 {
	if l < 0 {
		return int32((-l) >> 1)
	}
	return int32(l >> 1)
}

type satClause struct {
	lits []lit
}

// satSolver is a watched-literal chronological DPLL. It is intentionally
// independent of the interpreter so that cross-checking has no shared failure
// modes. The shared budget bounds both propagation and decision work.
type satSolver struct {
	clauses []*satClause
	watches map[lit][]*satClause

	value    []int8 // 0 unassigned, 1 true, -1 false, indexed by var id
	level    []int32
	trail    []lit
	trailLim []int32

	phase    []int8
	activity []int64

	budget    *int64
	exhausted bool
}

func newSatSolver(numVars int32, budget *int64) *satSolver {
	return &satSolver{
		watches:  map[lit][]*satClause{},
		value:    make([]int8, numVars+1),
		level:    make([]int32, numVars+1),
		phase:    make([]int8, numVars+1),
		activity: make([]int64, numVars+1),
		budget:   budget,
	}
}

func (s *satSolver) useBudget(n int64) bool {
	*s.budget -= n
	if *s.budget <= 0 {
		s.exhausted = true
		return false
	}
	return true
}

func (s *satSolver) addClause(lits []lit) bool {
	if !s.useBudget(1) {
		return false
	}
	if len(lits) == 0 {
		return false
	}
	if len(lits) == 1 {
		if !s.enqueue(lits[0], 0) {
			return false
		}
		return true
	}
	c := &satClause{lits: append([]lit(nil), lits...)}
	s.clauses = append(s.clauses, c)
	s.watches[c.lits[0]] = append(s.watches[c.lits[0]], c)
	s.watches[c.lits[1]] = append(s.watches[c.lits[1]], c)
	return true
}

func (s *satSolver) litValue(l lit) int8 {
	v := l.varID()
	val := s.value[v]
	if val == 0 {
		return 0
	}
	if l&1 == 1 {
		return -val
	}
	return val
}

func (s *satSolver) enqueue(l lit, reasonLevel int32) bool {
	v := l.varID()
	cur := s.value[v]
	if cur != 0 {
		return s.litValue(l) == 1
	}
	if !s.useBudget(1) {
		return false
	}
	val := int8(1)
	if l&1 == 1 {
		val = -1
	}
	s.value[v] = val
	s.level[v] = s.curLevel()
	s.phase[v] = val
	s.trail = append(s.trail, l)
	return true
}

func (s *satSolver) curLevel() int32 {
	return int32(len(s.trailLim))
}

// propagate performs watched-literal BCP and returns a conflicting clause.
func (s *satSolver) propagate() *satClause {
	q := int32(0)
	for q < int32(len(s.trail)) {
		falseLit := s.trail[q].neg()
		q++
		ws := s.watches[falseLit]
		kept := ws[:0]
		for i := 0; i < len(ws); i++ {
			c := ws[i]
			if !s.useBudget(1) {
				return nil
			}
			first := c.lits[0]
			if first == falseLit {
				c.lits[0], c.lits[1] = c.lits[1], c.lits[0]
			}
			// c.lits[1] is now the watched false literal.
			if s.litValue(c.lits[0]) == 1 {
				kept = append(kept, c)
				continue
			}
			found := false
			for k := 2; k < len(c.lits); k++ {
				if s.litValue(c.lits[k]) != -1 {
					c.lits[1], c.lits[k] = c.lits[k], c.lits[1]
					s.watches[c.lits[1]] = append(s.watches[c.lits[1]], c)
					found = true
					break
				}
			}
			if found {
				continue
			}
			kept = append(kept, c)
			if s.litValue(c.lits[0]) == -1 {
				// Conflict: retain remaining watchers unchanged.
				kept = append(kept, ws[i+1:]...)
				s.watches[falseLit] = kept
				return c
			}
			if !s.enqueue(c.lits[0], s.curLevel()) {
				if s.exhausted {
					return nil
				}
				return c
			}
		}
		s.watches[falseLit] = kept
	}
	return nil
}

func (s *satSolver) decide() lit {
	var best int32
	var bestAct int64 = -1
	for v := int32(1); v < int32(len(s.value)); v++ {
		if s.value[v] != 0 {
			continue
		}
		if s.activity[v] > bestAct {
			bestAct = s.activity[v]
			best = v
		}
	}
	if best == 0 {
		return 0
	}
	return mkLit(best, s.phase[best] == -1)
}

func (s *satSolver) cancelUntil(level int32) {
	if s.curLevel() <= level {
		return
	}
	for len(s.trail) > int(s.trailLim[level]) {
		l := s.trail[len(s.trail)-1]
		s.trail = s.trail[:len(s.trail)-1]
		v := l.varID()
		s.value[v] = 0
		s.level[v] = -1
	}
	s.trailLim = s.trailLim[:level]
}

func (s *satSolver) bumpConflict(c *satClause) {
	for _, l := range c.lits {
		s.activity[l.varID()]++
	}
}

// solve runs DPLL under the given unit assumptions.
func (s *satSolver) solve(assumptions []lit) (SatResult, map[int32]bool) {
	for _, l := range assumptions {
		if s.litValue(l) == -1 {
			return SatUnsat, nil
		}
		if s.litValue(l) == 0 {
			if !s.enqueue(l, 0) {
				if s.exhausted {
					return SatUnknown, nil
				}
				return SatUnsat, nil
			}
		}
	}
	if c := s.propagate(); c != nil {
		return SatUnsat, nil
	} else if s.exhausted {
		return SatUnknown, nil
	}

	for {
		c := s.propagate()
		if s.exhausted {
			return SatUnknown, nil
		}
		if c != nil {
			s.bumpConflict(c)
			if s.curLevel() == 0 {
				return SatUnsat, nil
			}
			// Chronological backtrack: flip the most recent decision.
			dec := s.trail[s.trailLim[s.curLevel()-1]]
			s.cancelUntil(s.curLevel() - 1)
			if !s.enqueue(dec.neg(), s.curLevel()) {
				if s.exhausted {
					return SatUnknown, nil
				}
				return SatUnsat, nil
			}
			continue
		}

		next := s.decide()
		if next == 0 {
			model := make(map[int32]bool, len(s.value))
			for v := int32(1); v < int32(len(s.value)); v++ {
				model[v] = s.value[v] == 1
			}
			return SatSat, model
		}
		if !s.useBudget(1) {
			return SatUnknown, nil
		}
		s.trailLim = append(s.trailLim, int32(len(s.trail)))
		if !s.enqueue(next, s.curLevel()) {
			if s.exhausted {
				return SatUnknown, nil
			}
			return SatUnsat, nil
		}
	}
}

// cnfQuery ties one boolean DAG root to the CNF emission variables.
type cnfQuery struct {
	factory *BFactory
	roots   []*BNode
}

// satCheck solves conjunction(query.roots) over several factories, assigning
// Tseitin variables per factory-local node id. Assumed-true roots are encoded
// as unit clauses.
func satCheck(queries []cnfQuery, budget *int64) (SatResult, map[string]bool) {
	// Collect reachable nodes and assign solver variable ids per factory.
	varIDByNode := map[*BNode]int32{}
	var numVars int32
	inputVars := map[*BNode]bool{}
	for _, q := range queries {
		stack := append([]*BNode(nil), q.roots...)
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n == nil {
				continue
			}
			if _, seen := varIDByNode[n]; seen {
				continue
			}
			numVars++
			varIDByNode[n] = numVars
			if n.op == BVar {
				inputVars[n] = true
			}
			if n.op != BConst {
				stack = append(stack, n.x, n.y)
			}
		}
	}

	// For small input domains, exhaustive enumeration over the primary input
	// variables is far more reliable than chronological DPLL on deep
	// multiplier circuits. The DPLL path remains the default for large ones.
	const bruteForceMaxInputs = 24
	if len(inputVars) > 0 && len(inputVars) <= bruteForceMaxInputs &&
		*budget > 0 && int64(uint64(1)<<len(inputVars)) <= *budget {
		return bruteForce(queries, inputVars, budget)
	}

	solver := newSatSolver(numVars, budget)
	nodeLit := func(n *BNode) lit {
		return mkLit(varIDByNode[n], false)
	}
	emit := func(n *BNode) {
		if n.op == BConst {
			return
		}
		if *budget <= 0 {
			return
		}
		v := varIDByNode[n]
		self := mkLit(v, false)
		switch n.op {
		case BVar:
			// free variable, no clauses
		case BNot:
			x := nodeLit(n.x)
			solver.addClause([]lit{self.neg(), x.neg()})
			solver.addClause([]lit{self, x})
		case BAnd:
			x, y := nodeLit(n.x), nodeLit(n.y)
			solver.addClause([]lit{x.neg(), y.neg(), self})
			solver.addClause([]lit{self.neg(), x})
			solver.addClause([]lit{self.neg(), y})
		case BOr:
			x, y := nodeLit(n.x), nodeLit(n.y)
			solver.addClause([]lit{x, y, self.neg()})
			solver.addClause([]lit{self, x.neg()})
			solver.addClause([]lit{self, y.neg()})
		default:
			panic(fmt.Sprintf("netsec: unexpected boolean op %d", n.op))
		}
	}

	// Emission must happen before assumptions so constants can be handled
	// explicitly instead of relying on variable id 1.
	for n := range varIDByNode {
		if *budget <= 0 {
			return SatUnknown, nil
		}
		emit(n)
	}

	var assumptions []lit
	for _, q := range queries {
		for _, r := range q.roots {
			if r.op == BConst {
				if !r.imm {
					return SatUnsat, nil
				}
				continue
			}
			assumptions = append(assumptions, mkLit(varIDByNode[r], false))
		}
	}

	res, model := solver.solve(assumptions)
	if res != SatSat {
		return res, nil
	}
	out := map[string]bool{}
	for _, q := range queries {
		for _, n := range q.factory.nodes {
			if n.op != BVar {
				continue
			}
			if v, ok := varIDByNode[n]; ok {
				out[n.name] = model[v]
			}
		}
	}
	return SatSat, out
}

// bruteForce enumerates assignments to primary input variables and evaluates
// the boolean DAG directly. It is used only when the input space is small
// enough to fully enumerate, which keeps multiplier-heavy 8/16-bit queries
// exact instead of timing out in DPLL.
func bruteForce(queries []cnfQuery, inputVars map[*BNode]bool, budget *int64) (SatResult, map[string]bool) {
	vars := make([]*BNode, 0, len(inputVars))
	for n := range inputVars {
		vars = append(vars, n)
	}
	// Deterministic order for reproducibility.
	for i := 0; i < len(vars); i++ {
		for j := i + 1; j < len(vars); j++ {
			if vars[j].name < vars[i].name {
				vars[i], vars[j] = vars[j], vars[i]
			}
		}
	}
	total := 1
	for range vars {
		total <<= 1
		if total > 1<<24 {
			return SatUnknown, nil
		}
	}
	assignment := make(map[*BNode]bool, len(vars))
	// Collect every reachable node once, then evaluate with timestamped array
	// slots so each enumeration pays no map-clearing cost.
	allNodes := make([]*BNode, 0, 2048)
	seenNode := map[*BNode]bool{}
	maxID := 0
	for _, q := range queries {
		stack := append([]*BNode(nil), q.roots...)
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n == nil || seenNode[n] {
				continue
			}
			seenNode[n] = true
			if n.id > maxID {
				maxID = n.id
			}
			allNodes = append(allNodes, n)
			if n.op != BConst {
				stack = append(stack, n.x, n.y)
			}
		}
	}
	slot := make([]int32, maxID+1)
	val := make([]bool, maxID+1)
	var stamp int32
	var eval func(n *BNode) bool
	eval = func(n *BNode) bool {
		if n.op == BConst {
			return n.imm
		}
		if slot[n.id] == stamp {
			return val[n.id]
		}
		var r bool
		switch n.op {
		case BVar:
			r = assignment[n]
		case BNot:
			r = !eval(n.x)
		case BAnd:
			r = eval(n.x) && eval(n.y)
		case BOr:
			r = eval(n.x) || eval(n.y)
		default:
			panic("netsec: unexpected boolean op in brute force")
		}
		slot[n.id] = stamp
		val[n.id] = r
		return r
	}

	for mask := 0; mask < total; mask++ {
		stamp++
		if *budget <= 0 {
			return SatUnknown, nil
		}
		*budget--
		for i, n := range vars {
			assignment[n] = mask&(1<<i) != 0
		}
		allRoots := true
		for _, q := range queries {
			for _, r := range q.roots {
				if !eval(r) {
					allRoots = false
					break
				}
			}
			if !allRoots {
				break
			}
		}
		if allRoots {
			model := map[string]bool{}
			for _, n := range vars {
				model[n.name] = assignment[n]
			}
			return SatSat, model
		}
	}
	return SatUnsat, nil
}
