package indexvis

import (
	"container/heap"
	"errors"
	"fmt"
	"math/bits"
	"sort"
)

// MaxBagSize is the maximum number of transactions allowed in one bag.
const MaxBagSize = 11

var (
	// ErrTreewidth is returned when no decomposition with bag size <= MaxBagSize
	// could be constructed by the available heuristics.
	ErrTreewidth = errors.New("indexvis: tree decomposition wider than allowed")
	// ErrBagTooLarge is returned for a caller-supplied decomposition that
	// violates the bag-size bound or another structural property.
	ErrBagTooLarge = errors.New("indexvis: invalid tree decomposition")
)

// TreeDecomposition is a valid tree decomposition. Bags are indexed from 0;
// the tree is represented by Parent and Children. Root has Parent -1.
type TreeDecomposition struct {
	Bags     [][]TxID
	Parent   []int
	Children [][]int
	Root     int
}

// Clone returns a deep copy.
func (td *TreeDecomposition) Clone() *TreeDecomposition {
	if td == nil {
		return nil
	}
	out := &TreeDecomposition{
		Bags:     make([][]TxID, len(td.Bags)),
		Parent:   append([]int(nil), td.Parent...),
		Children: make([][]int, len(td.Children)),
		Root:     td.Root,
	}
	for i, bag := range td.Bags {
		out.Bags[i] = append([]TxID(nil), bag...)
	}
	for i, children := range td.Children {
		out.Children[i] = append([]int(nil), children...)
	}
	return out
}

// Depth returns the distance from the root. It assumes a valid decomposition.
func (td *TreeDecomposition) Depth(bag int) int {
	depth := 0
	for cur := bag; cur >= 0 && td.Parent[cur] >= 0; cur = td.Parent[cur] {
		depth++
	}
	return depth
}

// PathToRoot returns bag, its parent, ..., and the root.
func (td *TreeDecomposition) PathToRoot(bag int) []int {
	if bag < 0 || bag >= len(td.Bags) {
		return nil
	}
	var path []int
	for cur := bag; cur >= 0; cur = td.Parent[cur] {
		path = append(path, cur)
		if td.Parent[cur] < 0 {
			break
		}
	}
	return path
}

// Validate checks the structural tree-decomposition properties and the bag-size
// bound. Coverage of specific dependency/version edges is checked by Index.
func (td *TreeDecomposition) Validate() error {
	if td == nil {
		return fmt.Errorf("%w: nil decomposition", ErrBagTooLarge)
	}
	n := len(td.Bags)
	if n == 0 {
		return fmt.Errorf("%w: no bags", ErrBagTooLarge)
	}
	if len(td.Parent) != n || len(td.Children) != n {
		return fmt.Errorf("%w: inconsistent array lengths", ErrBagTooLarge)
	}
	if td.Root < 0 || td.Root >= n {
		return fmt.Errorf("%w: invalid root %d", ErrBagTooLarge, td.Root)
	}
	if td.Parent[td.Root] != -1 {
		return fmt.Errorf("%w: root has a parent", ErrBagTooLarge)
	}

	seen := make([]bool, n)
	order := make([]int, 0, n)
	stack := []int{td.Root}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[v] {
			return fmt.Errorf("%w: cycle or repeated child at bag %d", ErrBagTooLarge, v)
		}
		seen[v] = true
		order = append(order, v)
		for _, ch := range td.Children[v] {
			if ch < 0 || ch >= n {
				return fmt.Errorf("%w: bag %d has invalid child %d", ErrBagTooLarge, v, ch)
			}
			if td.Parent[ch] != v {
				return fmt.Errorf("%w: parent/child mismatch at %d", ErrBagTooLarge, ch)
			}
			stack = append(stack, ch)
		}
	}
	if len(order) != n {
		return fmt.Errorf("%w: decomposition is not a single tree", ErrBagTooLarge)
	}

	depth := make([]int, n)
	for _, v := range order {
		if v == td.Root {
			continue
		}
		if td.Parent[v] < 0 || td.Parent[v] >= n {
			return fmt.Errorf("%w: bag %d has invalid parent", ErrBagTooLarge, v)
		}
		depth[v] = depth[td.Parent[v]] + 1
	}

	bagsOf := make(map[TxID][]int)
	for i, bag := range td.Bags {
		if len(bag) > MaxBagSize {
			return fmt.Errorf("%w: bag %d has %d transactions", ErrBagTooLarge, i, len(bag))
		}
		seenTx := make(map[TxID]struct{}, len(bag))
		for _, tx := range bag {
			if tx == NoTransaction {
				return fmt.Errorf("%w: bag %d contains NoTransaction", ErrBagTooLarge, i)
			}
			if _, dup := seenTx[tx]; dup {
				return fmt.Errorf("%w: bag %d repeats transaction %d", ErrBagTooLarge, i, tx)
			}
			seenTx[tx] = struct{}{}
			bagsOf[tx] = append(bagsOf[tx], i)
		}
	}

	for tx, list := range bagsOf {
		top := list[0]
		for _, b := range list[1:] {
			if depth[b] < depth[top] {
				top = b
			}
		}
		for _, b := range list {
			if b == top {
				continue
			}
			parent := td.Parent[b]
			if parent < 0 || !bagContains(td.Bags[parent], tx) {
				return fmt.Errorf("%w: bags containing transaction %d are disconnected", ErrBagTooLarge, tx)
			}
		}
	}
	return nil
}

