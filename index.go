package indexvis

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Edge is a dependency u -> v: if u commits then v must commit.
type Edge struct {
	From TxID
	To   TxID
}

// Witness is a legal final completion of the relevant transactions together
// with the number of versions visible in this snapshot under that completion.
type Witness struct {
	Final           map[TxID]Status
	VisibleVersions int64
}

// StatusOf returns the final status recorded in the witness.
func (w Witness) StatusOf(id TxID) Status {
	if w.Final == nil {
		return Unknown
	}
	return w.Final[id]
}

// Result contains the exact bounds and one witness for each bound.
type Result struct {
	Min        int64
	Max        int64
	MinWitness Witness
	MaxWitness Witness
}

const (
	dpPosInf = int64(math.MaxInt64 / 4)
	dpNegInf = int64(math.MinInt64 / 4)

	// MaxTransactions is the supported transaction-universe size.
	MaxTransactions = 4096
)

// ErrTooManyTransactions is returned when the transaction universe exceeds
// MaxTransactions.
var ErrTooManyTransactions = errors.New("indexvis: too many transactions")

type bagDP struct {
	bag      []TxID
	pos      map[TxID]int
	weight   []int64
	valid    []bool
	min      []int64
	max      []int64
	children []int
}

type edgeInfo struct {
	child        int
	parent       int
	shared       []TxID
	childToProj  []int32
	parentToProj []int32
	projMin      []int64
	projMax      []int64
}

// Index is a fixed tree decomposition plus the current commit-table state and
// the current page summary.
type Index struct {
	snapshot Snapshot
	deps     []Edge
	summary  *PageSummary
	universe []TxID
	status   map[TxID]Status
	eligible map[TxID]bool

	td        *TreeDecomposition
	depths    []int
	bags      []*bagDP
	bagsOf    map[TxID][]int
	ownerBag  map[TxID]int
	depsByBag [][]Edge
	pairBag   map[Pair]int

	edgeByChild []*edgeInfo
}

// NewIndex constructs a tree decomposition and evaluates the page on it.
func NewIndex(snapshot Snapshot, deps []Edge, page *PageSummary) (*Index, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	if page == nil {
		page = NewPageSummary()
	}
	universe, err := collectUniverse(snapshot, deps, page)
	if err != nil {
		return nil, err
	}
	edges, err := graphEdges(universe, deps, page)
	if err != nil {
		return nil, err
	}
	td, err := buildTreeDecomposition(universe, edges)
	if err != nil {
		return nil, err
	}
	return newIndexWithDecomposition(snapshot, deps, page, td)
}

// NewIndexForPages merges several page summaries before building the index, so
// unknown transaction outcomes are shared across all pages.
func NewIndexForPages(snapshot Snapshot, deps []Edge, pages ...*PageSummary) (*Index, error) {
	merged, err := MergePageSummaries(pages...)
	if err != nil {
		return nil, err
	}
	return NewIndex(snapshot, deps, merged)
}

// NewIndexWithDecomposition evaluates the page on a caller-supplied, fixed tree
// decomposition. The decomposition must satisfy MaxBagSize and cover every
// transaction that occurs in the page or dependencies.
func NewIndexWithDecomposition(snapshot Snapshot, deps []Edge, page *PageSummary, td *TreeDecomposition) (*Index, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	if page == nil {
		page = NewPageSummary()
	}
	if err := td.Validate(); err != nil {
		return nil, err
	}
	return newIndexWithDecomposition(snapshot, deps, page, td.Clone())
}

// Validate rechecks the structural decomposition, bag-size bound, transaction
// coverage, and coverage of every dependency edge and version pair.
func (ix *Index) Validate() error {
	if err := ix.td.Validate(); err != nil {
		return err
	}
	if len(ix.universe) > MaxTransactions {
		return fmt.Errorf("%w: %d > %d", ErrTooManyTransactions, len(ix.universe), MaxTransactions)
	}
	for _, id := range ix.universe {
		if len(ix.bagsOf[id]) == 0 {
			return fmt.Errorf("%w: transaction %d is absent from the decomposition", ErrBagTooLarge, id)
		}
	}
	for _, e := range ix.deps {
		if _, ok := ix.commonBag(e.From, e.To); !ok {
			return fmt.Errorf("%w: dependency %d -> %d is not covered", ErrBagTooLarge, e.From, e.To)
		}
	}
	for _, pc := range ix.summary.Pairs() {
		if _, ok := ix.bagForPair(pc.Pair); !ok {
			return fmt.Errorf("%w: version pair %s is not covered", ErrBagTooLarge, pc.Pair)
		}
	}
	return nil
}

