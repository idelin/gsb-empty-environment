package pretty

import "fmt"

// Parse parses source text into an AST.
func Parse(src string) (Expr, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.parseExpr(lowestPrec + 1)
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tEOF {
		return nil, fmt.Errorf("unexpected trailing token %q at %d", p.cur().text, p.cur().pos)
	}
	return e, nil
}

type parser struct {
	toks []token
	i    int
}

func (p *parser) cur() token  { return p.toks[p.i] }
func (p *parser) next() token { t := p.toks[p.i]; p.i++; return t }

// parseExpr is a precedence-climbing parser for binary operators.
func (p *parser) parseExpr(minPrec int) (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tOp {
		prec := binPrec(p.cur().text)
		if prec < minPrec {
			break
		}
		op := p.next().text
		right, err := p.parseExpr(prec + 1) // left associative
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: op, X: left, Y: right}
	}
	return left, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.cur().kind == tOp && (p.cur().text == "-" || p.cur().text == "!") {
		op := p.next().text
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: op, X: x}, nil
	}
	return p.parsePostfix()
}

// parsePostfix parses a primary expression followed by call suffixes.
func (p *parser) parsePostfix() (Expr, error) {
	e, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tLParen {
		p.next()
		var args []Expr
		if p.cur().kind != tRParen {
			for {
				a, err := p.parseExpr(lowestPrec + 1)
				if err != nil {
					return nil, err
				}
				args = append(args, a)
				if p.cur().kind != tComma {
					break
				}
				p.next()
			}
		}
		if p.cur().kind != tRParen {
			return nil, fmt.Errorf("expected ) at %d", p.cur().pos)
		}
		p.next()
		e = &Call{Func: e, Args: args}
	}
	return e, nil
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.cur()
	switch t.kind {
	case tNumber:
		p.next()
		return &Number{Value: t.text}, nil
	case tString:
		p.next()
		return &String{Value: t.text}, nil
	case tIdent:
		p.next()
		return &Ident{Name: t.text}, nil
	case tLParen:
		p.next()
		e, err := p.parseExpr(lowestPrec + 1)
		if err != nil {
			return nil, err
		}
		if p.cur().kind != tRParen {
			return nil, fmt.Errorf("expected ) at %d", p.cur().pos)
		}
		p.next()
		return e, nil
	}
	return nil, fmt.Errorf("unexpected token %q at %d", t.text, t.pos)
}
