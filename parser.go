package exprfmt

import "fmt"

// binPrec maps binary operators to their precedence (higher binds tighter).
var binPrec = map[string]int{
	"||": 1,
	"&&": 2,
	"==": 3, "!=": 3,
	"<": 4, "<=": 4, ">": 4, ">=": 4,
	"+": 5, "-": 5,
	"*": 6, "/": 6, "%": 6,
}

// Parse parses src into an AST. Parentheses are not kept in the tree; the
// printer re-derives the minimal set needed to preserve the structure.
func Parse(src string) (Expr, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.parseExpr(1)
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tEOF {
		return nil, fmt.Errorf("unexpected trailing token %q at offset %d", t.text, t.pos)
	}
	return e, nil
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != tEOF {
		p.pos++
	}
	return t
}

func (p *parser) expect(k tokKind) (token, error) {
	t := p.next()
	if t.kind != k {
		return t, fmt.Errorf("unexpected token %q at offset %d", t.text, t.pos)
	}
	return t, nil
}

// parseExpr is a precedence-climbing parser for binary expressions.
func (p *parser) parseExpr(minPrec int) (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tOp {
			return left, nil
		}
		prec, ok := binPrec[t.text]
		if !ok || prec < minPrec {
			return left, nil
		}
		p.next()
		right, err := p.parseExpr(prec + 1)
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: t.text, X: left, Y: right}
	}
}

func (p *parser) parseUnary() (Expr, error) {
	t := p.peek()
	if t.kind == tOp && (t.text == "-" || t.text == "!") {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: t.text, X: x}, nil
	}
	return p.parsePostfix()
}

// parsePostfix parses a primary expression followed by any number of call
// suffixes. Trailing commas in argument lists are accepted so the printer
// can emit one argument per line uniformly.
func (p *parser) parsePostfix() (Expr, error) {
	e, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tLParen {
		p.next()
		var args []Expr
		for p.peek().kind != tRParen {
			a, err := p.parseExpr(1)
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			if p.peek().kind == tComma {
				p.next()
				continue
			}
			break
		}
		if _, err := p.expect(tRParen); err != nil {
			return nil, err
		}
		e = &Call{Func: e, Args: args}
	}
	return e, nil
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.next()
	switch t.kind {
	case tNum:
		return &Number{Value: t.text}, nil
	case tStr:
		return &Str{Value: t.text}, nil
	case tIdent:
		return &Ident{Name: t.text}, nil
	case tLParen:
		e, err := p.parseExpr(1)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tRParen); err != nil {
			return nil, err
		}
		return e, nil
	}
	return nil, fmt.Errorf("unexpected token %q at offset %d", t.text, t.pos)
}
