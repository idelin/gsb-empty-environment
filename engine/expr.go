package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// Expr is a parsed formula.
type Expr interface{ exprNode() }

// Const is an integer constant.
type Const struct{ N int }

// Ref is a reference to another cell.
type Ref struct{ ID string }

// ErrLit is an explicit error literal such as #ERR!.
type ErrLit struct{ Code ErrCode }

// Bin is a binary arithmetic operation ('+', '-' or '*').
type Bin struct {
	Op   byte
	L, R Expr
}

// If evaluates C; a non-zero value selects T, zero selects F.
// Only the selected branch is evaluated.
type If struct {
	C, T, F Expr
}

func (*Const) exprNode()  {}
func (*Ref) exprNode()    {}
func (*ErrLit) exprNode() {}
func (*Bin) exprNode()    {}
func (*If) exprNode()     {}

// Grammar:
//
//	expr    := add
//	add     := mul (('+' | '-') mul)*
//	mul     := unary ('*' unary)*
//	unary   := '-' unary | primary
//	primary := INT | IDENT | '#' IDENT '!' | 'IF' '(' expr ',' expr ',' expr ')' | '(' expr ')'
//
// IDENT is a cell id: [A-Za-z][A-Za-z0-9]*, case sensitive, except "IF".

const (
	tNum = iota
	tIdent
	tErr
	tOp
)

type tok struct {
	kind int
	s    string
	n    int
}

func isAlpha(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isAlnum(c byte) bool { return isAlpha(c) || c >= '0' && c <= '9' }

func lex(src string) ([]tok, error) {
	var toks []tok
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			n, err := strconv.Atoi(src[i:j])
			if err != nil {
				return nil, fmt.Errorf("bad number %q", src[i:j])
			}
			toks = append(toks, tok{kind: tNum, n: n})
			i = j
		case isAlpha(c):
			j := i
			for j < len(src) && isAlnum(src[j]) {
				j++
			}
			toks = append(toks, tok{kind: tIdent, s: src[i:j]})
			i = j
		case c == '#':
			j := i + 1
			for j < len(src) && isAlpha(src[j]) {
				j++
			}
			if j >= len(src) || src[j] != '!' {
				return nil, fmt.Errorf("bad error literal near %q", src[i:])
			}
			toks = append(toks, tok{kind: tErr, s: src[i : j+1]})
			i = j + 1
		case strings.IndexByte("+-*(),", c) >= 0:
			toks = append(toks, tok{kind: tOp, s: string(c)})
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q", string(c))
		}
	}
	return toks, nil
}

type parser struct {
	toks []tok
	pos  int
}

// Parse parses a formula source into an Expr.
func Parse(src string) (Expr, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("unexpected trailing input")
	}
	return e, nil
}

func (p *parser) peek() *tok {
	if p.pos < len(p.toks) {
		return &p.toks[p.pos]
	}
	return nil
}

func (p *parser) next() (tok, error) {
	if p.pos >= len(p.toks) {
		return tok{}, fmt.Errorf("unexpected end of input")
	}
	t := p.toks[p.pos]
	p.pos++
	return t, nil
}

func (p *parser) expectOp(s string) error {
	t := p.peek()
	if t == nil || t.kind != tOp || t.s != s {
		return fmt.Errorf("expected %q", s)
	}
	p.pos++
	return nil
}

func (p *parser) peekOp(s string) bool {
	t := p.peek()
	return t != nil && t.kind == tOp && t.s == s
}

func (p *parser) parseAdd() (Expr, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.peekOp("+") || p.peekOp("-") {
		op, _ := p.next()
		r, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		l = &Bin{Op: op.s[0], L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseMul() (Expr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peekOp("*") {
		p.next()
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = &Bin{Op: '*', L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.peekOp("-") {
		p.next()
		e, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Bin{Op: '-', L: &Const{}, R: e}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	t, err := p.next()
	if err != nil {
		return nil, err
	}
	switch t.kind {
	case tNum:
		return &Const{N: t.n}, nil
	case tErr:
		switch strings.ToUpper(t.s) {
		case string(ErrRef):
			return &ErrLit{Code: ErrRef}, nil
		case string(ErrCycle):
			return &ErrLit{Code: ErrCycle}, nil
		case string(ErrLiteral):
			return &ErrLit{Code: ErrLiteral}, nil
		}
		return nil, fmt.Errorf("unknown error literal %q", t.s)
	case tIdent:
		if strings.EqualFold(t.s, "IF") {
			if err := p.expectOp("("); err != nil {
				return nil, err
			}
			c, err := p.parseAdd()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(","); err != nil {
				return nil, err
			}
			th, err := p.parseAdd()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(","); err != nil {
				return nil, err
			}
			el, err := p.parseAdd()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return &If{C: c, T: th, F: el}, nil
		}
		return &Ref{ID: t.s}, nil
	case tOp:
		if t.s == "(" {
			e, err := p.parseAdd()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return e, nil
		}
	}
	return nil, fmt.Errorf("unexpected token %q", t.s)
}
