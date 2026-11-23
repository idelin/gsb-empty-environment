package envguard

import "fmt"

type regexNode interface{ regexNode() }

type charNode struct{ ranges []runeRange }
type concatNode struct{ children []regexNode }
type altNode struct{ children []regexNode }
type repeatNode struct {
	child    regexNode
	min, max int // max == -1 means unbounded
}

func (*charNode) regexNode()   {}
func (*concatNode) regexNode() {}
func (*altNode) regexNode()    {}
func (*repeatNode) regexNode() {}

const runeMax = '\U0010FFFF'

type regexParser struct {
	src []rune
	pos int
}

func (p *regexParser) parseExpr() (regexNode, error) {
	var alts []regexNode
	for {
		child, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		alts = append(alts, child)
		if p.pos >= len(p.src) || p.src[p.pos] != '|' {
			break
		}
		p.pos++
	}
	if len(alts) == 1 {
		return alts[0], nil
	}
	return &altNode{children: alts}, nil
}

func (p *regexParser) parseConcat() (regexNode, error) {
	var children []regexNode
	for p.pos < len(p.src) {
		r := p.src[p.pos]
		if r == '|' || r == ')' {
			break
		}
		atom, err := p.parseRepeat()
		if err != nil {
			return nil, err
		}
		children = append(children, atom)
	}
	if len(children) == 0 {
		return &concatNode{}, nil
	}
	if len(children) == 1 {
		return children[0], nil
	}
	return &concatNode{children: children}, nil
}

func (p *regexParser) parseRepeat() (regexNode, error) {
	atom, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '*':
			p.pos++
			atom = &repeatNode{child: atom, min: 0, max: -1}
		case '+':
			p.pos++
			atom = &repeatNode{child: atom, min: 1, max: -1}
		case '?':
			p.pos++
			atom = &repeatNode{child: atom, min: 0, max: 1}
		default:
			return atom, nil
		}
	}
	return atom, nil
}

func (p *regexParser) parseAtom() (regexNode, error) {
	r := p.src[p.pos]
	switch r {
	case '(':
		p.pos++
		node, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.pos >= len(p.src) || p.src[p.pos] != ')' {
			return nil, fmt.Errorf("envguard: unbalanced '(' in regex")
		}
		p.pos++
		return node, nil
	case '[':
		return p.parseClass()
	case '.':
		p.pos++
		return &charNode{ranges: []runeRange{{0, '\n' - 1}, {'\n' + 1, runeMax}}}, nil
	case '\\':
		return p.parseEscape()
	default:
		p.pos++
		return &charNode{ranges: []runeRange{{r, r}}}, nil
	}
}

func (p *regexParser) parseEscape() (regexNode, error) {
	p.pos++
	if p.pos >= len(p.src) {
		return nil, fmt.Errorf("envguard: dangling escape in regex")
	}
	r := p.src[p.pos]
	p.pos++
	switch r {
	case 'd':
		return &charNode{ranges: []runeRange{{'0', '9'}}}, nil
	case 'D':
		return &charNode{ranges: negate([]runeRange{{'0', '9'}})}, nil
	case 'w':
		return &charNode{ranges: []runeRange{{'0', '9'}, {'A', 'Z'}, {'a', 'z'}, {'_', '_'}}}, nil
	case 'W':
		return &charNode{ranges: negate([]runeRange{{'0', '9'}, {'A', 'Z'}, {'a', 'z'}, {'_', '_'}})}, nil
	case 's':
		return &charNode{ranges: []runeRange{{'\t', '\r'}, {' ', ' '}}}, nil
	case 'S':
		return &charNode{ranges: negate([]runeRange{{'\t', '\r'}, {' ', ' '}})}, nil
	case 'n':
		r = '\n'
	case 't':
		r = '\t'
	case 'r':
		r = '\r'
	}
	return &charNode{ranges: []runeRange{{r, r}}}, nil
}

func (p *regexParser) parseClass() (regexNode, error) {
	p.pos++ // '['
	negated := false
	if p.pos < len(p.src) && p.src[p.pos] == '^' {
		negated = true
		p.pos++
	}
	var ranges []runeRange
	for p.pos < len(p.src) && p.src[p.pos] != ']' {
		lo, err := p.classRune()
		if err != nil {
			return nil, err
		}
		hi := lo
		if p.pos+1 < len(p.src) && p.src[p.pos] == '-' && p.src[p.pos+1] != ']' {
			p.pos++ // '-'
			hi, err = p.classRune()
			if err != nil {
				return nil, err
			}
		}
		ranges = append(ranges, runeRange{lo, hi})
	}
	if p.pos >= len(p.src) {
		return nil, fmt.Errorf("envguard: unbalanced '[' in regex")
	}
	p.pos++ // ']'
	if negated {
		ranges = negate(ranges)
	}
	return &charNode{ranges: ranges}, nil
}

func (p *regexParser) classRune() (rune, error) {
	if p.pos >= len(p.src) {
		return 0, fmt.Errorf("envguard: malformed character class")
	}
	r := p.src[p.pos]
	p.pos++
	if r == '\\' {
		if p.pos >= len(p.src) {
			return 0, fmt.Errorf("envguard: dangling escape in class")
		}
		esc := p.src[p.pos]
		p.pos++
		switch esc {
		case 'n':
			return '\n', nil
		case 't':
			return '\t', nil
		case 'r':
			return '\r', nil
		case 'd':
			return 0, fmt.Errorf("envguard: class shorthand not supported inside ranges")
		default:
			return esc, nil
		}
	}
	return r, nil
}

func negate(ranges []runeRange) []runeRange {
	// Ranges coming from the parser never overlap; normalize anyway.
	ranges = mergeRanges(ranges)
	var out []runeRange
	cur := rune(0)
	for _, rg := range ranges {
		if rg.lo > cur {
			out = append(out, runeRange{cur, rg.lo - 1})
		}
		if rg.hi < runeMax {
			cur = rg.hi + 1
		} else {
			cur = runeMax + 1
		}
	}
	if cur <= runeMax {
		out = append(out, runeRange{cur, runeMax})
	}
	return out
}

func mergeRanges(ranges []runeRange) []runeRange {
	if len(ranges) <= 1 {
		return ranges
	}
	sorted := make([]runeRange, len(ranges))
	copy(sorted, ranges)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1].lo > sorted[j].lo; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	out := sorted[:1]
	for _, rg := range sorted[1:] {
		last := &out[len(out)-1]
		if rg.lo <= last.hi+1 {
			if rg.hi > last.hi {
				last.hi = rg.hi
			}
			continue
		}
		out = append(out, rg)
	}
	return out
}
