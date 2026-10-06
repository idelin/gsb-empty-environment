package main

import (
	"fmt"
	"strings"
)

// Expr is a boolean expression over switch variables.
type Expr interface {
	Eval(env map[string]bool) bool
}

type varExpr struct{ name string }
type notExpr struct{ sub Expr }
type andExpr struct{ l, r Expr }
type orExpr struct{ l, r Expr }
type constExpr struct{ v bool }

func (e varExpr) Eval(env map[string]bool) bool   { return env[e.name] }
func (e notExpr) Eval(env map[string]bool) bool   { return !e.sub.Eval(env) }
func (e andExpr) Eval(env map[string]bool) bool   { return e.l.Eval(env) && e.r.Eval(env) }
func (e orExpr) Eval(env map[string]bool) bool    { return e.l.Eval(env) || e.r.Eval(env) }
func (e constExpr) Eval(env map[string]bool) bool { return e.v }

// token kinds
const (
	tkEOF = iota
	tkIdent
	tkNot
	tkAnd
	tkOr
	tkLParen
	tkRParen
)

type token struct {
	kind int
	text string
}

func lexExpr(s string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '!':
			toks = append(toks, token{tkNot, "!"})
			i++
		case c == '(':
			toks = append(toks, token{tkLParen, "("})
			i++
		case c == ')':
			toks = append(toks, token{tkRParen, ")"})
			i++
		case c == '&':
			if i+1 < len(s) && s[i+1] == '&' {
				toks = append(toks, token{tkAnd, "&&"})
				i += 2
			} else {
				toks = append(toks, token{tkAnd, "&"})
				i++
			}
		case c == '|':
			if i+1 < len(s) && s[i+1] == '|' {
				toks = append(toks, token{tkOr, "||"})
				i += 2
			} else {
				toks = append(toks, token{tkOr, "|"})
				i++
			}
		case c == '_' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			j := i
			for j < len(s) {
				d := s[j]
				if d == '_' || d == '.' || (d >= 'a' && d <= 'z') || (d >= 'A' && d <= 'Z') || (d >= '0' && d <= '9') {
					j++
				} else {
					break
				}
			}
			toks = append(toks, token{tkIdent, s[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q in expression %q", string(c), s)
		}
	}
	toks = append(toks, token{tkEOF, ""})
	return toks, nil
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

// grammar: or := and (| and)* ; and := unary (& unary)* ; unary := ! unary | primary
func (p *parser) parseOr() (Expr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tkOr {
		p.next()
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = orExpr{l, r}
	}
	return l, nil
}

func (p *parser) parseAnd() (Expr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tkAnd {
		p.next()
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = andExpr{l, r}
	}
	return l, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.peek().kind == tkNot {
		p.next()
		sub, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return notExpr{sub}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tkLParen:
		p.next()
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tkRParen {
			return nil, fmt.Errorf("expected ) but found %q", p.peek().text)
		}
		p.next()
		return e, nil
	case tkIdent:
		p.next()
		switch strings.ToLower(t.text) {
		case "true":
			return constExpr{true}, nil
		case "false":
			return constExpr{false}, nil
		}
		return varExpr{t.text}, nil
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}

// ParseExpr parses a boolean expression. An empty string means "always true".
func ParseExpr(s string) (Expr, error) {
	if strings.TrimSpace(s) == "" {
		return constExpr{true}, nil
	}
	toks, err := lexExpr(s)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tkEOF {
		return nil, fmt.Errorf("trailing tokens in expression %q", s)
	}
	return e, nil
}