func collectUniverse(snapshot Snapshot, deps []Edge, page *PageSummary) ([]TxID, error) {
	set := make(map[TxID]struct{})
	for id := range snapshot.Known {
		if id == NoTransaction {
			return nil, fmt.Errorf("snapshot: NoTransaction is not a valid transaction")
		}
		set[id] = struct{}{}
	}
	for id := range snapshot.Active {
		if id == NoTransaction {
			return nil, fmt.Errorf("snapshot: NoTransaction is not a valid active transaction")
		}
		set[id] = struct{}{}
	}
	for _, e := range deps {
		if e.From == NoTransaction || e.To == NoTransaction {
			return nil, fmt.Errorf("dependency: NoTransaction is not a valid endpoint")
		}
		set[e.From] = struct{}{}
		set[e.To] = struct{}{}
	}
	if page != nil {
		for _, pc := range page.Pairs() {
			if pc.Pair.Create == NoTransaction {
				return nil, fmt.Errorf("version pair %s has no creator", pc.Pair)
			}
			set[pc.Pair.Create] = struct{}{}
			if pc.Pair.Delete != NoTransaction {
				set[pc.Pair.Delete] = struct{}{}
			}
		}
	}
	out := make([]TxID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func graphEdges(universe []TxID, deps []Edge, page *PageSummary) ([][2]int, error) {
	pos := make(map[TxID]int, len(universe))
	for i, id := range universe {
		pos[id] = i
	}
	seen := make(map[[2]int]struct{})
	add := func(a, b TxID) {
		if a == NoTransaction || b == NoTransaction || a == b {
			return
		}
		i, ok1 := pos[a]
		j, ok2 := pos[b]
		if !ok1 || !ok2 {
			return
		}
		if i > j {
			i, j = j, i
		}
		seen[[2]int{i, j}] = struct{}{}
	}
	for _, e := range deps {
		add(e.From, e.To)
	}
	if page != nil {
		for _, pc := range page.Pairs() {
			if pc.Pair.Delete != NoTransaction {
				add(pc.Pair.Create, pc.Pair.Delete)
			}
		}
	}
	out := make([][2]int, 0, len(seen))
	for e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out, nil
}

func uniqueDeps(deps []Edge) []Edge {
	seen := make(map[Edge]struct{}, len(deps))
	out := make([]Edge, 0, len(deps))
	for _, e := range deps {
		if _, ok := seen[e]; ok {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}
	return out
}

func newIndexWithDecomposition(snapshot Snapshot, deps []Edge, page *PageSummary, td *TreeDecomposition) (*Index, error) {
	snapshot = cloneSnapshot(snapshot)
	universe, err := collectUniverse(snapshot, deps, page)
	if err != nil {
		return nil, err
	}
	if err := td.Validate(); err != nil {
		return nil, err
	}
	if len(universe) > MaxTransactions {
		return nil, fmt.Errorf("%w: %d > %d", ErrTooManyTransactions, len(universe), MaxTransactions)
	}

	ix := &Index{
		snapshot:    snapshot,
		deps:        uniqueDeps(deps),
		summary:     page.Clone(),
		universe:    universe,
		status:      make(map[TxID]Status, len(universe)),
		eligible:    make(map[TxID]bool, len(universe)),
		td:          td,
		depths:      decompositionDepths(td),
		bags:        make([]*bagDP, len(td.Bags)),
		bagsOf:      make(map[TxID][]int),
		ownerBag:    make(map[TxID]int, len(universe)),
		depsByBag:   make([][]Edge, len(td.Bags)),
		pairBag:     make(map[Pair]int),
		edgeByChild: make([]*edgeInfo, len(td.Bags)),
	}
	for _, id := range universe {
		ix.status[id] = snapshot.InitialStatus(id)
		ix.eligible[id] = snapshot.Eligible(id)
	}

	for i, bag := range td.Bags {
		b := &bagDP{
			bag:      append([]TxID(nil), bag...),
			pos:      make(map[TxID]int, len(bag)),
			children: append([]int(nil), td.Children[i]...),
		}
		size := 1 << uint(len(bag))
		b.weight = make([]int64, size)
		b.valid = make([]bool, size)
		b.min = make([]int64, size)
		b.max = make([]int64, size)
		for j, id := range bag {
			b.pos[id] = j
			ix.bagsOf[id] = append(ix.bagsOf[id], i)
		}
		ix.bags[i] = b
	}

	for _, id := range universe {
		if len(ix.bagsOf[id]) == 0 {
			return nil, fmt.Errorf("%w: transaction %d is absent from the decomposition", ErrBagTooLarge, id)
		}
	}

	allTx := make([]TxID, 0, len(ix.bagsOf))
	for id := range ix.bagsOf {
		allTx = append(allTx, id)
	}
	sort.Slice(allTx, func(i, j int) bool { return allTx[i] < allTx[j] })
	for _, id := range allTx {
		if _, ok := ix.status[id]; !ok {
			ix.status[id] = snapshot.InitialStatus(id)
			ix.eligible[id] = snapshot.Eligible(id)
		}
		bags := ix.bagsOf[id]
		best := bags[0]
		bestDepth := ix.depths[best]
		for _, bag := range bags[1:] {
			if d := ix.depths[bag]; d < bestDepth || (d == bestDepth && bag < best) {
				best = bag
				bestDepth = d
			}
		}
		ix.ownerBag[id] = best
	}
	ix.universe = allTx
	if len(ix.universe) > MaxTransactions {
		return nil, fmt.Errorf("%w: %d > %d", ErrTooManyTransactions, len(ix.universe), MaxTransactions)
	}

	for _, e := range ix.deps {
		bag, ok := ix.commonBag(e.From, e.To)
		if !ok {
			return nil, fmt.Errorf("%w: dependency %d -> %d is not covered by a bag", ErrBagTooLarge, e.From, e.To)
		}
		ix.depsByBag[bag] = append(ix.depsByBag[bag], e)
	}

	for _, pc := range ix.summary.Pairs() {
		bag, ok := ix.bagForPair(pc.Pair)
		if !ok {
			return nil, fmt.Errorf("%w: version pair %s is not covered by a bag", ErrBagTooLarge, pc.Pair)
		}
		ix.pairBag[pc.Pair] = bag
		ix.applyPairDelta(bag, pc.Pair, pc.Count)
	}

	for child, parent := range td.Parent {
		if parent < 0 {
			continue
		}
		ix.edgeByChild[child] = makeEdgeInfo(td, child, parent)
	}

	for i := range ix.bags {
		ix.refreshValid(i)
	}

	post := postorder(td)
	for _, bag := range post {
		ix.recomputeBag(bag)
		ix.refreshProjection(bag)
	}
	if !ix.feasible() {
		if ce := ix.findContradiction(); ce != nil {
			return nil, ce
		}
		return nil, errors.New("indexvis: no legal completion")
	}
	return ix, nil
}

func (ix *Index) commonBag(a, b TxID) (int, bool) {
	listA := ix.bagsOf[a]
	listB := ix.bagsOf[b]
	if a == b {
		listB = listA
	}
	if len(listA) == 0 || len(listB) == 0 {
		return 0, false
	}
	best := -1
	bestDepth := 0
	i, j := 0, 0
	for i < len(listA) && j < len(listB) {
		switch {
		case listA[i] < listB[j]:
			i++
		case listA[i] > listB[j]:
			j++
		default:
			x := listA[i]
			d := ix.depths[x]
			if best < 0 || d < bestDepth || (d == bestDepth && x < best) {
				best = x
				bestDepth = d
			}
			i++
			j++
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

func (ix *Index) bagForPair(pair Pair) (int, bool) {
	if pair.Create == NoTransaction {
		return 0, false
	}
	if pair.Delete == NoTransaction || pair.Delete == pair.Create {
		bags := ix.bagsOf[pair.Create]
		if len(bags) == 0 {
			return 0, false
		}
		best := bags[0]
		bestDepth := ix.depths[best]
		for _, b := range bags[1:] {
			if d := ix.depths[b]; d < bestDepth || (d == bestDepth && b < best) {
				best = b
				bestDepth = d
			}
		}
		return best, true
	}
	return ix.commonBag(pair.Create, pair.Delete)
}

func makeEdgeInfo(td *TreeDecomposition, child, parent int) *edgeInfo {
	childBag := td.Bags[child]
	parentBag := td.Bags[parent]
	parentPos := make(map[TxID]int, len(parentBag))
	for i, tx := range parentBag {
		parentPos[tx] = i
	}
	shared := make([]TxID, 0, len(childBag))
	for _, tx := range childBag {
		if _, ok := parentPos[tx]; ok {
			shared = append(shared, tx)
		}
	}
	sort.Slice(shared, func(i, j int) bool { return shared[i] < shared[j] })
	childMask := make([]int, len(shared))
	for i, tx := range shared {
		for p, v := range childBag {
			if v == tx {
				childMask[i] = p
				break
			}
		}
	}
	parentMask := make([]int, len(shared))
	for i, tx := range shared {
		parentMask[i] = parentPos[tx]
	}

	childToProj := make([]int32, 1<<uint(len(childBag)))
	for mask := range childToProj {
		var idx int32
		for j, p := range childMask {
			if mask&(1<<uint(p)) != 0 {
				idx |= 1 << uint(j)
			}
		}
		childToProj[mask] = idx
	}
	parentToProj := make([]int32, 1<<uint(len(parentBag)))
	for mask := range parentToProj {
		var idx int32
		for j, p := range parentMask {
			if mask&(1<<uint(p)) != 0 {
				idx |= 1 << uint(j)
			}
		}
		parentToProj[mask] = idx
	}
	n := 1 << uint(len(shared))
	return &edgeInfo{
		child:        child,
		parent:       parent,
		shared:       shared,
		childToProj:  childToProj,
		parentToProj: parentToProj,
		projMin:      make([]int64, n),
		projMax:      make([]int64, n),
	}
}

func postorder(td *TreeDecomposition) []int {
	order := make([]int, 0, len(td.Bags))
	stack := []int{td.Root}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		order = append(order, v)
		stack = append(stack, td.Children[v]...)
	}
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order
}

func decompositionDepths(td *TreeDecomposition) []int {
	depths := make([]int, len(td.Bags))
	for i := range depths {
		depths[i] = -1
	}
	depths[td.Root] = 0
	stack := []int{td.Root}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, child := range td.Children[v] {
			depths[child] = depths[v] + 1
			stack = append(stack, child)
		}
	}
	return depths
}

func addPos(a, b int64) int64 {
	if a >= dpPosInf || b >= dpPosInf {
		return dpPosInf
	}
	if a > dpPosInf-b {
		return dpPosInf
	}
	return a + b
}

func addNeg(a, b int64) int64 {
	if a <= dpNegInf || b <= dpNegInf {
		return dpNegInf
	}
	if a < dpNegInf-b {
		return dpNegInf
	}
	return a + b
}

func (ix *Index) recomputeBag(i int) int {
	b := ix.bags[i]
	for a := range b.min {
		if b.valid[a] {
			b.min[a] = b.weight[a]
			b.max[a] = b.weight[a]
		} else {
			b.min[a] = dpPosInf
			b.max[a] = dpNegInf
		}
	}
	for _, child := range b.children {
		e := ix.edgeByChild[child]
		for a := range b.min {
			pi := int(e.parentToProj[a])
			b.min[a] = addPos(b.min[a], e.projMin[pi])
			b.max[a] = addNeg(b.max[a], e.projMax[pi])
		}
	}
	return len(b.min)
}

func (ix *Index) refreshProjection(i int) int {
	e := ix.edgeByChild[i]
	if e == nil {
		return 0
	}
	for j := range e.projMin {
		e.projMin[j] = dpPosInf
		e.projMax[j] = dpNegInf
	}
	b := ix.bags[i]
	for a := range b.min {
		pi := int(e.childToProj[a])
		if b.min[a] < e.projMin[pi] {
			e.projMin[pi] = b.min[a]
		}
		if b.max[a] > e.projMax[pi] {
			e.projMax[pi] = b.max[a]
		}
	}
	return len(e.projMin)
}

func (ix *Index) refreshValid(i int) int {
	b := ix.bags[i]
	for mask := range b.valid {
		ok := true
		for p, tx := range b.bag {
			if ix.ownerBag[tx] != i {
				continue
			}
			bit := mask&(1<<uint(p)) != 0
			switch ix.status[tx] {
			case Committed:
				if !bit {
					ok = false
				}
			case Aborted:
				if bit {
					ok = false
				}
			}
			if !ok {
				break
			}
		}
		if ok {
			for _, e := range ix.depsByBag[i] {
				if e.From == e.To {
					continue
				}
				pi, ok1 := b.pos[e.From]
				qi, ok2 := b.pos[e.To]
				if !ok1 || !ok2 {
					ok = false
					break
				}
				if mask&(1<<uint(pi)) != 0 && mask&(1<<uint(qi)) == 0 {
					ok = false
					break
				}
			}
		}
		b.valid[mask] = ok
	}
	return len(b.valid)
}

func (ix *Index) applyPairDelta(bag int, pair Pair, delta int64) int {
	if delta == 0 {
		return 0
	}
	b := ix.bags[bag]
	touched := 0
	for mask := range b.weight {
		if ix.pairVisibleAt(b, mask, pair) {
			b.weight[mask] += delta
			touched++
		}
	}
	return touched
}

func (ix *Index) pairVisibleAt(b *bagDP, mask int, pair Pair) bool {
	ci, ok := b.pos[pair.Create]
	if !ok || !ix.eligible[pair.Create] || mask&(1<<uint(ci)) == 0 {
		return false
	}
	if pair.Delete == NoTransaction {
		return true
	}
	di, ok := b.pos[pair.Delete]
	if !ok {
		return false
	}
	if !ix.eligible[pair.Delete] {
		return true
	}
	return mask&(1<<uint(di)) == 0
}

func (ix *Index) feasible() bool {
	for _, v := range ix.bags[ix.td.Root].min {
		if v < dpPosInf {
			return true
		}
	}
	return false
}

func (ix *Index) globalMin() int64 {
	best := dpPosInf
	for _, v := range ix.bags[ix.td.Root].min {
		if v < best {
			best = v
		}
	}
	return best
}

func (ix *Index) globalMax() int64 {
	best := dpNegInf
	for _, v := range ix.bags[ix.td.Root].max {
		if v > best {
			best = v
		}
	}
	return best
}

// Evaluate returns the exact minimum and maximum visible-version counts and a
// completion witness for each bound.
func (ix *Index) Evaluate() (Result, error) {
	if !ix.feasible() {
		return Result{}, errors.New("indexvis: no legal completion")
	}
	minCount := ix.globalMin()
	maxCount := ix.globalMax()
	minW, err := ix.reconstruct(false, minCount)
	if err != nil {
		return Result{}, err
	}
	maxW, err := ix.reconstruct(true, maxCount)
	if err != nil {
		return Result{}, err
	}
	if minW.VisibleVersions != minCount || maxW.VisibleVersions != maxCount {
		return Result{}, fmt.Errorf("indexvis: witness recount mismatch (min %d/%d, max %d/%d)",
			minW.VisibleVersions, minCount, maxW.VisibleVersions, maxCount)
	}
	return Result{Min: minCount, Max: maxCount, MinWitness: minW, MaxWitness: maxW}, nil
}

// Bounds is a convenience alias for Evaluate.
func (ix *Index) Bounds() (Result, error) {
	return ix.Evaluate()
}

// MinMax is a convenience alias for Evaluate.
func (ix *Index) MinMax() (Result, error) {
	return ix.Evaluate()
}

func (ix *Index) reconstruct(wantMax bool, target int64) (Witness, error) {
	final := make(map[TxID]Status, len(ix.universe))
	root := ix.td.Root
	rootMask := -1
	table := ix.bags[root].min
	if wantMax {
		table = ix.bags[root].max
	}
	for mask, v := range table {
		if v == target {
			rootMask = mask
			break
		}
	}
	if rootMask < 0 {
		return Witness{}, fmt.Errorf("indexvis: cannot reconstruct target %d", target)
	}

	var walk func(bag, mask int) error
	walk = func(bag, mask int) error {
		b := ix.bags[bag]
		for p, tx := range b.bag {
			st := Aborted
			if mask&(1<<uint(p)) != 0 {
				st = Committed
			}
			if prev, ok := final[tx]; ok && prev != st {
				return fmt.Errorf("indexvis: inconsistent reconstruction for transaction %d", tx)
			}
			final[tx] = st
		}
		for _, child := range b.children {
			e := ix.edgeByChild[child]
			pi := int(e.parentToProj[mask])
			childTarget := e.projMin[pi]
			if wantMax {
				childTarget = e.projMax[pi]
			}
			chosen := -1
			cb := ix.bags[child]
			for cm := range cb.min {
				if int(e.childToProj[cm]) != pi {
					continue
				}
				if wantMax {
					if cb.max[cm] == childTarget {
						chosen = cm
						break
					}
				} else if cb.min[cm] == childTarget {
					chosen = cm
					break
				}
			}
			if chosen < 0 {
				return fmt.Errorf("indexvis: cannot reconstruct child bag %d", child)
			}
			if err := walk(child, chosen); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, rootMask); err != nil {
		return Witness{}, err
	}
	for _, tx := range ix.universe {
		if _, ok := final[tx]; ok {
			continue
		}
		st := ix.status[tx]
		if st == Unknown {
			st = Aborted
		}
		final[tx] = st
	}
	count := ix.countVisible(final)
	return Witness{Final: final, VisibleVersions: count}, nil
}

func (ix *Index) countVisible(final map[TxID]Status) int64 {
	var total int64
	if ix.summary == nil {
		return 0
	}
	for pair, n := range ix.summary.counts {
		if n == 0 || !ix.eligible[pair.Create] || final[pair.Create] != Committed {
			continue
		}
		if pair.Delete != NoTransaction && ix.eligible[pair.Delete] && final[pair.Delete] == Committed {
			continue
		}
		total += n
	}
	return total
}

// Replay validates a witness (known states and dependency implications) and
// independently recounts the visible versions from the page summary.
func (ix *Index) Replay(w Witness) (int64, error) {
	for tx, st := range w.Final {
		if tx == NoTransaction {
			return 0, fmt.Errorf("witness: NoTransaction has final status %s", st)
		}
		if st != Committed && st != Aborted {
			return 0, fmt.Errorf("witness: transaction %d has non-final status %s", tx, st)
		}
	}
	for _, tx := range ix.universe {
		st, ok := w.Final[tx]
		if !ok {
			return 0, fmt.Errorf("witness: missing transaction %d", tx)
		}
		if known := ix.status[tx]; known != Unknown && st != known {
			return 0, fmt.Errorf("witness: transaction %d is %s but known %s", tx, st, known)
		}
	}
	for _, e := range ix.deps {
		if w.Final[e.From] == Committed && w.Final[e.To] != Committed {
			return 0, fmt.Errorf("witness: dependency %d -> %d violated", e.From, e.To)
		}
	}
	return ix.countVisible(w.Final), nil
}

// VerifyWitness replays a witness and checks that its recorded row count is the
// recount obtained from the page summary.
func (ix *Index) VerifyWitness(w Witness) error {
	count, err := ix.Replay(w)
	if err != nil {
		return err
	}
	if count != w.VisibleVersions {
		return fmt.Errorf("witness: recorded %d visible versions, recounted %d", w.VisibleVersions, count)
	}
	return nil
}

// Summary returns a copy of the current page summary.
func (ix *Index) Summary() *PageSummary {
	return ix.summary.Clone()
}

// Dependencies returns a copy of the dependency edges.
func (ix *Index) Dependencies() []Edge {
	return append([]Edge(nil), ix.deps...)
}

// Universe returns all transactions represented by the index, in ascending ID
// order.
func (ix *Index) Universe() []TxID {
	return append([]TxID(nil), ix.universe...)
}

// FinalStatus returns the currently fixed final status, or Unknown.
func (ix *Index) FinalStatus(id TxID) Status {
	return ix.status[id]
}

// IsEligible reports whether id can be effective at this snapshot.
func (ix *Index) IsEligible(id TxID) bool {
	return ix.eligible[id]
}

// OwnerBag returns the bag that owns a transaction's domain constraint.
func (ix *Index) OwnerBag(id TxID) (int, bool) {
	bag, ok := ix.ownerBag[id]
	return bag, ok
}

// PairBag returns the bag that owns a version pair, if it is covered.
func (ix *Index) PairBag(pair Pair) (int, bool) {
	if bag, ok := ix.pairBag[pair]; ok {
		return bag, true
	}
	return ix.bagForPair(pair)
}

// DependencyBag returns the bag that enforces a dependency edge.
func (ix *Index) DependencyBag(e Edge) (int, bool) {
	if e.From == NoTransaction || e.To == NoTransaction {
		return 0, false
	}
	return ix.commonBag(e.From, e.To)
}

// Decomposition returns a deep copy of the fixed tree decomposition.
func (ix *Index) Decomposition() *TreeDecomposition {
	return ix.td.Clone()
}

// Snapshot returns the snapshot used to build the index.
func (ix *Index) Snapshot() Snapshot {
	return cloneSnapshot(ix.snapshot)
}
