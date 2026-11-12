package netsec

import (
	"fmt"
	"unicode"
)

// SyntaxError describes a pattern that is outside the supported regular
// expression subset.
type SyntaxError struct {
	Offset  int
	Message string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("netsec: invalid pattern at rune %d: %s", e.Offset, e.Message)
}

type runeRange struct {
	lo rune
	hi rune
}

type classTerm struct {
	negate bool
	ranges []runeRange
}

type charClass struct {
	negate bool
	terms  []classTerm
}

func (c *charClass) matches(r rune) bool {
	matched := false
	for _, term := range c.terms {
		inRanges := false
		for _, rg := range term.ranges {
			if r >= rg.lo && r <= rg.hi {
				inRanges = true
				break
			}
		}
		if term.negate {
			inRanges = !inRanges
		}
		if inRanges {
			matched = true
			break
		}
	}
	if c.negate {
		return !matched
	}
	return matched
}

func literalClass(r rune) *charClass {
	return &charClass{terms: []classTerm{{ranges: []runeRange{{lo: r, hi: r}}}}}
}

type nodeKind uint8

const (
	nodeEmpty nodeKind = iota
	nodeLiteral
	nodeClass
	nodeConcat
	nodeUnion
	nodeStar
	nodePlus
	nodeOptional
	nodeAnchorStart
	nodeAnchorEnd
)

type regexNode struct {
	kind        nodeKind
	left, right *regexNode
	child       *regexNode
	class       *charClass
}

type parser struct {
	pattern []rune
	pos     int
}

func parsePattern(pattern string) (*regexNode, error) {
	p := &parser{pattern: []rune(pattern)}
	root, err := p.parseAlternation()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.pattern) {
		return nil, p.errorf(p.pos, "unmatched ')'")
	}
	return root, nil
}

func (p *parser) errorf(offset int, format string, args ...any) error {
	return &SyntaxError{Offset: offset, Message: fmt.Sprintf(format, args...)}
}

func (p *parser) peek() (rune, bool) {
	if p.pos >= len(p.pattern) {
		return 0, false
	}
	return p.pattern[p.pos], true
}

