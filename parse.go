package domain

import (
	"fmt"
	"math/big"
)

// Parse parses an expression in the single variable x.
//
// Grammar (whitespace tolerated):
//
//	expr    := term (('+' | '-') term)*
//	term    := power (('*' | '/') power)*
//	power   := unary (('^' | '**') power)?        // right associative
//	unary   := ('-' | '+') unary | primary        // binds tighter than ^
//	primary := number | 'x' | '(' expr ')'
//	number  := digits ('.' digits)?
//
// Exponents must be rational constants (e.g. 1/2, -2/3, 0.25, 2);
// anything else is rejected as unsupported. Constant subexpressions are
// folded exactly, so 2/4 and 1/2 denote the same exponent.
func Parse(s string) (*Expr, error) {
	p := &parser{s: s}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipWS()
	if p.i != len(p.s) {
		return nil, fmt.Errorf("unexpected input %q", p.s[p.i:])
	}
	return fold(e), nil
}

type parser struct {
	s string
	i int
}

func (p *parser) skipWS() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *parser) peek() byte {
	p.skipWS()
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *parser) parseExpr() (*Expr, error) {
	e, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		c := p.peek()
		if c != '+' && c != '-' {
			return e, nil
		}
		p.i++
		r, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		if c == '+' {
			e = &Expr{Kind: KAdd, L: e, R: r}
		} else {
			e = &Expr{Kind: KSub, L: e, R: r}
		}
	}
}

func (p *parser) parseTerm() (*Expr, error) {
	e, err := p.parsePower()
	if err != nil {
		return nil, err
	}
	for {
		c := p.peek()
		if c == '*' && p.i+1 < len(p.s) && p.s[p.i+1] == '*' {
			return nil, fmt.Errorf("unexpected '**' after complete term")
		}
		if c != '*' && c != '/' {
			return e, nil
		}
		p.i++
		r, err := p.parsePower()
		if err != nil {
			return nil, err
		}
		if c == '*' {
			e = &Expr{Kind: KMul, L: e, R: r}
		} else {
			e = &Expr{Kind: KDiv, L: e, R: r}
		}
	}
}

func (p *parser) parsePower() (*Expr, error) {
	base, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	c := p.peek()
	if c == '^' {
		p.i++
		return p.makePow(base)
	}
	if c == '*' && p.i+1 < len(p.s) && p.s[p.i+1] == '*' {
		p.i += 2
		return p.makePow(base)
	}
	return base, nil
}

func (p *parser) makePow(base *Expr) (*Expr, error) {
	rhs, err := p.parsePower()
	if err != nil {
		return nil, err
	}
	fr := fold(rhs)
	if fr.Kind != KConst {
		return nil, fmt.Errorf("unsupported exponent %s: must be a rational constant", fr)
	}
	return Pow(base, fr.Val), nil
}

func (p *parser) parseUnary() (*Expr, error) {
	switch c := p.peek(); c {
	case '-':
		p.i++
		e, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return neg(e), nil
	case '+':
		p.i++
		return p.parseUnary()
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (*Expr, error) {
	switch c := p.peek(); {
	case c == '(':
		p.i++
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, fmt.Errorf("expected ')'")
		}
		p.i++
		return e, nil
	case c == 'x':
		p.i++
		return Var(), nil
	case isDigit(c) || c == '.':
		return p.parseNumber()
	case c == 0:
		return nil, fmt.Errorf("unexpected end of input")
	}
	return nil, fmt.Errorf("unexpected character %q", p.s[p.i])
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (p *parser) parseNumber() (*Expr, error) {
	start := p.i
	for p.i < len(p.s) && (isDigit(p.s[p.i]) || p.s[p.i] == '.') {
		p.i++
	}
	txt := p.s[start:p.i]
	r, ok := new(big.Rat).SetString(txt)
	if !ok {
		return nil, fmt.Errorf("invalid number %q", txt)
	}
	return Const(r), nil
}

func neg(e *Expr) *Expr {
	if e.Kind == KConst {
		return Const(new(big.Rat).Neg(e.Val))
	}
	return &Expr{Kind: KMul, L: Const(big.NewRat(-1, 1)), R: e}
}

// fold evaluates constant subexpressions exactly. It never merges
// nested powers and never approximates: only rational operations on
// constants (and integer powers of constants, which stay rational)
// are folded.
func fold(e *Expr) *Expr {
	switch e.Kind {
	case KAdd, KSub, KMul, KDiv:
		l, r := fold(e.L), fold(e.R)
		if l.Kind == KConst && r.Kind == KConst {
			switch e.Kind {
			case KAdd:
				return Const(new(big.Rat).Add(l.Val, r.Val))
			case KSub:
				return Const(new(big.Rat).Sub(l.Val, r.Val))
			case KMul:
				return Const(new(big.Rat).Mul(l.Val, r.Val))
			case KDiv:
				if r.Val.Sign() != 0 {
					return Const(new(big.Rat).Quo(l.Val, r.Val))
				}
			}
		}
		return &Expr{Kind: e.Kind, L: l, R: r}
	case KPow:
		b := fold(e.L)
		if b.Kind == KConst && e.Exp.IsInt() {
			if v, ok := constIntPow(b.Val, e.Exp.Num()); ok {
				return Const(v)
			}
		}
		return Pow(b, e.Exp)
	default:
		return e
	}
}

// constIntPow computes base^n exactly for an integer n, when defined.
func constIntPow(base *big.Rat, n *big.Int) (*big.Rat, bool) {
	if n.Sign() == 0 {
		return big.NewRat(1, 1), true
	}
	if base.Sign() == 0 && n.Sign() < 0 {
		return nil, false // 0 to a negative power: undefined, keep as is
	}
	abs := new(big.Int).Abs(n)
	num := new(big.Int).Exp(base.Num(), abs, nil)
	den := new(big.Int).Exp(base.Denom(), abs, nil)
	if n.Sign() < 0 {
		num, den = den, num
	}
	return new(big.Rat).SetFrac(num, den), true
}
