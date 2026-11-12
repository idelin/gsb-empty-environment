package msglang

import "sort"

type enumerator struct {
	product        *Automaton
	bounds         Bounds
	leafValues     map[string][]*Node          // leaf label -> finite domain nodes
	leafOrder      []string                    // sorted leaf labels
	internal       map[string]int              // internal label -> arity
	internalOrder  []string                    // sorted internal labels
	nullaryOrder   []string                    // leaf labels plus arity-0 internal labels
	symbolsByArity map[int][]string            // arity -> labels with that arity
	childLabels    map[string]map[int][]string // parent label -> child position -> labels
	cache          map[string]map[cacheKey][]*Node
	canReachFinal  map[string]bool
	examined       int
	stopped        bool
}

type cacheKey struct {
	size   int
	height int
}

func newEnumerator(product *Automaton, bounds Bounds) (*enumerator, error) {
	e := &enumerator{
		product:        product,
		bounds:         bounds,
		leafValues:     make(map[string][]*Node),
		internal:       make(map[string]int),
		symbolsByArity: make(map[int][]string),
		cache:          make(map[string]map[cacheKey][]*Node),
	}

	// Finite leaf domains: exact values required by product rules plus
	// deterministic non-empty and empty defaults for each leaf kind.
	byKind := make(map[LeafKind][][]byte)
	for i := range product.Leaves {
		rule := &product.Leaves[i]
		byKind[rule.Kind] = append(byKind[rule.Kind], canonicalValue(rule))
	}
	for kind := range byKind {
		byKind[kind] = append(byKind[kind], defaultLeafValue(kind), []byte{})
		byKind[kind] = dedupValues(byKind[kind])
	}

	for i := range product.Leaves {
		rule := &product.Leaves[i]
		if _, seen := e.leafValues[rule.Label]; seen {
			continue
		}
		nodes := make([]*Node, 0, len(byKind[rule.Kind]))
		for _, value := range byKind[rule.Kind] {
			nodes = append(nodes, NewLeaf(rule.Label, rule.Kind, value))
		}
		e.leafValues[rule.Label] = nodes
		e.leafOrder = append(e.leafOrder, rule.Label)
		e.symbolsByArity[0] = append(e.symbolsByArity[0], rule.Label)
	}

	for i := range product.Rules {
		rule := &product.Rules[i]
		if _, seen := e.internal[rule.Label]; !seen {
			e.internal[rule.Label] = len(rule.Child)
			e.internalOrder = append(e.internalOrder, rule.Label)
			e.symbolsByArity[len(rule.Child)] = append(
				e.symbolsByArity[len(rule.Child)], rule.Label)
		}
	}

	sort.Strings(e.leafOrder)
	sort.Strings(e.internalOrder)
	for arity := range e.symbolsByArity {
		sort.Strings(e.symbolsByArity[arity])
	}
	e.nullaryOrder = append(e.nullaryOrder, e.symbolsByArity[0]...)
	e.canReachFinal = e.computeProductiveLabels()
	e.childLabels = e.computeChildLabels()

	// Seed nullary symbols: typed leaves and arity-0 internal symbols.
	for _, label := range e.nullaryOrder {
		e.cache[label] = make(map[cacheKey][]*Node)
		if nodes, ok := e.leafValues[label]; ok {
			e.cache[label][cacheKey{size: 1, height: 0}] = nodes
			continue
		}
		e.cache[label][cacheKey{size: 1, height: 0}] = []*Node{NewNode(label)}
	}
	return e, nil
}

// computeChildLabels determines, for every internal parent label and each of
// its rule child positions, the concrete symbols whose produced state can
// satisfy some rule at that position. Enumeration is then restricted to these
// symbols, so every generated subtree is reducible instead of merely
// well-ranked.
func (e *enumerator) computeChildLabels() map[string]map[int][]string {
	leafStateLabels := make(map[string][]string)
	for i := range e.product.Leaves {
		rule := &e.product.Leaves[i]
		leafStateLabels[rule.State] = append(leafStateLabels[rule.State], rule.Label)
	}
	internalStateLabels := make(map[string][]string)
	for i := range e.product.Rules {
		rule := &e.product.Rules[i]
		internalStateLabels[rule.State] = append(internalStateLabels[rule.State], rule.Label)
	}
	symbolsForState := func(state string) []string {
		out := append(append([]string(nil), leafStateLabels[state]...), internalStateLabels[state]...)
		sort.Strings(out)
		return out
	}

	out := make(map[string]map[int][]string)
	for parent, arity := range e.internal {
		positions := make(map[int]map[string]bool)
		for i := 0; i < arity; i++ {
			positions[i] = make(map[string]bool)
		}
		for i := range e.product.Rules {
			rule := &e.product.Rules[i]
			if rule.Label != parent || len(rule.Child) != arity {
				continue
			}
			for position, state := range rule.Child {
				for _, label := range symbolsForState(state) {
					positions[position][label] = true
				}
			}
		}
		out[parent] = make(map[int][]string, arity)
		for position, set := range positions {
			labels := make([]string, 0, len(set))
			for label := range set {
				if e.canReachFinal[label] {
					labels = append(labels, label)
				}
			}
			sort.Strings(labels)
			out[parent][position] = labels
		}
	}
	return out
}