func (p *parser) parseAlternation() (*regexNode, error) {
	left, err := p.parseConcatenation()
	if err != nil {
		return nil, err
	}
	for {
		r, ok := p.peek()
		if !ok || r != '|' {
			break
		}
		p.pos++
		right, err := p.parseConcatenation()
		if err != nil {
			return nil, err
		}
		left = &regexNode{kind: nodeUnion, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseConcatenation() (*regexNode, error) {
	var parts []*regexNode
	for {
		r, ok := p.peek()
		if !ok || r == '|' || r == ')' {
			break
		}

		atom, err := p.parseAtom()
		if err != nil {
			return nil, err
		}

		for {
			r, ok = p.peek()
			if !ok {
				break
			}

			var kind nodeKind
			switch r {
			case '*':
				kind = nodeStar
			case '+':
				kind = nodePlus
			case '?':
				kind = nodeOptional
			default:
				kind = 0
			}
			if kind == 0 {
				break
			}
			if atom.kind == nodeAnchorStart || atom.kind == nodeAnchorEnd {
				return nil, p.errorf(p.pos, "quantifier cannot be applied to an anchor")
			}
			quantifierOffset := p.pos
			p.pos++
			atom = &regexNode{kind: kind, child: atom}

			if next, ok := p.peek(); ok && (next == '*' || next == '+' || next == '?') {
				return nil, p.errorf(quantifierOffset, "stacked quantifiers are not supported")
			}
		}

		parts = append(parts, atom)
	}

	switch len(parts) {
	case 0:
		return &regexNode{kind: nodeEmpty}, nil
	case 1:
		return parts[0], nil
	default:
		root := parts[0]
		for _, part := range parts[1:] {
			root = &regexNode{kind: nodeConcat, left: root, right: part}
		}
		return root, nil
	}
}

func (p *parser) parseAtom() (*regexNode, error) {
	r, _ := p.peek()
	switch r {
	case '\\':
		esc, err := p.parseEscape(false)
		if err != nil {
			return nil, err
		}
		if esc.class != nil {
			return &regexNode{kind: nodeClass, class: esc.class}, nil
		}
		return &regexNode{kind: nodeLiteral, class: literalClass(esc.literal)}, nil
	case '[':
		class, err := p.parseClass()
		if err != nil {
			return nil, err
		}
		return &regexNode{kind: nodeClass, class: class}, nil
	case '(':
		openOffset := p.pos
		p.pos++
		if next, ok := p.peek(); ok && next == '?' {
			return nil, p.errorf(openOffset, "lookaround and non-capturing group syntax is not supported")
		}
		inner, err := p.parseAlternation()
		if err != nil {
			return nil, err
		}
		if next, ok := p.peek(); !ok || next != ')' {
			return nil, p.errorf(openOffset, "unclosed group")
		}
		p.pos++
		return inner, nil
	case '^':
		p.pos++
		return &regexNode{kind: nodeAnchorStart}, nil
	case '$':
		p.pos++
		return &regexNode{kind: nodeAnchorEnd}, nil
	case '.':
		p.pos++
		// Dot has the usual "any rune except newline" meaning.
		return &regexNode{
			kind: nodeClass,
			class: &charClass{
				negate: true,
				terms:  []classTerm{{ranges: []runeRange{{lo: '\n', hi: '\n'}}}},
			},
		}, nil
	case '*', '+', '?':
		return nil, p.errorf(p.pos, "quantifier has no operand")
	case '{':
		return nil, p.errorf(p.pos, "repetition counts are outside the supported subset")
	default:
		p.pos++
		return &regexNode{kind: nodeLiteral, class: literalClass(r)}, nil
	}
}

type escapedValue struct {
	literal rune
	class   *charClass
}

func (p *parser) parseEscape(inClass bool) (escapedValue, error) {
	offset := p.pos
	p.pos++
	if p.pos >= len(p.pattern) {
		return escapedValue{}, p.errorf(offset, "dangling escape")
	}

	r := p.pattern[p.pos]
	p.pos++
	switch r {
	case '1', '2', '3', '4', '5', '6', '7', '8', '9', '0':
		return escapedValue{}, p.errorf(offset, "backreferences and numeric escapes are not supported")
	case 'k':
		return escapedValue{}, p.errorf(offset, "named backreferences are not supported")
	case 'd':
		return escapedValue{class: &charClass{terms: []classTerm{{ranges: asciiDigitRanges()}}}}, nil
	case 'D':
		return escapedValue{class: &charClass{terms: []classTerm{{negate: true, ranges: asciiDigitRanges()}}}}, nil
	case 's':
		return escapedValue{class: &charClass{terms: []classTerm{{ranges: asciiSpaceRanges()}}}}, nil
	case 'S':
		return escapedValue{class: &charClass{terms: []classTerm{{negate: true, ranges: asciiSpaceRanges()}}}}, nil
	case 'w':
		return escapedValue{class: &charClass{terms: []classTerm{{ranges: asciiWordRanges()}}}}, nil
	case 'W':
		return escapedValue{class: &charClass{terms: []classTerm{{negate: true, ranges: asciiWordRanges()}}}}, nil
	case 'n':
		return escapedValue{literal: '\n'}, nil
	case 'r':
		return escapedValue{literal: '\r'}, nil
	case 't':
		return escapedValue{literal: '\t'}, nil
	case 'f':
		return escapedValue{literal: '\f'}, nil
	case 'v':
		return escapedValue{literal: '\v'}, nil
	case 'a':
		return escapedValue{literal: '\a'}, nil
	case 'x':
		return p.parseHexEscape(offset)
	default:
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return escapedValue{}, p.errorf(offset, "unsupported escape \\%c", r)
		}
		// Escaped punctuation always denotes the punctuation itself.
		return escapedValue{literal: r}, nil
	}
}

func (p *parser) parseHexEscape(offset int) (escapedValue, error) {
	if p.pos >= len(p.pattern) {
		return escapedValue{}, p.errorf(offset, "incomplete hexadecimal escape")
	}
	if p.pattern[p.pos] == '{' {
		p.pos++
		start := p.pos
		var value rune
		for p.pos < len(p.pattern) && p.pattern[p.pos] != '}' {
			digit, ok := hexValue(p.pattern[p.pos])
			if !ok {
				return escapedValue{}, p.errorf(p.pos, "invalid hexadecimal escape")
			}
			if value > 0x10ffff/16 {
				return escapedValue{}, p.errorf(start, "hexadecimal escape is out of range")
			}
			value = value*16 + rune(digit)
			p.pos++
		}
		if p.pos >= len(p.pattern) || p.pattern[p.pos] != '}' || p.pos == start {
			return escapedValue{}, p.errorf(offset, "incomplete hexadecimal escape")
		}
		p.pos++
		if value > 0x10ffff || value >= 0xd800 && value <= 0xdfff {
			return escapedValue{}, p.errorf(start, "hexadecimal escape is not a valid rune")
		}
		return escapedValue{literal: value}, nil
	}

	if p.pos+2 > len(p.pattern) {
		return escapedValue{}, p.errorf(offset, "hexadecimal escape requires two digits")
	}
	var value rune
	for range 2 {
		digit, ok := hexValue(p.pattern[p.pos])
		if !ok {
			return escapedValue{}, p.errorf(p.pos, "invalid hexadecimal escape")
		}
		value = value*16 + rune(digit)
		p.pos++
	}
	return escapedValue{literal: value}, nil
}

func hexValue(r rune) (int, bool) {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0'), true
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10, true
	case r >= 'A' && r <= 'F':
		return int(r-'A') + 10, true
	default:
		return 0, false
	}
}

func (p *parser) parseClass() (*charClass, error) {
	openOffset := p.pos
	p.pos++

	class := &charClass{}
	if r, ok := p.peek(); ok && r == '^' {
		class.negate = true
		p.pos++
	}

	first := true
	for {
		if p.pos >= len(p.pattern) {
			return nil, p.errorf(openOffset, "unclosed character class")
		}
		if p.pattern[p.pos] == ']' && !first {
			p.pos++
			return class, nil
		}

		term, err := p.parseClassAtom()
		if err != nil {
			return nil, err
		}
		class.terms = append(class.terms, term)
		first = false
	}
}

func (p *parser) parseClassAtom() (classTerm, error) {
	if p.pattern[p.pos] == '\\' {
		esc, err := p.parseEscape(true)
		if err != nil {
			return classTerm{}, err
		}
		if esc.class != nil {
			if len(esc.class.terms) != 1 || esc.class.negate {
				return classTerm{}, p.errorf(p.pos, "invalid character class escape")
			}
			return esc.class.terms[0], nil
		}
		return p.maybeClassRange(esc.literal)
	}

	if p.pattern[p.pos] == '[' && p.pos+1 < len(p.pattern) && p.pattern[p.pos+1] == ':' {
		return classTerm{}, p.errorf(p.pos, "POSIX character classes are not supported")
	}

	r := p.pattern[p.pos]
	p.pos++
	return p.maybeClassRange(r)
}

func (p *parser) maybeClassRange(lo rune) (classTerm, error) {
	if p.pos+1 < len(p.pattern) && p.pattern[p.pos] == '-' && p.pattern[p.pos+1] != ']' {
		dashOffset := p.pos
		p.pos++

		var hi rune
		if p.pattern[p.pos] == '\\' {
			esc, err := p.parseEscape(true)
			if err != nil {
				return classTerm{}, err
			}
			if esc.class != nil {
				return classTerm{}, p.errorf(dashOffset, "character class shorthand cannot be a range endpoint")
			}
			hi = esc.literal
		} else {
			hi = p.pattern[p.pos]
			p.pos++
		}

		if lo > hi {
			return classTerm{}, p.errorf(dashOffset, "invalid character class range %c-%c", lo, hi)
		}
		return classTerm{ranges: []runeRange{{lo: lo, hi: hi}}}, nil
	}
	return classTerm{ranges: []runeRange{{lo: lo, hi: lo}}}, nil
}

func asciiDigitRanges() []runeRange {
	return []runeRange{{lo: '0', hi: '9'}}
}

func asciiSpaceRanges() []runeRange {
	return []runeRange{
		{lo: '\t', hi: '\n'},
		{lo: '\f', hi: '\r'},
		{lo: ' ', hi: ' '},
	}
}

func asciiWordRanges() []runeRange {
	return []runeRange{
		{lo: '0', hi: '9'},
		{lo: 'A', hi: 'Z'},
		{lo: '_', hi: '_'},
		{lo: 'a', hi: 'z'},
	}
}
