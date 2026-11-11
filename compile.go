package wafdecision

// stateKey identifies a partial truth assignment: known lists the leaves that
// have been evaluated, vals gives their results. The pair packs into one
// uint32 because at most MaxLeaves (12) leaves exist.
type stateKey uint32

func makeState(known, vals uint16) stateKey {
	return stateKey(uint32(known)<<16 | uint32(vals))
}

func (s stateKey) parts() (known, vals uint16) {
	return uint16(s >> 16), uint16(s & 0xffff)
}

// sub is the optimal solution for one partial-assignment state.
type sub struct {
	w    uint64  // minimum worst-case total additional cost
	e    float64 // expected additional cost at selectivity midpoints
	node int     // root node identifier of the compiled sub-DAG
}

type builder struct {
	root      node
	specs     []LeafSpec
	byID      map[int]int
	cost      []uint64
	mid       []float64
	n         int
	nodes     []Node
	memo      map[stateKey]*sub
	intern    map[nodeKey]int
	allowNode int
	denyNode  int
}

// nodeKey identifies an equivalent test node so structurally identical states
// can share one DAG node instead of duplicating the subgraph.
type nodeKey struct {
	leaf    int
	onFalse int
	onTrue  int
}

// Compile converts a Boolean rule into an optimal decision DAG.
//
// The compiler searches every evaluation ordering that preserves the
// short-circuit semantics of the rule and returns a graph minimizing the
// worst-case total detector cost; ties are broken deterministically by the
// expected cost at selectivity midpoints. With at most 12 leaves the search
// visits no more than 3^12 = 531,441 partial-assignment states.
func Compile(f Formula, specs []LeafSpec) (*Graph, error) {
	if f == nil {
		return nil, ErrNilFormula
	}
	byID, err := validateSpecs(specs)
	if err != nil {
		return nil, err
	}
	nnfRoot, err := toNNF(f, false)
	if err != nil {
		return nil, err
	}
	nnfRoot = simplify(nnfRoot)
	n := len(specs)
	used := leafMask(nnfRoot)
	if used == 0 {
		return nil, ErrNoLeaves
	}
	if err := checkLeaves(used, byID); err != nil {
		return nil, err
	}

	b := &builder{
		root:   nnfRoot,
		specs:  specs,
		byID:   byID,
		n:      n,
		cost:   make([]uint64, n),
		mid:    make([]float64, n),
		memo:   make(map[stateKey]*sub),
		intern: make(map[nodeKey]int),
	}
	for i, s := range specs {
		b.cost[i] = s.Cost
		b.mid[i] = s.SelMid()
	}
	// Shared terminal nodes keep the DAG compact.
	b.allowNode = b.addNode(Node{Kind: NodeAllow, OnFalse: -1, OnTrue: -1})
	b.denyNode = b.addNode(Node{Kind: NodeDeny, OnFalse: -1, OnTrue: -1})

	best := b.solve(makeState(0, 0))
	g := &Graph{
		nodes:           b.nodes,
		root:            best.node,
		specs:           append([]LeafSpec(nil), specs...),
		costs:           append([]uint64(nil), b.cost...),
		formula:         f,
		WorstCost:       best.w,
		MidExpectedCost: best.e,
		StatesVisited:   len(b.memo),
		LeafCount:       n,
	}
	return g, nil
}

func checkLeaves(used uint16, byID map[int]int) error {
	for id := range byID {
		if id < 0 || id >= MaxLeaves || used&(1<<uint(id)) == 0 {
			// Unused spec entries are harmless but rejected to keep the
			// exhaustive assignment space aligned with the formula.
			return &LeafError{LeafID: id, Reason: "leaf not referenced by formula"}
		}
	}
	// Every referenced identifier must have a spec.
	var missing uint16
	for bit := uint(0); bit < MaxLeaves; bit++ {
		id := int(bit)
		if used&(1<<bit) != 0 {
			if _, ok := byID[id]; !ok {
				missing |= 1 << bit
			}
		}
	}
	if missing != 0 {
		for bit := uint(0); bit < MaxLeaves; bit++ {
			if missing&(1<<bit) != 0 {
				return &LeafError{LeafID: int(bit), Reason: "missing leaf specification"}
			}
		}
	}
	return nil
}

func (b *builder) addNode(nd Node) int {
	if nd.Kind == NodeTest {
		key := nodeKey{leaf: nd.Leaf, onFalse: nd.OnFalse, onTrue: nd.OnTrue}
		if id, ok := b.intern[key]; ok {
			return id
		}
		id := len(b.nodes)
		b.nodes = append(b.nodes, nd)
		b.intern[key] = id
		return id
	}
	id := len(b.nodes)
	b.nodes = append(b.nodes, nd)
	return id
}

// solve implements the minimax recurrence for a partial assignment state:
//
//	W(known, vals) = 0                                 if the formula is decided
//	W(known, vals) = min over frontier leaves i of
//	                   c_i + max(W(state+i=false), W(state+i=true))
//
// The same recurrence is evaluated with expected midpoint costs to produce a
// deterministic tie-breaker among worst-case-optimal choices.
func (b *builder) solve(key stateKey) *sub {
	if cached, ok := b.memo[key]; ok {
		return cached
	}
	known, vals := key.parts()
	if v, ok := decided(b.root, known, vals); ok {
		term := b.allowNode
		if v {
			term = b.denyNode
		}
		s := &sub{w: 0, e: 0, node: term}
		b.memo[key] = s
		return s
	}

	front := b.frontier(known, vals)
	var best *sub
	for _, leafID := range front {
		idx := b.byID[leafID]
		bit := uint16(1) << uint(leafID)
		falseState := makeState(known|bit, vals)
		trueState := makeState(known|bit, vals|bit)
		sf := b.solve(falseState)
		st := b.solve(trueState)

		worst := b.cost[idx] + maxUint64(sf.w, st.w)
		p := b.mid[idx]
		expected := float64(b.cost[idx]) + (1-p)*sf.e + p*st.e

		candidate := &sub{w: worst, e: expected}
		if best == nil || better(candidate, best) {
			nodeID := b.addNode(Node{
				Kind:    NodeTest,
				Leaf:    leafID,
				OnFalse: sf.node,
				OnTrue:  st.node,
			})
			candidate.node = nodeID
			best = candidate
		}
	}
	b.memo[key] = best
	return best
}

func (b *builder) frontier(known, vals uint16) []int {
	return frontier(b.root, known, vals, make([]int, 0, b.n))
}

// better compares worst-case cost first and expected midpoint cost second.
// The frontier is iterated in ascending leaf order and candidates only
// replace on a strict improvement, so equal-cost ties deterministically keep
// the smallest leaf identifier.
func better(a, z *sub) bool {
	if a.w != z.w {
		return a.w < z.w
	}
	return a.e < z.e
}

func maxUint64(a, z uint64) uint64 {
	if a > z {
		return a
	}
	return z
}
