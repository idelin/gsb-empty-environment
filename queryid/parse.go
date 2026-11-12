package queryid

import "fmt"

// ParseSetExpr 解析有限集合表达式。优先级自低向高：
//
//	union := inter { ("|" | "∪" | "-" | "∖") inter }  // 左结合，"-" 为差集
//	inter := unary { ("&" | "∩") unary }
//	unary := ("!" | "~" | "¬") unary | primary
//	primary := "(" union ")" | IDENT | "∅" | "EMPTY" | "FULL"
//
// IDENT 为 [A-Za-z_][A-Za-z0-9_]*；EMPTY 与 FULL 是保留字。
func ParseSetExpr(src string) (*SetExpr, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.parseUnion()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("queryid: unexpected token %q at %d", p.toks[p.pos].text, p.toks[p.pos].off)
	}
	return e, nil
}

type token struct {
	kind byte // 'i' 标识符，'('，')'，'|'，'&'，'-'，'!'
	text string
	off  int
}

func lex(src string) ([]token, error) {
	var toks []token
	rs := []rune(src)
	byteOff := 0
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		width := len(string(r))
		off := byteOff
		byteOff += width
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
		case r == '(' || r == ')':
			toks = append(toks, token{kind: byte(r), text: string(r), off: off})
		case r == '|' || r == '∪':
			toks = append(toks, token{kind: '|', text: string(r), off: off})
		case r == '&' || r == '∩':
			toks = append(toks, token{kind: '&', text: string(r), off: off})
		case r == '-' || r == '∖':
			toks = append(toks, token{kind: '-', text: string(r), off: off})
		case r == '!' || r == '~' || r == '¬':
			toks = append(toks, token{kind: '!', text: string(r), off: off})
		case r == '∅':
			toks = append(toks, token{kind: 'i', text: "EMPTY", off: off})
		case isIdentStart(r):
			j := i + 1
			for j < len(rs) && isIdentPart(rs[j]) {
				j++
			}
			name := string(rs[i:j])
			toks = append(toks, token{kind: 'i', text: name, off: off})
			for k := i + 1; k < j; k++ {
				byteOff += len(string(rs[k]))
			}
			i = j - 1
		default:
			return nil, fmt.Errorf("queryid: invalid character %q at %d", r, off)
		}
	}
	return toks, nil
}

func isIdentStart(r rune) bool {
	return r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() (byte, bool) {
	if p.pos < len(p.toks) {
		return p.toks[p.pos].kind, true
	}
	return 0, false
}

func (p *parser) parseUnion() (*SetExpr, error) {
	left, err := p.parseInter()
	if err != nil {
		return nil, err
	}
	for {
		k, ok := p.peek()
		if !ok || (k != '|' && k != '-') {
			return left, nil
		}
		op := k
		p.pos++
		right, err := p.parseInter()
		if err != nil {
			return nil, err
		}
		if op == '|' {
			left = Union(left, right)
		} else {
			left = Diff(left, right)
		}
	}
}

func (p *parser) parseInter() (*SetExpr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		k, ok := p.peek()
		if !ok || k != '&' {
			return left, nil
		}
		p.pos++
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = Inter(left, right)
	}
}

func (p *parser) parseUnary() (*SetExpr, error) {
	k, ok := p.peek()
	if !ok {
		return nil, fmt.Errorf("queryid: unexpected end of expression")
	}
	if k == '!' {
		p.pos++
		sub, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return Comp(sub), nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (*SetExpr, error) {
	tok := p.toks[p.pos]
	switch tok.kind {
	case '(':
		p.pos++
		e, err := p.parseUnion()
		if err != nil {
			return nil, err
		}
		if k, ok := p.peek(); !ok || k != ')' {
			return nil, fmt.Errorf("queryid: missing ')' for '(' at %d", tok.off)
		}
		p.pos++
		return e, nil
	case 'i':
		p.pos++
		switch tok.text {
		case "EMPTY":
			return Empty(), nil
		case "FULL":
			return Full(), nil
		}
		return Base(tok.text), nil
	}
	return nil, fmt.Errorf("queryid: unexpected token %q at %d", tok.text, tok.off)
}