// computeProductiveLabels reports labels whose produced state can reach a
// product final state. Enumeration skips every other label, eliminating whole
// unproductive subtrees instead of generating trees membership tests reject.
func (e *enumerator) computeProductiveLabels() map[string]bool {
	canReach := make(map[string]bool)
	for _, state := range e.product.Final {
		canReach[state] = true
	}
	changed := true
	for changed {
		changed = false
		for i := range e.product.Rules {
			rule := &e.product.Rules[i]
			if !canReach[rule.State] {
				continue
			}
			// A reachable rule makes every child state reachable too, since a
			// subtree reducing to that child can be embedded under this rule.
			for _, child := range rule.Child {
				if !canReach[child] {
					canReach[child] = true
					changed = true
				}
			}
		}
	}

	out := make(map[string]bool)
	for i := range e.product.Leaves {
		rule := &e.product.Leaves[i]
		if canReach[rule.State] {
			out[rule.Label] = true
		}
	}
	for i := range e.product.Rules {
		rule := &e.product.Rules[i]
		if canReach[rule.State] {
			out[rule.Label] = true
		}
	}
	return out
}

// trees returns distinct well-ranked trees rooted at label whose node count is
// exactly size and whose height is at most maxHeight.
func (e *enumerator) trees(label string, size, maxHeight int) (trees []*Node, stopped bool, err error) {
	if maxHeight < 0 || size < 1 {
		return nil, false, nil
	}
	if !e.canReachFinal[label] {
		return nil, false, nil
	}
	if e.cache[label] == nil {
		e.cache[label] = make(map[cacheKey][]*Node)
	}
	key := cacheKey{size: size, height: maxHeight}
	if ready, ok := e.cache[label][key]; ok {
		return ready, false, nil
	}

	arity, internal := e.internal[label]
	if !internal {
		// Leaf labels only produce size-1, height-0 trees. The seed cache
		// already holds the finite leaf domain for this label.
		if size != 1 || maxHeight < 0 {
			e.cache[label][key] = nil
			return nil, false, nil
		}
		return e.cache[label][cacheKey{size: 1, height: 0}], false, nil
	}
	if arity == 0 {
		if size != 1 {
			e.cache[label][key] = nil
			return nil, false, nil
		}
		return e.cache[label][cacheKey{size: 1, height: 0}], false, nil
	}
	if size < arity+1 || maxHeight < 1 {
		e.cache[label][key] = nil
		return nil, false, nil
	}

	var result []*Node
	var build func(position, remainingSize, childHeight int, prefix []*Node) bool
	build = func(position, remainingSize, childHeight int, prefix []*Node) bool {
		if position == arity {
			if remainingSize != 0 {
				return false
			}
			tree := cloneNode(NewNode(label, prefix...))
			if !e.charge(1) {
				return true
			}
			result = append(result, tree)
			return false
		}
		remainingPositions := arity - position - 1
		for childSize := 1; childSize <= remainingSize-remainingPositions; childSize++ {
			// Only symbols that can reduce to a state required at this exact
			// position are considered; size-1 subtrees are restricted to the
			// nullary (leaf) members of that position's symbol set.
			candidates := e.positionCandidates(label, position, childSize)
			for _, childLabel := range candidates {
				options, childStopped, childErr := e.trees(childLabel, childSize, childHeight)
				if childErr != nil {
					err = childErr
					return true
				}
				if childStopped {
					return true
				}
				for _, option := range options {
					next := append(append([]*Node(nil), prefix...), option)
					if build(position+1, remainingSize-childSize, childHeight, next) {
						return true
					}
				}
			}
		}
		return false
	}

	stopped = build(0, size-1, maxHeight-1, nil)
	if err != nil {
		return nil, stopped, err
	}
	if stopped {
		return nil, true, nil
	}
	e.cache[label][key] = result
	return result, false, nil
}

func (e *enumerator) charge(nodes int) bool {
	e.examined += nodes
	if e.bounds.NodeBudget > 0 && e.examined > e.bounds.NodeBudget {
		e.stopped = true
		return false
	}
	return true
}

func (e *enumerator) filterProductive(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		if e.canReachFinal[label] {
			out = append(out, label)
		}
	}
	return out
}

// productiveRootLabels returns every productive label (internal and leaf),
// sorted, that may serve as the root of an enumerated tree.
func (e *enumerator) productiveRootLabels() []string {
	all := append(append([]string(nil), e.internalOrder...), e.leafOrder...)
	return e.filterProductive(all)
}

// positionCandidates returns symbols usable at child position of parent. For
// size-1 subtrees only nullary symbols can produce a single node; larger
// subtrees require positive-arity internal symbols.
func (e *enumerator) positionCandidates(parent string, position, size int) []string {
	allowed := e.childLabels[parent][position]
	if size == 1 {
		out := make([]string, 0, len(allowed))
		for _, label := range allowed {
			// A size-1 subtree is either a typed leaf or an arity-0 internal
			// symbol (a nullary constructor such as nil).
			if arity, internal := e.internal[label]; !internal || arity == 0 {
				out = append(out, label)
			}
		}
		return out
	}
	out := make([]string, 0, len(allowed))
	for _, label := range allowed {
		if arity, internal := e.internal[label]; internal && arity > 0 {
			out = append(out, label)
		}
	}
	return out
}

func (e *enumerator) unknown(result *BoundedResult, reason string) *BoundedResult {
	result.Verdict = VerdictUnknown
	result.Reason = reason
	result.NodesExamined = e.examined
	return result
}

func dedupValues(values [][]byte) [][]byte {
	seen := make(map[string][]byte)
	for _, value := range values {
		seen[string(value)] = append([]byte(nil), value...)
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([][]byte, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out
}
