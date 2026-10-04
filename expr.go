package domain

import (
	"fmt"
	"math/big"
)

// Kind identifies the node type of an Expr.
type Kind int

const (
	KConst Kind = iota // rational constant (C)
	KVar               // the variable (Name)
	KNeg               // -A
	KAdd               // A + B
	KSub               // A - B
	KMul               // A * B
	KDiv               // A / B
	KPow               // A ^ Exp, Exp a rational constant in lowest terms
)

// Expr is an expression tree over rational constants, one variable,
// the four arithmetic operations and rational powers.
type Expr struct {
	Kind Kind
	C    *big.Rat // KConst
	Name string   // KVar
	A, B *Expr    // operands (A only for KNeg; B unused for KPow)
	Exp  *big.Rat // KPow exponent, always normalized (big.Rat keeps lowest terms)
}

func Const(c *big.Rat) *Expr { return &Expr{Kind: KConst, C: new(big.Rat).Set(c)} }
func ConstInt(v int64) *Expr { return &Expr{Kind: KConst, C: big.NewRat(v, 1)} }
func Var(name string) *Expr  { return &Expr{Kind: KVar, Name: name} }
func Neg(a *Expr) *Expr      { return &Expr{Kind: KNeg, A: a} }
func Add(a, b *Expr) *Expr   { return &Expr{Kind: KAdd, A: a, B: b} }
func Sub(a, b *Expr) *Expr   { return &Expr{Kind: KSub, A: a, B: b} }
func Mul(a, b *Expr) *Expr   { return &Expr{Kind: KMul, A: a, B: b} }
func Div(a, b *Expr) *Expr   { return &Expr{Kind: KDiv, A: a, B: b} }

// Pow builds base^(p/q). big.NewRat normalizes, so Pow(x, 2, 4) and
// Pow(x, 1, 2) are the same exponent; nested Pow nodes are never merged.
func Pow(base *Expr, p, q int64) *Expr {
	return &Expr{Kind: KPow, A: base, Exp: big.NewRat(p, q)}
}

func PowRat(base *Expr, exp *big.Rat) *Expr {
	return &Expr{Kind: KPow, A: base, Exp: new(big.Rat).Set(exp)}
}

// String renders the expression with minimal parentheses.
func (e *Expr) String() string { return e.pp(0) }

func (e *Expr) pp(parentPrec int) string {
	var s string
	prec := 5
	switch e.Kind {
	case KConst:
		s = e.C.RatString()
		if e.C.Sign() < 0 {
			prec = 3
		}
	case KVar:
		s = e.Name
	case KNeg:
		s = "-" + e.A.pp(3)
		prec = 3
	case KAdd:
		s = e.A.pp(1) + " + " + e.B.pp(2)
		prec = 1
	case KSub:
		s = e.A.pp(1) + " - " + e.B.pp(2)
		prec = 1
	case KMul:
		s = e.A.pp(2) + "*" + e.B.pp(3)
		prec = 2
	case KDiv:
		s = e.A.pp(2) + "/" + e.B.pp(3)
		prec = 2
	case KPow:
		prec = 4
		p := e.Exp.Num().Int64()
		q := e.Exp.Denom().Int64()
		if q == 1 && p >= 0 {
			s = fmt.Sprintf("%s^%d", e.A.pp(5), p)
		} else {
			s = fmt.Sprintf("%s^(%s)", e.A.pp(5), e.Exp.RatString())
		}
	}
	if prec < parentPrec {
		return "(" + s + ")"
	}
	return s
}

// affine reports whether e is of the form a*x + b with rational a, b.
func affine(e *Expr) (a, b *big.Rat, ok bool) {
	switch e.Kind {
	case KConst:
		return big.NewRat(0, 1), new(big.Rat).Set(e.C), true
	case KVar:
		return big.NewRat(1, 1), big.NewRat(0, 1), true
	case KNeg:
		a, b, ok := affine(e.A)
		if !ok {
			return nil, nil, false
		}
		return a.Neg(a), b.Neg(b), true
	case KAdd, KSub:
		a1, b1, ok1 := affine(e.A)
		a2, b2, ok2 := affine(e.B)
		if !ok1 || !ok2 {
			return nil, nil, false
		}
		if e.Kind == KSub {
			a2.Neg(a2)
			b2.Neg(b2)
		}
		return a1.Add(a1, a2), b1.Add(b1, b2), true
	case KMul:
		a1, b1, ok1 := affine(e.A)
		a2, b2, ok2 := affine(e.B)
		if !ok1 || !ok2 {
			return nil, nil, false
		}
		switch {
		case a1.Sign() == 0:
			return new(big.Rat).Mul(b1, a2), new(big.Rat).Mul(b1, b2), true
		case a2.Sign() == 0:
			return new(big.Rat).Mul(b2, a1), new(big.Rat).Mul(b2, b1), true
		}
		return nil, nil, false
	case KDiv:
		a1, b1, ok1 := affine(e.A)
		a2, b2, ok2 := affine(e.B)
		if !ok1 || !ok2 || a2.Sign() != 0 || b2.Sign() == 0 {
			return nil, nil, false
		}
		return a1.Quo(a1, b2), b1.Quo(b1, b2), true
	case KPow:
		p := e.Exp.Num().Int64()
		q := e.Exp.Denom().Int64()
		if p == 0 {
			return big.NewRat(0, 1), big.NewRat(1, 1), true
		}
		if q == 1 && p == 1 {
			return affine(e.A)
		}
	}
	return nil, nil, false
}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

