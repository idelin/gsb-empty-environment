package sandbox

import (
	"fmt"
	"strconv"
)

// maxParseDepth 限制解析器递归深度,保护宿主进程调用栈。
const maxParseDepth = 200

type parser struct {
	toks   []token
	pos    int
	depth  int
	charge func(int) error
}

// parse 词法+语法分析。charge 用于把解析开销计入执行预算。
func parse(src string, charge func(int) error) (Node, error) {
	if err := charge(len(src) + 1); err != nil {
		return nil, err
	}
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, charge: charge}
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("%w: unexpected trailing token %q", ErrValidation, p.peek().lit)
	}
	return node, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) enter() error {
	p.depth++
	if p.depth > maxParseDepth {
		return fmt.Errorf("%w: expression nesting too deep", ErrValidation)
	}
	return p.charge(1)
}
func (p *parser) leave() { p.depth-- }

func (p *parser) parseOr() (Node, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOp && p.peek().lit == "||" {
		p.next()
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = &BinaryExpr{Op: "||", L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseAnd() (Node, error) {
	l, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOp && p.peek().lit == "&&" {
		p.next()
		r, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		l = &BinaryExpr{Op: "&&", L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseEquality() (Node, error) {
	l, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOp && (p.peek().lit == "==" || p.peek().lit == "!=") {
		op := p.next().lit
		r, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		l = &BinaryExpr{Op: op, L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseComparison() (Node, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOp {
		op := p.peek().lit
		if op != "<" && op != "<=" && op != ">" && op != ">=" {
			break
		}
		p.next()
		r, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		l = &BinaryExpr{Op: op, L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseAdd() (Node, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOp && (p.peek().lit == "+" || p.peek().lit == "-") {
		op := p.next().lit
		r, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		l = &BinaryExpr{Op: op, L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseMul() (Node, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOp && (p.peek().lit == "*" || p.peek().lit == "/" || p.peek().lit == "%") {
		op := p.next().lit
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = &BinaryExpr{Op: op, L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseUnary() (Node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	if p.peek().kind == tOp && (p.peek().lit == "-" || p.peek().lit == "!") {
		op := p.next().lit
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: op, X: x}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	t := p.peek()
	switch t.kind {
	case tNumber:
		p.next()
		v, err := strconv.ParseFloat(t.lit, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: bad number %q", ErrValidation, t.lit)
		}
		return &NumberLit{Value: v}, nil
	case tString:
		p.next()
		return &StringLit{Value: t.lit}, nil
	case tIdent:
		p.next()
		switch t.lit {
		case "true":
			return &BoolLit{Value: true}, nil
		case "false":
			return &BoolLit{Value: false}, nil
		}
		if p.peek().kind == tLParen {
			p.next()
			var args []Node
			if p.peek().kind != tRParen {
				for {
					a, err := p.parseOr()
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
			}
			if p.peek().kind != tRParen {
				return nil, fmt.Errorf("%w: missing ')' in call to %q", ErrValidation, t.lit)
			}
			p.next()
			return &CallExpr{Name: t.lit, Args: args}, nil
		}
		return &Ident{Name: t.lit}, nil
	case tLParen:
		p.next()
		node, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tRParen {
			return nil, fmt.Errorf("%w: missing ')'", ErrValidation)
		}
		p.next()
		return node, nil
	}
	return nil, fmt.Errorf("%w: unexpected token %q", ErrValidation, t.lit)
}