func bagContains(bag []TxID, tx TxID) bool {
	for _, v := range bag {
		if v == tx {
			return true
		}
	}
	return false
}

type bitset []uint64

func newBitset(n int) bitset {
	return make(bitset, (n+63)/64)
}

func (b bitset) set(i int) {
	b[i/64] |= uint64(1) << uint(i%64)
}

func (b bitset) clear(i int) {
	b[i/64] &^= uint64(1) << uint(i%64)
}

func (b bitset) has(i int) bool {
	return b[i/64]&(uint64(1)<<uint(i%64)) != 0
}

func (b bitset) count() int {
	n := 0
	for _, w := range b {
		n += bits.OnesCount64(w)
	}
	return n
}

func (b bitset) list() []int {
	out := make([]int, 0, b.count())
	for wi, w := range b {
		for w != 0 {
			tz := bits.TrailingZeros64(w)
			out = append(out, wi*64+tz)
			w &= w - 1
		}
	}
	return out
}

func cloneBitsets(in []bitset) []bitset {
	out := make([]bitset, len(in))
	for i, b := range in {
		out[i] = append(bitset(nil), b...)
	}
	return out
}

func andCount(a, b bitset) int {
	n := 0
	for i := range a {
		n += bits.OnesCount64(a[i] & b[i])
	}
	return n
}

func countFill(neigh bitset, adj []bitset) int {
	d := neigh.count()
	if d < 2 {
		return 0
	}
	missing := 0
	for wi, w := range neigh {
		for w != 0 {
			tz := bits.TrailingZeros64(w)
			u := wi*64 + tz
			common := andCount(adj[u], neigh)
			missing += d - 1 - common
			w &= w - 1
		}
	}
	return missing / 2
}

type orderItem struct {
	fill int
	deg  int
	tie  int
	v    int
	ver  uint64
}

type orderHeap []orderItem

