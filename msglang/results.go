package msglang

// SingleResult is the minimum accepted tree of a single automaton.
type SingleResult struct {
	Empty      bool
	Tree       *Node
	NodeCount  int
	Height     int
	Derivation *Derivation
}

// MinimalTree returns the smallest tree accepted by a, or reports emptiness.
// Minimum means fewest nodes; ties are broken by smallest height, then
// deterministic rule order.
func MinimalTree(a *Automaton) (*SingleResult, error) {
	if err := a.ensureCompiled(); err != nil {
		return nil, err
	}
	best, err := minimalTree(a)
	if err != nil {
		return nil, err
	}
	winner := selectFinal(a, best)
	if winner == nil {
		return &SingleResult{Empty: true}, nil
	}
	return &SingleResult{
		Tree:       winner.tr.node,
		NodeCount:  winner.cost,
		Height:     winner.height,
		Derivation: traceToDerivation(a, winner.tr),
	}, nil
}

// MinimalResult is the smallest tree accepted by both phase automata.
type MinimalResult struct {
	Empty        bool
	Tree         *Node
	NodeCount    int
	Height       int
	Product      *Automaton
	ProductTrace *Derivation
	LeftTrace    *Derivation
	RightTrace   *Derivation
}

// MinimalIntersection builds the product automaton and returns the smallest
// tree in the common language, together with one parsing derivation for each
// phase over the same witness tree.
func MinimalIntersection(left, right *Automaton) (*MinimalResult, error) {
	product, err := Intersect(left, right)
	if err != nil {
		return nil, err
	}
	best, err := minimalTree(product)
	if err != nil {
		return nil, err
	}
	winner := selectFinal(product, best)
	if winner == nil {
		return &MinimalResult{Empty: true, Product: product}, nil
	}
	return &MinimalResult{
		Tree:         winner.tr.node,
		NodeCount:    winner.cost,
		Height:       winner.height,
		Product:      product,
		ProductTrace: traceToDerivation(product, winner.tr),
		LeftTrace:    projectTrace(winner.tr, left, true),
		RightTrace:   projectTrace(winner.tr, right, false),
	}, nil
}