// Parse parses an expression over the single variable varName.
//
// Grammar:
//
//	expr     := term (('+'|'-') term)*
//	term     := power (('*'|'/') power)*
//	power    := unary ('^' exponent)?
//	unary    := '-' unary | '+' unary | primary
//	primary  := number | ident | '(' expr ')'
//	exponent := ['-'] integer | '(' ['-'] integer ['/' integer] ')'
//
// Exponents must be rational literals; fractions and negative exponents
// need parentheses, e.g. x^(1/2), x^(-2/3), x^2, x^-1.
func Parse(src, varName string) (*Expr, error) {
	p := &parser{s: src, v: varName}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipWS()
	if p.pos != len(p.s) {
		return nil, fmt.Errorf("unexpected trailing input at %d: %q", p.pos, p.s[p.pos:])
	}
	return e, nil
}

type parser struct {
	s   string
	v   string
	pos int
}

func (p *parser) skipWS() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t' || p.s[p.pos] == '\n') {
		p.pos++
	}
}

func (p *parser) peek() byte {
	p.skipWS()
	if p.pos >= len(p.s) {
		return 0
	}
	return p.s[p.pos]
}

func (p *parser) expect(c byte) error {
	if p.peek() != c {
		return fmt.Errorf("expected %q at position %d in %q", string(c), p.pos, p.s)
	}
	p.pos++
	return nil
}

func (p *parser) parseExpr() (*Expr, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek() {
		case '+':
			p.pos++
			r, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			left = Add(left, r)
		case '-':
			p.pos++
			r, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			left = Sub(left, r)
		default:
			return left, nil
		}
	}
}

func (p *parser) parseTerm() (*Expr, error) {
	left, err := p.parsePower()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek() {
		case '*':
			p.pos++
			r, err := p.parsePower()
			if err != nil {
				return nil, err
			}
			left = Mul(left, r)
		case '/':
			p.pos++
			r, err := p.parsePower()
			if err != nil {
				return nil, err
			}
			left = Div(left, r)
		default:
			return left, nil
		}
	}
}

func (p *parser) parsePower() (*Expr, error) {
	base, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	if p.peek() == '^' {
		p.pos++
		exp, err := p.parseExponent()
		if err != nil {
			return nil, err
		}
		return PowRat(base, exp), nil
	}
	return base, nil
}

func (p *parser) parseUnary() (*Expr, error) {
	switch p.peek() {
	case '-':
		p.pos++
		e, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return Neg(e), nil
	case '+':
		p.pos++
		return p.parseUnary()
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (*Expr, error) {
	c := p.peek()
	switch {
	case c == '(':
		p.pos++
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(')'); err != nil {
			return nil, err
		}
		return e, nil
	case c >= '0' && c <= '9':
		return p.parseNumber()
	case isAlpha(c):
		name := p.parseIdent()
		if name != p.v {
			return nil, fmt.Errorf("unknown identifier %q (only variable %q is allowed)", name, p.v)
		}
		return Var(name), nil
	}
	return nil, fmt.Errorf("unexpected character %q at position %d in %q", string(c), p.pos, p.s)
}

func (p *parser) parseNumber() (*Expr, error) {
	p.skipWS()
	start := p.pos
	for p.pos < len(p.s) && (isDigit(p.s[p.pos]) || p.s[p.pos] == '.') {
		p.pos++
	}
	tok := p.s[start:p.pos]
	r, ok := new(big.Rat).SetString(tok)
	if !ok {
		return nil, fmt.Errorf("invalid number %q", tok)
	}
	return Const(r), nil
}

// parseExponent parses a rational literal exponent: an optionally signed
// integer, or a parenthesized optionally signed fraction like (-2/3).
func (p *parser) parseExponent() (*big.Rat, error) {
	p.skipWS()
	if p.peek() == '(' {
		p.pos++
		r, err := p.parseSignedFraction()
		if err != nil {
			return nil, err
		}
		if err := p.expect(')'); err != nil {
			return nil, err
		}
		return r, nil
	}
	return p.parseSignedInt()
}

func (p *parser) parseSignedFraction() (*big.Rat, error) {
	num, err := p.parseSignedInt()
	if err != nil {
		return nil, err
	}
	p.skipWS()
	if p.pos < len(p.s) && p.s[p.pos] == '/' {
		p.pos++
		den, err := p.parseSignedInt()
		if err != nil {
			return nil, err
		}
		if den.Sign() == 0 {
			return nil, fmt.Errorf("zero denominator in exponent")
		}
		if den.Sign() < 0 {
			return nil, fmt.Errorf("negative denominator in exponent")
		}
		return num.Quo(num, den), nil
	}
	return num, nil
}

func (p *parser) parseSignedInt() (*big.Rat, error) {
	p.skipWS()
	neg := false
	if p.pos < len(p.s) && (p.s[p.pos] == '-' || p.s[p.pos] == '+') {
		neg = p.s[p.pos] == '-'
		p.pos++
	}
	p.skipWS()
	start := p.pos
	for p.pos < len(p.s) && isDigit(p.s[p.pos]) {
		p.pos++
	}
	if start == p.pos {
		return nil, fmt.Errorf("expected integer at position %d in %q", p.pos, p.s)
	}
	n, ok := new(big.Int).SetString(p.s[start:p.pos], 10)
	if !ok {
		return nil, fmt.Errorf("invalid integer at position %d", start)
	}
	r := new(big.Rat).SetInt(n)
	if neg {
		r.Neg(r)
	}
	return r, nil
}

func (p *parser) parseIdent() string {
	p.skipWS()
	start := p.pos
	for p.pos < len(p.s) && (isAlpha(p.s[p.pos]) || isDigit(p.s[p.pos])) {
		p.pos++
	}
	return p.s[start:p.pos]
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}
