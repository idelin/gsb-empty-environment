package main

// Precedence describes operator precedence and associativity, used to
// select the conventional reading from an ambiguous parse forest.
type Precedence struct {
	Level      map[string]int  // operator terminal -> level; higher binds tighter
	RightAssoc map[string]bool // operator terminal -> right associative
}

// Filter keeps only the trees that respect the precedence and
// associativity rules.
func (p *Precedence) Filter(trees []*Tree) []*Tree {
	var out []*Tree
	for _, t := range trees {
		if p.respects(t) {
			out = append(out, t)
		}
	}
	return out
}

// respects reports whether every operator node in t is properly nested:
// an operand may not be rooted at a lower-precedence operator, and a
// same-precedence operand is only allowed on the associative side.
func (p *Precedence) respects(t *Tree) bool {
	op, ok := p.rootOp(t)
	if ok {
		opIdx := -1
		for i, ch := range t.Children {
			if len(ch.Children) == 0 && ch.Symbol == op {
				opIdx = i
				break
			}
		}
		for i, ch := range t.Children {
			if i == opIdx || len(ch.Children) == 0 {
				continue
			}
			childOp, isOp := p.rootOp(ch)
			if !isOp {
				continue
			}
			if p.Level[childOp] < p.Level[op] {
				return false
			}
			if p.Level[childOp] == p.Level[op] {
				if i < opIdx && p.RightAssoc[op] {
					return false
				}
				if i > opIdx && !p.RightAssoc[op] {
					return false
				}
			}
		}
	}
	for _, ch := range t.Children {
		if !p.respects(ch) {
			return false
		}
	}
	return true
}

// rootOp returns the operator terminal at the root of a subtree,
// following single-child (unit) derivations.
func (p *Precedence) rootOp(t *Tree) (string, bool) {
	for _, ch := range t.Children {
		if len(ch.Children) == 0 {
			if _, ok := p.Level[ch.Symbol]; ok {
				return ch.Symbol, true
			}
		}
	}
	if len(t.Children) == 1 && len(t.Children[0].Children) > 0 {
		return p.rootOp(t.Children[0])
	}
	return "", false
}
