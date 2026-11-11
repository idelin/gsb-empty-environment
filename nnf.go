package wafdecision

// node is an internal negation-normal-form representation of a rule.
// Negations are pushed down to literals so that only lit, andNode and orNode
// remain, which makes short-circuit analysis and memoization straightforward.
type node interface {
	isNode()
}

// lit is a signed leaf literal. Neg flips the detector result.
type lit struct {
	id  int
	neg bool
}

// constNode is a fully determined logical constant.
type constNode struct {
	value bool
}

// andNode is a flat conjunction of NNF children.
type andNode struct {
	children []node
}

// orNode is a flat disjunction of NNF children.
type orNode struct {
	children []node
}

func (*lit) isNode()       {}
func (*constNode) isNode() {}
func (*andNode) isNode()   {}
func (*orNode) isNode()    {}

// toNNF converts a public Formula into NNF. The negated flag carries
// polarity while Not nodes are pushed downward.
func toNNF(f Formula, negated bool) (node, error) {
	if f == nil {
		return nil, ErrNilFormula
	}
	switch t := f.(type) {
	case *Leaf:
		return &lit{id: t.ID, neg: negated}, nil
	case *Not:
		return toNNF(t.Inner, !negated)
	case *And:
		children, err := nnfChildren(t.Children, negated)
		if err != nil {
			return nil, err
		}
		if negated {
			return simplify(&orNode{children: children}), nil
		}
		return simplify(&andNode{children: children}), nil
	case *Or:
		children, err := nnfChildren(t.Children, negated)
		if err != nil {
			return nil, err
		}
		if negated {
			return simplify(&andNode{children: children}), nil
		}
		return simplify(&orNode{children: children}), nil
	default:
		return nil, &UnsupportedFormulaError{Type: typeName(f)}
	}
}

func nnfChildren(forms []Formula, negated bool) ([]node, error) {
	children := make([]node, 0, len(forms))
	for _, c := range forms {
		n, err := toNNF(c, negated)
		if err != nil {
			return nil, err
		}
		children = append(children, n)
	}
	return children, nil
}

// simplify flattens nested same-operator nodes, absorbs logical constants and
// reduces empty connectives to their neutral element.
func simplify(n node) node {
	switch t := n.(type) {
	case *andNode:
		out := make([]node, 0, len(t.children))
		for _, c := range t.children {
			c = simplify(c)
			switch c := c.(type) {
			case *constNode:
				if !c.value {
					return constFalse
				}
			case *andNode:
				out = append(out, c.children...)
			default:
				out = append(out, c)
			}
		}
		if len(out) == 0 {
			return constTrue
		}
		if len(out) == 1 {
			return out[0]
		}
		return &andNode{children: out}
	case *orNode:
		out := make([]node, 0, len(t.children))
		for _, c := range t.children {
			c = simplify(c)
			switch c := c.(type) {
			case *constNode:
				if c.value {
					return constTrue
				}
			case *orNode:
				out = append(out, c.children...)
			default:
				out = append(out, c)
			}
		}
		if len(out) == 0 {
			return constFalse
		}
		if len(out) == 1 {
			return out[0]
		}
		return &orNode{children: out}
	default:
		return n
	}
}

var (
	constTrue  = &constNode{value: true}
	constFalse = &constNode{value: false}
)

// leafMask collects the set of leaf identifiers referenced by a node.
func leafMask(n node) uint16 {
	switch t := n.(type) {
	case *lit:
		return 1 << uint(t.id)
	case *constNode:
		return 0
	case *andNode:
		var mask uint16
		for _, c := range t.children {
			mask |= leafMask(c)
		}
		return mask
	case *orNode:
		var mask uint16
		for _, c := range t.children {
			mask |= leafMask(c)
		}
		return mask
	default:
		return 0
	}
}

// decided reports whether a node's truth value is fully determined by the
// given partial assignment.
func decided(n node, known, vals uint16) (value, ok bool) {
	switch t := n.(type) {
	case *constNode:
		return t.value, true
	case *lit:
		bit := uint16(1) << uint(t.id)
		if known&bit == 0 {
			return false, false
		}
		raw := vals&bit != 0
		if t.neg {
			return !raw, true
		}
		return raw, true
	case *andNode:
		for _, c := range t.children {
			v, ok := decided(c, known, vals)
			if !ok {
				continue
			}
			if !v {
				return false, true
			}
		}
		return true, allDecided(t, known)
	case *orNode:
		for _, c := range t.children {
			v, ok := decided(c, known, vals)
			if !ok {
				continue
			}
			if v {
				return true, true
			}
		}
		return false, allDecided(t, known)
	default:
		return false, false
	}
}

func allDecided(n node, known uint16) bool {
	return leafMask(n)&^known == 0
}

// frontier returns the distinct, ordered leaf identifiers that may be tested
// next while respecting short-circuit semantics: for a conjunction any child
// not yet falsified may be evaluated first, for a disjunction any child not
// yet satisfied may be evaluated first.
func frontier(n node, known, vals uint16, out []int) []int {
	switch t := n.(type) {
	case *lit:
		bit := uint16(1) << uint(t.id)
		if known&bit == 0 {
			out = appendUnique(out, t.id)
		}
	case *andNode:
		for _, c := range t.children {
			if v, ok := decided(c, known, vals); ok && !v {
				continue
			}
			out = frontier(c, known, vals, out)
		}
	case *orNode:
		for _, c := range t.children {
			if v, ok := decided(c, known, vals); ok && v {
				continue
			}
			out = frontier(c, known, vals, out)
		}
	case *constNode:
		// Nothing to test.
	}
	return out
}

func appendUnique(out []int, id int) []int {
	for _, existing := range out {
		if existing == id {
			return out
		}
	}
	return append(out, id)
}
