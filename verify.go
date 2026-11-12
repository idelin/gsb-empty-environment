package netsec

import "math/big"

// validateFlip enforces the node acceptance rule: an identified node with
// non-negative rational weights summing to exactly one.
func validateFlip(f Flip) error {
	if f.NodeID == "" {
		return &NodeError{Reason: "missing node identifier"}
	}
	if len(f.Branches) == 0 {
		return &NodeError{NodeID: f.NodeID, Reason: "no branches"}
	}
	sum := new(big.Rat)
	for _, br := range f.Branches {
		if br.Weight == nil {
			return &NodeError{NodeID: f.NodeID, Reason: "nil branch weight"}
		}
		if br.Weight.Sign() < 0 {
			return &NodeError{NodeID: f.NodeID, Reason: "negative branch weight"}
		}
		sum.Add(sum, br.Weight)
	}
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		return &NodeError{NodeID: f.NodeID, Reason: "branch weights do not sum to exactly 1"}
	}
	return nil
}

// Report is the result of cross-checking the merging interpreter against
// exhaustive path enumeration.
type Report struct {
	// Paths is the number of concrete execution paths enumerated.
	Paths int
	// Distribution is the merged exact outcome distribution.
	Distribution *Distribution
	// EnumeratedMass is the sum of all enumerated path probabilities.
	EnumeratedMass *big.Rat
}

// Verify runs both engines and asserts they agree exactly: enumerated path
// masses sum to one, every outcome mass matches the merged distribution, and
// every retained source is a valid enumerated path with matching mass.
func (p *Program) Verify() (*Report, error) {
	dist, err := p.Distribution()
	if err != nil {
		return nil, err
	}
	paths, err := p.Enumerate()
	if err != nil {
		return nil, err
	}

	one := big.NewRat(1, 1)
	enumMass := new(big.Rat)
	enumByOutcome := map[string]*big.Rat{}
	knownPaths := map[string]*big.Rat{}
	for i := range paths {
		path := paths[i]
		enumMass.Add(enumMass, path.Prob)
		key := outcomeKey(path.Outcome)
		if _, ok := enumByOutcome[key]; !ok {
			enumByOutcome[key] = new(big.Rat)
		}
		enumByOutcome[key].Add(enumByOutcome[key], path.Prob)
		knownPaths[pathKey(path.Path)] = new(big.Rat).Set(path.Prob)
	}
	if enumMass.Cmp(one) != 0 {
		return nil, &EvalError{Reason: "enumerated path probabilities do not sum to 1"}
	}
	if len(enumByOutcome) != len(dist.Points) {
		return nil, &EvalError{Reason: "outcome count mismatch between engines"}
	}
	for _, pt := range dist.Points {
		got, ok := enumByOutcome[outcomeKey(pt.Outcome)]
		if !ok || got.Cmp(pt.Prob) != 0 {
			return nil, &EvalError{Reason: "outcome probability mismatch between engines"}
		}
	}

	compiled, err := p.ensureCompiled()
	if err != nil {
		return nil, err
	}
	e := &engine{prog: compiled}
	terminals, err := e.run()
	if err != nil {
		return nil, err
	}
	for _, tn := range terminals {
		sourceMass := new(big.Rat)
		for _, src := range tn.sources {
			sourceMass.Add(sourceMass, src.Mass)
			want, ok := knownPaths[pathKey(src.Path)]
			if !ok {
				return nil, &EvalError{Reason: "retained source is not an enumerated path"}
			}
			if want.Cmp(src.Mass) != 0 {
				return nil, &EvalError{Reason: "retained source mass disagrees with enumerator"}
			}
		}
		if sourceMass.Cmp(tn.mass) != 0 {
			return nil, &EvalError{Reason: "merged mass disagrees with retained sources"}
		}
	}

	return &Report{
		Paths:          len(paths),
		Distribution:   dist,
		EnumeratedMass: enumMass,
	}, nil
}
