package main

// indexedTree flattens a tree in postorder. Node ids are 1-based postorder
// numbers; id n is always the root.
type indexedTree struct {
	n        int
	root     *Node
	labels   []string
	parent   []int
	kids     [][]int
	lmost    []int // postorder id of the leftmost leaf of each subtree
	pre      []int // preorder number of each postorder id
	keyroots []int
}

func indexTree(root *Node) *indexedTree {
	t := &indexedTree{root: root}
	t.labels = []string{""}
	t.parent = []int{0}
	t.kids = [][]int{nil}
	t.lmost = []int{0}
	idOf := map[*Node]int{}
	var post func(n *Node) int
	post = func(n *Node) int {
		var ks []int
		for _, c := range n.Children {
			ks = append(ks, post(c))
		}
		id := len(t.labels)
		t.labels = append(t.labels, n.Label)
		t.parent = append(t.parent, 0)
		t.kids = append(t.kids, ks)
		for _, k := range ks {
			t.parent[k] = id
		}
		if len(ks) == 0 {
			t.lmost = append(t.lmost, id)
		} else {
			t.lmost = append(t.lmost, t.lmost[ks[0]])
		}
		idOf[n] = id
		return id
	}
	t.n = post(root)

	t.pre = make([]int, t.n+1)
	counter := 0
	var preWalk func(n *Node)
	preWalk = func(n *Node) {
		counter++
		t.pre[idOf[n]] = counter
		for _, c := range n.Children {
			preWalk(c)
		}
	}
	preWalk(root)

	// A keyroot is the topmost node of its leftmost-leaf class, i.e. the
	// last node with that lmost value in postorder. The root is always a
	// keyroot.
	seen := make(map[int]bool)
	for i := t.n; i >= 1; i-- {
		if !seen[t.lmost[i]] {
			seen[t.lmost[i]] = true
			t.keyroots = append(t.keyroots, i)
		}
	}
	for i, j := 0, len(t.keyroots)-1; i < j; i, j = i+1, j-1 {
		t.keyroots[i], t.keyroots[j] = t.keyroots[j], t.keyroots[i]
	}
	return t
}

// zsEngine computes the Zhang-Shasha tree edit distance and backtraces an
// optimal mapping.
type zsEngine struct {
	t1, t2 *indexedTree
	costs  *CostTable
	td     [][]int // tree distance for relevant pairs; -1 = not computed
}

func newEngine(t1, t2 *indexedTree, c *CostTable) *zsEngine {
	td := make([][]int, t1.n+1)
	for i := range td {
		td[i] = make([]int, t2.n+1)
		for j := range td[i] {
			td[i][j] = -1
		}
	}
	return &zsEngine{t1: t1, t2: t2, costs: c, td: td}
}

// forestDist computes the forest distance matrix for the keyroot pair
// (i1, j1) over the ranges [lmost1[i1]-1 .. i1] x [lmost2[j1]-1 .. j1].
// It also stores tree distances for pairs whose leftmost leaves match the
// keyroots'. The matrix is returned together with its index bases so the
// backtracer can walk it.
func (e *zsEngine) forestDist(i1, j1 int) (fd [][]int, base1, base2 int) {
	t1, t2, c := e.t1, e.t2, e.costs
	base1 = t1.lmost[i1] - 1
	base2 = t2.lmost[j1] - 1
	d1 := i1 - base1 + 1
	d2 := j1 - base2 + 1
	fd = make([][]int, d1)
	for i := range fd {
		fd[i] = make([]int, d2)
	}
	for a := 1; a < d1; a++ {
		fd[a][0] = fd[a-1][0] + c.DelCost(t1.labels[base1+a])
	}
	for b := 1; b < d2; b++ {
		fd[0][b] = fd[0][b-1] + c.InsCost(t2.labels[base2+b])
	}
	for a := 1; a < d1; a++ {
		i := base1 + a
		for b := 1; b < d2; b++ {
			j := base2 + b
			best := min(fd[a-1][b]+c.DelCost(t1.labels[i]),
				fd[a][b-1]+c.InsCost(t2.labels[j]))
			if t1.lmost[i] == t1.lmost[i1] && t2.lmost[j] == t2.lmost[j1] {
				best = min(best, fd[a-1][b-1]+c.RenCost(t1.labels[i], t2.labels[j]))
				e.td[i][j] = best
			} else {
				sub := e.td[i][j]
				if sub < 0 {
					panic("zs: tree distance accessed before computation")
				}
				best = min(best, fd[t1.lmost[i]-1-base1][t2.lmost[j]-1-base2]+sub)
			}
			fd[a][b] = best
		}
	}
	return fd, base1, base2
}

// distance fills the dynamic program and returns the edit distance.
func (e *zsEngine) distance() int {
	for _, i := range e.t1.keyroots {
		for _, j := range e.t2.keyroots {
			e.forestDist(i, j)
		}
	}
	return e.td[e.t1.n][e.t2.n]
}

// rawMapping is the outcome of the backtrace: matched node pairs plus the
// unmatched (deleted / inserted) node ids of both trees.
type rawMapping struct {
	pairs [][2]int
	del   []int
	ins   []int
}

func (e *zsEngine) backtrace() *rawMapping {
	m := &rawMapping{}
	e.trace(e.t1.n, e.t2.n, m)
	return m
}

// trace walks the forest distance matrix of the pair (i1, j1) from the
// bottom-right corner back to the origin. Every step strictly decreases the
// remaining subproblem, so the walk terminates even when zero-cost
// operations make many matrix entries equal.
func (e *zsEngine) trace(i1, j1 int, m *rawMapping) {
	t1, t2, c := e.t1, e.t2, e.costs
	fd, base1, base2 := e.forestDist(i1, j1)
	a, b := i1, j1
	for a > base1 || b > base2 {
		ai, bi := a-base1, b-base2
		prevA, prevB := a, b
		switch {
		case b > base2 && fd[ai][bi-1]+c.InsCost(t2.labels[b]) == fd[ai][bi]:
			m.ins = append(m.ins, b)
			b--
		case a > base1 && fd[ai-1][bi]+c.DelCost(t1.labels[a]) == fd[ai][bi]:
			m.del = append(m.del, a)
			a--
		case t1.lmost[a] == t1.lmost[i1] && t2.lmost[b] == t2.lmost[j1]:
			m.pairs = append(m.pairs, [2]int{a, b})
			a--
			b--
		default:
			na, nb := t1.lmost[a]-1, t2.lmost[b]-1
			e.trace(a, b, m)
			a, b = na, nb
		}
		if a >= prevA && b >= prevB {
			panic("zs: backtrace made no progress")
		}
	}
}