func (h orderHeap) Len() int { return len(h) }
func (h orderHeap) Less(i, j int) bool {
	if h[i].fill != h[j].fill {
		return h[i].fill < h[j].fill
	}
	if h[i].deg != h[j].deg {
		return h[i].deg < h[j].deg
	}
	if h[i].tie != h[j].tie {
		return h[i].tie < h[j].tie
	}
	return h[i].v < h[j].v
}
func (h orderHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *orderHeap) Push(x any)   { *h = append(*h, x.(orderItem)) }
func (h *orderHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func minFillOrder(n int, base []bitset, seed int) []int {
	adj := cloneBitsets(base)
	deg := make([]int, n)
	fill := make([]int, n)
	ver := make([]uint64, n)
	alive := make([]bool, n)
	h := &orderHeap{}
	heap.Init(h)

	for v := 0; v < n; v++ {
		deg[v] = adj[v].count()
		fill[v] = countFill(adj[v], adj)
		alive[v] = true
		heap.Push(h, orderItem{fill: fill[v], deg: deg[v], tie: (v + seed) % n, v: v, ver: ver[v]})
	}

	order := make([]int, 0, n)
	for len(order) < n {
		var it orderItem
		for {
			it = heap.Pop(h).(orderItem)
			v := it.v
			if alive[v] && it.ver == ver[v] && it.fill == fill[v] && it.deg == deg[v] {
				break
			}
		}
		v := it.v
		neigh := adj[v].list()
		alive[v] = false
		for _, u := range neigh {
			adj[u].clear(v)
			deg[u]--
		}
		adj[v] = nil

		for a := 0; a < len(neigh); a++ {
			u := neigh[a]
			for b := a + 1; b < len(neigh); b++ {
				w := neigh[b]
				if adj[u].has(w) {
					continue
				}
				adj[u].set(w)
				adj[w].set(u)
				deg[u]++
				deg[w]++

				// Every common neighbour loses one missing edge in its
				// neighbourhood. Neighbours of v are recomputed below.
				for wi := range adj[u] {
					x := adj[u][wi] & adj[w][wi]
					for x != 0 {
						tz := bits.TrailingZeros64(x)
						z := wi*64 + tz
						fill[z]--
						ver[z]++
						heap.Push(h, orderItem{fill: fill[z], deg: deg[z], tie: (z + seed) % n, v: z, ver: ver[z]})
						x &= x - 1
					}
				}
			}
		}
		for _, u := range neigh {
			fill[u] = countFill(adj[u], adj)
			ver[u]++
			heap.Push(h, orderItem{fill: fill[u], deg: deg[u], tie: (u + seed) % n, v: u, ver: ver[u]})
		}
		order = append(order, v)
	}
	return order
}

func minDegreeOrder(n int, base []bitset, seed int) []int {
	adj := cloneBitsets(base)
	deg := make([]int, n)
	ver := make([]uint64, n)
	alive := make([]bool, n)
	h := &orderHeap{}
	heap.Init(h)
	for v := 0; v < n; v++ {
		deg[v] = adj[v].count()
		alive[v] = true
		heap.Push(h, orderItem{deg: deg[v], tie: (v + seed) % n, v: v, ver: ver[v]})
	}
	order := make([]int, 0, n)
	for len(order) < n {
		var it orderItem
		for {
			it = heap.Pop(h).(orderItem)
			v := it.v
			if alive[v] && it.ver == ver[v] && it.deg == deg[v] {
				break
			}
		}
		v := it.v
		neigh := adj[v].list()
		alive[v] = false
		for _, u := range neigh {
			adj[u].clear(v)
			deg[u]--
		}
		adj[v] = nil
		for a := 0; a < len(neigh); a++ {
			u := neigh[a]
			for b := a + 1; b < len(neigh); b++ {
				w := neigh[b]
				if adj[u].has(w) {
					continue
				}
				adj[u].set(w)
				adj[w].set(u)
				deg[u]++
				deg[w]++
			}
		}
		for _, u := range neigh {
			ver[u]++
			heap.Push(h, orderItem{deg: deg[u], tie: (u + seed) % n, v: u, ver: ver[u]})
		}
		order = append(order, v)
	}
	return order
}

func tdFromOrder(vertices []TxID, base []bitset, order []int) *TreeDecomposition {
	n := len(vertices)
	adj := cloneBitsets(base)
	elimPos := make([]int, n)
	for i, v := range order {
		elimPos[v] = i
	}
	bags := make([][]TxID, n+1) // last bag is the empty super-root
	parent := make([]int, n+1)
	parent[n] = -1

	for i, v := range order {
		neigh := adj[v].list()
		bag := make([]TxID, 0, len(neigh)+1)
		bag = append(bag, vertices[v])
		for _, u := range neigh {
			bag = append(bag, vertices[u])
		}
		sort.Slice(bag, func(a, b int) bool { return bag[a] < bag[b] })
		bags[i] = bag

		if len(neigh) == 0 {
			parent[i] = n
		} else {
			w := neigh[0]
			for _, u := range neigh[1:] {
				if elimPos[u] < elimPos[w] {
					w = u
				}
			}
			parent[i] = elimPos[w]
		}

		for _, u := range neigh {
			adj[u].clear(v)
		}
		adj[v] = nil
		for a := 0; a < len(neigh); a++ {
			u := neigh[a]
			for b := a + 1; b < len(neigh); b++ {
				w := neigh[b]
				if !adj[u].has(w) {
					adj[u].set(w)
					adj[w].set(u)
				}
			}
		}
	}
	bags[n] = nil
	children := make([][]int, n+1)
	for i := 0; i < n; i++ {
		p := parent[i]
		children[p] = append(children[p], i)
	}
	return &TreeDecomposition{Bags: bags, Parent: parent, Children: children, Root: n}
}

func buildTreeDecomposition(vertices []TxID, edges [][2]int) (*TreeDecomposition, error) {
	n := len(vertices)
	if n == 0 {
		return &TreeDecomposition{
			Bags:     [][]TxID{{}},
			Parent:   []int{-1},
			Children: [][]int{{}},
			Root:     0,
		}, nil
	}
	base := make([]bitset, n)
	for i := range base {
		base[i] = newBitset(n)
	}
	for _, e := range edges {
		u, v := e[0], e[1]
		if u == v {
			continue
		}
		base[u].set(v)
		base[v].set(u)
	}

	bestBag := n + 1
	var best *TreeDecomposition
	try := func(order []int) bool {
		td := tdFromOrder(vertices, base, order)
		maxBag := 0
		for _, bag := range td.Bags {
			if len(bag) > maxBag {
				maxBag = len(bag)
			}
		}
		if maxBag < bestBag {
			bestBag = maxBag
			best = td
		}
		return maxBag <= MaxBagSize
	}

	for seed := 0; seed < 3; seed++ {
		if try(minFillOrder(n, base, seed)) {
			return best, nil
		}
	}
	for seed := 0; seed < 3; seed++ {
		if try(minDegreeOrder(n, base, seed)) {
			return best, nil
		}
	}
	return nil, fmt.Errorf("%w: best bag has %d transactions (limit %d)", ErrTreewidth, bestBag, MaxBagSize)
}
