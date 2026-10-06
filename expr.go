package main

import (
	"fmt"
	"sort"
	"strings"
)

// Expr is a boolean expression over named switch variables.
type Expr interface {
	// Eval evaluates the expression; assignment bit i gives variable vars[i].
	Eval(assignment uint64, vars []string) bool
	Vars() map[string]bool
}

type varExpr struct{ name string }
type notExpr struct{ sub Expr }
type binExpr struct {
	op   byte // '&', '|', '^'
	l, r Expr
}
type constExpr struct{ val bool }

func (e varExpr) Eval(a uint64, vars []string) bool {
	for i, v := range vars {
		if v == e.name {
			return a&(1<<uint(i)) != 0
		}
	}
	return false
}
func (e varExpr) Vars() map[string]bool { return map[string]bool{e.name: true} }

func (e notExpr) Eval(a uint64, vars []string) bool { return !e.sub.Eval(a, vars) }
func (e notExpr) Vars() map[string]bool             { return e.sub.Vars() }

func (e binExpr) Eval(a uint64, vars []string) bool {
	switch e.op {
	case '&':
		return e.l.Eval(a, vars) && e.r.Eval(a, vars)
	case '|':
		return e.l.Eval(a, vars) || e.r.Eval(a, vars)
	default: // '^'
		return e.l.Eval(a, vars) != e.r.Eval(a, vars)
	}
}
func (e binExpr) Vars() map[string]bool {
	m := e.l.Vars()
	for v := range e.r.Vars() {
		m[v] = true
	}
	return m
}

func (e constExpr) Eval(a uint64, vars []string) bool { return e.val }
func (e constExpr) Vars() map[string]bool             { return map[string]bool{} }

// --- lexer ---

type token struct {
	kind byte // 'v' var, 'c' const, or operator/paren byte
	text string
	val  bool
}

func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '!' || c == '~':
			toks = append(toks, token{kind: '!'})
			i++
		case c == '&':
			if i+1 < len(src) && src[i+1] == '&' {
				i++
			}
			toks = append(toks, token{kind: '&'})
			i++
		case c == '|':
			if i+1 < len(src) && src[i+1] == '|' {
				i++
			}
			toks = append(toks, token{kind: '|'})
			i++
		case c == '^' || c == '(' || c == ')':
			toks = append(toks, token{kind: c})
			i++
		case c == '0' || c == '1':
			toks = append(toks, token{kind: 'c', val: c == '1'})
			i++
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(src) && (src[j] == '_' || src[j] >= 'a' && src[j] <= 'z' ||
				src[j] >= 'A' && src[j] <= 'Z' || src[j] >= '0' && src[j] <= '9') {
				j++
			}
			word := src[i:j]
			switch strings.ToLower(word) {
			case "true":
				toks = append(toks, token{kind: 'c', val: true})
			case "false":
				toks = append(toks, token{kind: 'c', val: false})
			case "and":
				toks = append(toks, token{kind: '&'})
			case "or":
				toks = append(toks, token{kind: '|'})
			case "xor":
				toks = append(toks, token{kind: '^'})
			case "not":
				toks = append(toks, token{kind: '!'})
			default:
				toks = append(toks, token{kind: 'v', text: word})
			}
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q at position %d", c, i)
		}
	}
	return toks, nil
}

// --- parser ---
// expr := orExpr
// orExpr  := xorExpr (('|') xorExpr)*
// xorExpr := andExpr (('^') andExpr)*
// andExpr := unary  (('&') unary)*
// unary   := '!' unary | primary
// primary := '(' expr ')' | IDENT | const

type parser struct {
	toks []token
	pos  int
}

func ParseExpr(src string) (Expr, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("empty expression")
	}
	p := &parser{toks: toks}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("unexpected trailing token near position %d", p.pos)
	}
	return e, nil
}

func (p *parser) peek() (byte, bool) {
	if p.pos < len(p.toks) {
		return p.toks[p.pos].kind, true
	}
	return 0, false
}

func (p *parser) parseOr() (Expr, error) {
	l, err := p.parseXor()
	if err != nil {
		return nil, err
	}
	for {
		if k, ok := p.peek(); ok && k == '|' {
			p.pos++
			r, err := p.parseXor()
			if err != nil {
				return nil, err
			}
			l = binExpr{op: '|', l: l, r: r}
		} else {
			return l, nil
		}
	}
}

func (p *parser) parseXor() (Expr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		if k, ok := p.peek(); ok && k == '^' {
			p.pos++
			r, err := p.parseAnd()
			if err != nil {
				return nil, err
			}
			l = binExpr{op: '^', l: l, r: r}
		} else {
			return l, nil
		}
	}
}

func (p *parser) parseAnd() (Expr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		if k, ok := p.peek(); ok && k == '&' {
			p.pos++
			r, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			l = binExpr{op: '&', l: l, r: r}
		} else {
			return l, nil
		}
	}
}

func (p *parser) parseUnary() (Expr, error) {
	if k, ok := p.peek(); ok && k == '!' {
		p.pos++
		sub, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return notExpr{sub: sub}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	k, ok := p.peek()
	if !ok {
		return nil, fmt.Errorf("unexpected end of expression")
	}
	t := p.toks[p.pos]
	switch k {
	case 'v':
		p.pos++
		return varExpr{name: t.text}, nil
	case 'c':
		p.pos++
		return constExpr{val: t.val}, nil
	case '(':
		p.pos++
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if k, ok := p.peek(); !ok || k != ')' {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return e, nil
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}

// SortedVars returns the sorted union of variables used by both expressions.
func SortedVars(a, b Expr) []string {
	m := a.Vars()
	for v := range b.Vars() {
		m[v] = true
	}
	vars := make([]string, 0, len(m))
	for v := range m {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	return vars
}
