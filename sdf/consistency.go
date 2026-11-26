package sdf

import "math/big"

// repVector computes the primitive positive integer repetition vector q of
// the internal topology.
//
// For every internal edge e: Prod[e]*q[src] == Cons[e]*q[dst]. External input
// and output edges impose no balance equation. Each weakly connected
// component is balanced independently and the components are merged by taking
// component scalings so the integer vector is primitive and positive.
func repVector(p *Problem, idx *index) ([]int64, int, bool) {
	n := p.Actors
	comp := connectedComponents(p, n)
	q := make([]int64, n)
	internalEdges := 0
	for _, e := range p.Edges {
		if !e.IsInput() && !e.IsOutput() {
			internalEdges++
		}
	}
	for cid := 0; cid < numComponents(comp); cid++ {
		members := make([]int, 0, n)
		for a := 0; a < n; a++ {
			if comp[a] == cid {
				members = append(members, a)
			}
		}
		scale, _, ok := balanceComponent(p, members)
		if !ok {
			return nil, internalEdges, false
		}
		for _, a := range members {
			q[a] = scale[a]
		}
	}
	for _, v := range q {
		if v <= 0 {
			return nil, internalEdges, false
		}
	}
	for eid, e := range p.Edges {
		if e.IsInput() || e.IsOutput() {
			continue
		}
		if int64(e.Prod)*q[e.Src] != int64(e.Cons)*q[e.Dst] {
			_ = eid
			return nil, internalEdges, false
		}
	}
	return q, internalEdges, true
}

func numComponents(comp []int) int {
	best := -1
	for _, c := range comp {
		if c > best {
			best = c
		}
	}
	return best + 1
}

// connectedComponents labels actors joined by internal edges (in either
// direction), ignoring external source/sink edges.
func connectedComponents(p *Problem, n int) []int {
	adj := make([][]int, n)
	for _, e := range p.Edges {
		if e.IsInput() || e.IsOutput() {
			continue
		}
		adj[e.Src] = append(adj[e.Src], e.Dst)
		adj[e.Dst] = append(adj[e.Dst], e.Src)
	}
	comp := make([]int, n)
	for i := range comp {
		comp[i] = -1
	}
	cid := 0
	for start := 0; start < n; start++ {
		if comp[start] != -1 {
			continue
		}
		stack := []int{start}
		comp[start] = cid
		for len(stack) > 0 {
			a := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, b := range adj[a] {
				if comp[b] == -1 {
					comp[b] = cid
					stack = append(stack, b)
				}
			}
		}
		cid++
	}
	return comp
}

// balanceComponent solves the rate equations on one connected component by
// propagating rational ratios from a seed actor, then converts the result to
// the smallest positive integer vector.
func balanceComponent(p *Problem, members []int) (map[int]int64, int, bool) {
	if len(members) == 1 {
		a := members[0]
		// An isolated actor may have self-loops; those are checked below.
		return map[int]int64{a: 1}, 0, true
	}
	rat := map[int]*big.Rat{}
	seed := members[0]
	rat[seed] = big.NewRat(1, 1)
	rank := 0

	// BFS over internal edges, propagating q[dst] = Prod/Cons * q[src].
	queue := []int{seed}
	for len(queue) > 0 {
		a := queue[0]
		queue = queue[1:]
		for _, e := range p.Edges {
			if e.IsInput() || e.IsOutput() {
				continue
			}
			if e.Src == a || e.Dst == a {
				rank++
			}
			var from, to int
			var ratio *big.Rat
			if e.Src == a {
				from, to = e.Src, e.Dst
				ratio = new(big.Rat).SetFrac(big.NewInt(int64(e.Prod)), big.NewInt(int64(e.Cons)))
			} else if e.Dst == a {
				from, to = e.Dst, e.Src
				ratio = new(big.Rat).SetFrac(big.NewInt(int64(e.Cons)), big.NewInt(int64(e.Prod)))
			} else {
				continue
			}
			expected := new(big.Rat).Mul(rat[from], ratio)
			if existing, ok := rat[to]; ok {
				if existing.Cmp(expected) != 0 {
					return nil, rank, false
				}
				continue
			}
			rat[to] = expected
			queue = append(queue, to)
		}
	}

	out := map[int]int64{}
	vec := make([]*big.Rat, 0, len(members))
	for _, a := range members {
		r, ok := rat[a]
		if !ok {
			return nil, rank, false
		}
		vec = append(vec, r)
	}
	prim := primitive(vec)
	if prim == nil {
		return nil, rank, false
	}
	for i, a := range members {
		out[a] = prim[i]
	}
	return out, rank, true
}

// primitive converts a rational vector to the smallest positive integer vector
// proportional to it. Returns nil if any component is non-positive.
func primitive(x []*big.Rat) []int64 {
	denoms := make([]*big.Int, len(x))
	for i, r := range x {
		if r.Sign() <= 0 {
			return nil
		}
		denoms[i] = r.Denom()
	}
	l := big.NewInt(1)
	for _, d := range denoms {
		l = lcmBig(l, d)
	}
	out := make([]int64, len(x))
	g := new(big.Int)
	for i, r := range x {
		v := new(big.Int).Mul(r.Num(), new(big.Int).Quo(l, r.Denom()))
		if !v.IsInt64() {
			return nil
		}
		out[i] = v.Int64()
		if i == 0 {
			g.Set(v)
		} else {
			g.GCD(nil, nil, g, v)
		}
	}
	for i := range out {
		out[i] /= g.Int64()
	}
	return out
}

func lcmBig(a, b *big.Int) *big.Int {
	g := new(big.Int).GCD(nil, nil, a, b)
	return new(big.Int).Quo(new(big.Int).Mul(a, b), g)
}
