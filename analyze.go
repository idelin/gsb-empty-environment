package netsec

// DefaultBudget is used when Options.Budget is zero.
const DefaultBudget = 1 << 20

// Verdict is the outcome of Analyze.
type Verdict int

const (
	// Safe: no reachable write exceeds its allocation.
	Safe Verdict = iota
	// Vulnerable: a reachable out-of-bounds write exists; Result.Witness
	// holds concrete input bytes that trigger it.
	Vulnerable
	// Unknown: the search budget was exhausted before a conclusion.
	Unknown
)

func (v Verdict) String() string {
	switch v {
	case Safe:
		return "safe"
	case Vulnerable:
		return "vulnerable"
	case Unknown:
		return "unknown"
	}
	return "invalid"
}

// Options configures Analyze.
type Options struct {
	// Budget bounds the total number of concrete evaluations and path
	// exploration steps. Zero selects DefaultBudget. When the budget is
	// exhausted before a verdict is reached, Analyze returns Unknown.
	Budget uint64
}

// Result is the outcome of Analyze.
type Result struct {
	Verdict Verdict
	// Witness is the concrete input triggering an out-of-bounds write;
	// valid only when Verdict is Vulnerable.
	Witness [MaxInputs]byte
	// Site is the Write node that overflows; valid when Vulnerable.
	Site NodeID
	// Paths is the number of terminal paths explored.
	Paths int
	// BudgetUsed is the number of budget units consumed.
	BudgetUsed uint64
}

// Analyze searches the loop-free graph for a reachable write that exceeds
// its allocation. Per path it first tries a sound interval proof, then an
// exhaustive sweep when the referenced input space fits the budget, and
// finally a bounded boundary/random search. It never reports Safe unless
// every path is proven safe.
func Analyze(g *Graph, opts Options) Result {
	budget := opts.Budget
	if budget == 0 {
		budget = DefaultBudget
	}
	a := &analyzer{g: g, ivs: g.intervals(), budget: budget}
	a.res.Verdict = Safe
	a.res.Site = -1
	a.dfs(0, nil, -1, nil)
	a.res.BudgetUsed = a.used
	return a.res
}

type condEdge struct {
	cond  NodeID
	taken bool
}

type pair struct {
	alloc NodeID // -1: no allocation precedes the write
	write NodeID
}

type analyzer struct {
	g      *Graph
	ivs    []iv
	budget uint64
	used   uint64
	res    Result
	done   bool // witness found
}

func (a *analyzer) spend(n uint64) bool {
	if a.budget < n {
		return false
	}
	a.budget -= n
	a.used += n
	return true
}

func (a *analyzer) unknown() {
	if a.res.Verdict != Vulnerable {
		a.res.Verdict = Unknown
	}
}

// dfs enumerates terminal paths. alloc is the most recent Alloc node on
// the path; pairs collects (alloc, write) relations in order.
func (a *analyzer) dfs(blk int, pc []condEdge, alloc NodeID, pairs []pair) {
	if a.done {
		return
	}
	if !a.spend(1) {
		a.unknown()
		return
	}
	b := &a.g.blocks[blk]
	for _, id := range b.nodes {
		switch a.g.nodes[id].op {
		case opAlloc:
			alloc = id
		case opWrite:
			pairs = append(pairs, pair{alloc: alloc, write: id})
		}
	}
	if b.cond < 0 && b.next[0] < 0 {
		a.res.Paths++
		a.checkPath(pc, pairs)
		return
	}
	if b.cond >= 0 {
		a.dfs(b.next[1], append(pc, condEdge{cond: b.cond, taken: true}), alloc, pairs)
		a.dfs(b.next[0], append(pc, condEdge{cond: b.cond, taken: false}), alloc, pairs)
	} else {
		a.dfs(b.next[0], pc, alloc, pairs)
	}
}

func (a *analyzer) checkPath(pc []condEdge, pairs []pair) {
	for _, p := range pairs {
		if a.done {
			return
		}
		if p.alloc < 0 {
			// Write without a preceding allocation: cannot bound it.
			a.unknown()
			continue
		}
		// Sound interval proof: the write can never exceed the
		// smallest possible allocation.
		if a.ivs[p.write].hi <= a.ivs[p.alloc].lo {
			continue
		}
		roots := []NodeID{p.alloc, p.write}
		for _, c := range pc {
			roots = append(roots, c.cond)
		}
		a.solve(a.g.usedBytes(roots), p)
	}
}

// solve searches for inputs (varying only the used bytes) whose concrete
// execution performs an out-of-bounds write. Path conditions and the
// alloc/write expressions depend only on used bytes, so an exhaustive
// sweep over them decides the pair; a bounded sweep may run out of budget
// and degrade the verdict to Unknown.
func (a *analyzer) solve(used []int, p pair) {
	k := len(used)
	if k == 0 {
		if !a.spend(1) {
			a.unknown()
			return
		}
		a.try([MaxInputs]byte{}, p)
		return
	}
	if k < MaxInputs {
		if total := uint64(1) << (8 * uint(k)); total <= a.budget {
			a.exhaustive(used, p)
			return
		}
	}
	a.bounded(used, p)
}

