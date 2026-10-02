package earley

// Disambiguate filters parse trees down to those respecting the given
// operator precedence and left associativity. prec maps an operator
// terminal (e.g. "+", "*") to its precedence level; higher binds tighter.
// Operators are assumed to be binary, appearing as the middle child of a
// three-child node (X -> X op X), and left-associative.
//
// A tree is kept when, at every operator node:
//   - a left operand rooted at an operator has precedence >= the node's,
//   - a right operand rooted at an operator has precedence > the node's.
func Disambiguate(trees []*Node, prec map[string]int) []*Node {
	var kept []*Node
	for _, t := range trees {
		if respectsPrecedence(t, prec) {
			kept = append(kept, t)
		}
	}
	return kept
}

func respectsPrecedence(n *Node, prec map[string]int) bool {
	if op, ok := nodeOp(n, prec); ok {
		left, right := n.Children[0], n.Children[2]
		if lop, ok := nodeOp(left, prec); ok && prec[lop] < prec[op] {
			return false
		}
		if rop, ok := nodeOp(right, prec); ok && prec[rop] <= prec[op] {
			return false
		}
	}
	for _, c := range n.Children {
		if !respectsPrecedence(c, prec) {
			return false
		}
	}
	return true
}

// nodeOp returns the operator of a node shaped like X -> X op X.
func nodeOp(n *Node, prec map[string]int) (string, bool) {
	if len(n.Children) != 3 {
		return "", false
	}
	mid := n.Children[1]
	if mid == nil || len(mid.Children) != 0 {
		return "", false
	}
	_, ok := prec[mid.Sym]
	return mid.Sym, ok
}
