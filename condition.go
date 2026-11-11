package netsec

import "fmt"

// Condition is a boolean expression over finite-domain membership atoms.
// Implementations are [In], [And], [Or] and [Not]; conditions are compiled
// once and reused across evaluations.
type Condition interface {
	compile(u *Universe) (node, error)
}

// In tests whether axis dim takes any of the named values.
type In struct {
	Axis   int
	Values []string
}

// And holds when every child condition holds.
type And struct{ All []Condition }

// Or holds when any child condition holds.
type Or struct{ Any []Condition }

// Not holds when its child condition does not.
type Not struct{ Inner Condition }

func (c In) compile(u *Universe) (node, error) {
	if c.Axis < 0 || c.Axis >= u.Dims() {
		return node{}, fmt.Errorf("netsec: In references axis %d, universe has %d", c.Axis, u.Dims())
	}
	if len(c.Values) == 0 {
		return node{}, fmt.Errorf("netsec: In on axis %q lists no values", u.axes[c.Axis].name)
	}
	seen := make(map[int]bool)
	var values []int
	for _, name := range c.Values {
		idx, ok := u.axes[c.Axis].index[name]
		if !ok {
			return node{}, fmt.Errorf("netsec: value %q is not in domain of axis %q", name, u.axes[c.Axis].name)
		}
		if !seen[idx] {
			seen[idx] = true
			values = append(values, idx)
		}
	}
	return node{kind: nodeIn, axis: c.Axis, values: values}, nil
}

func (c And) compile(u *Universe) (node, error) {
	children, err := compileAll(u, c.All)
	if err != nil {
		return node{}, err
	}
	return node{kind: nodeAnd, children: children}, nil
}

func (c Or) compile(u *Universe) (node, error) {
	children, err := compileAll(u, c.Any)
	if err != nil {
		return node{}, err
	}
	return node{kind: nodeOr, children: children}, nil
}

func (c Not) compile(u *Universe) (node, error) {
	if c.Inner == nil {
		return node{}, fmt.Errorf("netsec: Not has no inner condition")
	}
	inner, err := c.Inner.compile(u)
	if err != nil {
		return node{}, err
	}
	return node{kind: nodeNot, children: []node{inner}}, nil
}

func compileAll(u *Universe, conditions []Condition) ([]node, error) {
	if len(conditions) == 0 {
		return nil, fmt.Errorf("netsec: boolean condition lists no children")
	}
	nodes := make([]node, 0, len(conditions))
	for _, child := range conditions {
		if child == nil {
			return nil, fmt.Errorf("netsec: boolean condition contains a nil child")
		}
		compiled, err := child.compile(u)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, compiled)
	}
	return nodes, nil
}

type nodeKind int

const (
	nodeIn nodeKind = iota
	nodeAnd
	nodeOr
	nodeNot
)

// node is a compiled condition. truth holds disjoint rectangles (relative to
// the full universe) on which the condition is true.
type node struct {
	kind     nodeKind
	axis     int
	values   []int
	children []node
	truth    []rect
}

func (n node) holds(point []int) bool {
	switch n.kind {
	case nodeIn:
		return containsInt(n.values, point[n.axis])
	case nodeAnd:
		for _, child := range n.children {
			if !child.holds(point) {
				return false
			}
		}
		return true
	case nodeOr:
		for _, child := range n.children {
			if child.holds(point) {
				return true
			}
		}
		return false
	case nodeNot:
		return !n.children[0].holds(point)
	default:
		panic("netsec: unknown condition node kind")
	}
}

// compileCondition validates and compiles a condition against the universe.
func compileCondition(u *Universe, c Condition) (node, error) {
	if c == nil {
		return node{}, fmt.Errorf("netsec: rule condition is nil")
	}
	compiled, err := c.compile(u)
	if err != nil {
		return node{}, err
	}
	compiled.truth = compiled.truthRects(fullRect(u))
	return compiled, nil
}

// truthRects returns disjoint rectangles within scope where the node is true.
func (n node) truthRects(scope rect) []rect {
	switch n.kind {
	case nodeIn:
		r := scope.clone()
		r.sets[n.axis] = filterSet(r.sets[n.axis], n.values)
		if r.empty() {
			return nil
		}
		return []rect{r}

	case nodeAnd:
		cover := []rect{scope.clone()}
		for _, child := range n.children {
			var next []rect
			for _, piece := range cover {
				for _, hit := range child.truthRects(piece) {
					next = append(next, hit)
				}
			}
			cover = next
		}
		return cover

	case nodeOr:
		var cover []rect
		for _, child := range n.children {
			for _, hit := range child.truthRects(scope) {
				cover = append(cover, subtractMany(hit, cover)...)
			}
		}
		return cover

	case nodeNot:
		return subtractMany(scope, n.children[0].truthRects(scope))

	default:
		panic("netsec: unknown condition node kind")
	}
}

func filterSet(set, allowed []int) []int {
	var out []int
	for _, v := range set {
		if containsInt(allowed, v) {
			out = append(out, v)
		}
	}
	return out
}