func (a *analyzer) try(in [MaxInputs]byte, p pair) {
	ok, err := Violates(a.g, in)
	if err != nil {
		a.unknown()
		return
	}
	if ok {
		a.res.Verdict = Vulnerable
		a.res.Witness = in
		a.res.Site = p.write
		a.done = true
	}
}

// exhaustive enumerates every combination of the used input bytes in
// ascending order, so the first witness found is the lexicographically
// smallest one. The caller guarantees the budget covers the sweep.
func (a *analyzer) exhaustive(used []int, p pair) {
	var in [MaxInputs]byte
	k := len(used)
	idx := make([]int, k)
	for {
		for i, v := range idx {
			in[used[i]] = byte(v)
		}
		if !a.spend(1) {
			a.unknown()
			return
		}
		a.try(in, p)
		if a.done {
			return
		}
		i := k - 1
		for ; i >= 0; i-- {
			if idx[i] < 0xff {
				idx[i]++
				break
			}
			idx[i] = 0
		}
		if i < 0 {
			return
		}
	}
}

var boundaries = []byte{0x00, 0x01, 0x02, 0x7f, 0x80, 0xfe, 0xff}

// bounded tries boundary-value combinations first (overflows usually live
// at the edges), then deterministic pseudo-random samples, until the
// budget runs out.
func (a *analyzer) bounded(used []int, p pair) {
	var in [MaxInputs]byte
	k := len(used)
	idx := make([]int, k)
	product := true
	for product {
		for i, d := range idx {
			in[used[i]] = boundaries[d]
		}
		if !a.spend(1) {
			a.unknown()
			return
		}
		a.try(in, p)
		if a.done {
			return
		}
		i := k - 1
		for ; i >= 0; i-- {
			if idx[i] < len(boundaries)-1 {
				idx[i]++
				break
			}
			idx[i] = 0
		}
		product = i >= 0
	}
	seed := uint64(0x9e3779b97f4a7c15) * (uint64(p.write) + 1)
	for _, pos := range used {
		seed ^= uint64(pos+1) * 0xbf58476d1ce4e5b9
	}
	rng := splitmix64{s: seed}
	for a.budget > 0 && !a.done {
		for _, pos := range used {
			in[pos] = byte(rng.next())
		}
		a.spend(1)
		a.try(in, p)
	}
	if !a.done {
		a.unknown()
	}
}

// iv is an unsigned interval over-approximation of a node's values.
type iv struct{ lo, hi uint64 }

// intervals computes sound value ranges for every node, independent of
// path conditions. Node IDs are topological by construction.
func (g *Graph) intervals() []iv {
	out := make([]iv, len(g.nodes))
	for id, n := range g.nodes {
		switch n.op {
		case opInput:
			out[id] = iv{0, n.w.max()}
		case opConst:
			v := uint64(n.imm)
			out[id] = iv{v, v}
		case opAdd:
			a, b := out[n.a], out[n.b]
			if a.hi+b.hi <= n.w.max() {
				out[id] = iv{a.lo + b.lo, a.hi + b.hi}
			} else {
				out[id] = iv{0, n.w.max()}
			}
		case opMul:
			a, b := out[n.a], out[n.b]
			if a.hi*b.hi <= n.w.max() {
				out[id] = iv{a.lo * b.lo, a.hi * b.hi}
			} else {
				out[id] = iv{0, n.w.max()}
			}
		case opTrunc:
			tw := Width(n.imm)
			if out[n.a].hi <= tw.max() {
				out[id] = out[n.a]
			} else {
				out[id] = iv{0, tw.max()}
			}
		case opExt:
			out[id] = out[n.a]
		case opCmp:
			out[id] = iv{0, 1}
		case opAlloc, opWrite:
			out[id] = out[n.a]
		}
	}
	return out
}

// usedBytes returns the sorted input byte offsets that the expressions
// rooted at roots depend on.
func (g *Graph) usedBytes(roots []NodeID) []int {
	seen := make([]bool, len(g.nodes))
	var used [MaxInputs]bool
	var walk func(id NodeID)
	walk = func(id NodeID) {
		if id < 0 || seen[id] {
			return
		}
		seen[id] = true
		n := g.nodes[id]
		if n.op == opInput {
			for i := 0; i < n.w.bytes(); i++ {
				used[int(n.imm)+i] = true
			}
		}
		walk(n.a)
		walk(n.b)
	}
	for _, r := range roots {
		walk(r)
	}
	var out []int
	for i, u := range used {
		if u {
			out = append(out, i)
		}
	}
	return out
}

type splitmix64 struct{ s uint64 }

func (r *splitmix64) next() uint64 {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
