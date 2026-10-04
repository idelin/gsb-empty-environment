package main

// costFn is the cost interface consumed by the DP.
type costFn interface {
	Ins(label string) int
	Del(label string) int
	Ren(old, new string) int
}

const (
	chDel   byte = 0
	chIns   byte = 1
	chMatch byte = 2
)

// forestChoice stores the backtracking choices of one forest-distance
// computation for a keyroot pair.
type forestChoice struct {
	li, lj int
	ch     [][]byte // indexed [x-li+1][y-lj+1]
}

type zhangShasha struct {
	t1, t2  *flatTree
	c       costFn
	td      [][]int // tree distance cache, -1 = unknown
	forests map[[2]int]*forestChoice
}

// treeEditDistance computes the minimum edit cost between t1 and t2 and the
// corresponding node mapping (pairs of postorder ids).
func treeEditDistance(t1, t2 *Tree, c costFn) (int, [][2]int) {
	f1, f2 := flatten(t1), flatten(t2)
	z := &zhangShasha{
		t1: f1, t2: f2, c: c,
		forests: map[[2]int]*forestChoice{},
	}
	z.td = make([][]int, f1.n+1)
	for i := range z.td {
		z.td[i] = make([]int, f2.n+1)
		for j := range z.td[i] {
			z.td[i][j] = -1
		}
	}
	for _, i := range f1.keyroots {
		for _, j := range f2.keyroots {
			z.computeForest(i, j)
		}
	}
	d := z.td[f1.n][f2.n]
	var mapping [][2]int
	z.backtrack(f1.n, f2.n, func(s, t int) { mapping = append(mapping, [2]int{s, t}) })
	return d, mapping
}

// computeForest fills the forest-distance matrix for the keyroot pair (i, j)
// and records per-cell choices for later backtracking. As a side effect it
// sets td[x][y] for every cell where both x and y are topmost roots of the
// current forests (the standard Zhang-Shasha optimization).
func (z *zhangShasha) computeForest(i, j int) {
	li, lj := z.t1.l[i], z.t2.l[j]
	rows, cols := i-li+2, j-lj+2
	fd := make([][]int, rows)
	ch := make([][]byte, rows)
	for r := range fd {
		fd[r] = make([]int, cols)
		ch[r] = make([]byte, cols)
	}
	for x := li; x <= i; x++ {
		fd[x-li+1][0] = fd[x-li][0] + z.c.Del(z.t1.labels[x])
		ch[x-li+1][0] = chDel
	}
	for y := lj; y <= j; y++ {
		fd[0][y-lj+1] = fd[0][y-lj] + z.c.Ins(z.t2.labels[y])
		ch[0][y-lj+1] = chIns
	}
	for x := li; x <= i; x++ {
		for y := lj; y <= j; y++ {
			r, cc := x-li+1, y-lj+1
			best := fd[r-1][cc] + z.c.Del(z.t1.labels[x])
			choice := chDel
			if v := fd[r][cc-1] + z.c.Ins(z.t2.labels[y]); v < best {
				best, choice = v, chIns
			}
			if z.t1.l[x] == li && z.t2.l[y] == lj {
				if v := fd[r-1][cc-1] + z.c.Ren(z.t1.labels[x], z.t2.labels[y]); v < best {
					best, choice = v, chMatch
				}
				z.td[x][y] = best
			} else {
				// subtree x spans l(x)..x, subtree y spans l(y)..y: the
				// prefix forests end at l(x)-1 and l(y)-1
				if v := fd[z.t1.l[x]-li][z.t2.l[y]-lj] + z.td[x][y]; v < best {
					best, choice = v, chMatch
				}
			}
			fd[r][cc] = best
			ch[r][cc] = choice
		}
	}
	z.forests[[2]int{i, j}] = &forestChoice{li: li, lj: lj, ch: ch}
}

// backtrack recovers the mapping for the subtree pair rooted at (x, y).
func (z *zhangShasha) backtrack(x, y int, emit func(s, t int)) {
	fm := z.forests[[2]int{z.t1.kr[x], z.t2.kr[y]}]
	cx, cy := x, y
	for cx >= fm.li || cy >= fm.lj {
		if cx < fm.li {
			cy--
			continue
		}
		if cy < fm.lj {
			cx--
			continue
		}
		switch fm.ch[cx-fm.li+1][cy-fm.lj+1] {
		case chDel:
			cx--
		case chIns:
			cy--
		case chMatch:
			if z.t1.l[cx] == fm.li && z.t2.l[cy] == fm.lj {
				// both topmost roots: the cells were matched directly
				emit(cx, cy)
				cx--
				cy--
			} else {
				// matched as whole subtrees via treeDist[cx][cy]; that
				// subproblem decides its own pairs, and the walk resumes
				// before the two subtrees
				z.backtrack(cx, cy, emit)
				cx = z.t1.l[cx] - 1
				cy = z.t2.l[cy] - 1
			}
		}
	}
}
