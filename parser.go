package netsec

import (
	"fmt"
	"strconv"
)

type parser struct {
	tokens []token
	pos    int
}

func newParser(src string) (*parser, error) {
	tokens, err := lex(src)
	if err != nil {
		return nil, err
	}
	return &parser{tokens: tokens, pos: 0}, nil
}

var keywords = map[string]bool{
	"permit": true, "forbid": true,
	"principal": true, "action": true, "resource": true,
	"when": true, "has": true, "in": true,
	"true": true, "false": true,
}

func (p *parser) peek() token {
	if p.pos >= len(p.tokens) {
		return token{kind: tEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) next() token {
	tok := p.peek()
	p.pos++
	return tok
}

func (p *parser) expectIdent(word string) error {
	tok := p.peek()
	if tok.kind != tIdent || tok.text != word {
		return fmt.Errorf("expected %q at offset %d, got %q", word, tok.pos, tok.text)
	}
	p.pos++
	return nil
}

func (p *parser) expectOp(op string) error {
	tok := p.peek()
	if tok.kind != tOp || tok.text != op {
		return fmt.Errorf("expected %q at offset %d, got %q", op, tok.pos, tok.text)
	}
	p.pos++
	return nil
}

func (p *parser) parsePolicies() ([]*Policy, error) {
	var policies []*Policy
	index := 1
	for p.peek().kind != tEOF {
		policy, err := p.parsePolicy(index)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
		index++
		if p.peek().kind == tOp && p.peek().text == ";" {
			p.pos++
		}
	}
	return policies, nil
}

func (p *parser) parsePolicy(index int) (*Policy, error) {
	effectTok := p.peek()
	var effect Effect
	switch effectTok.text {
	case "permit":
		effect = Permit
	case "forbid":
		effect = Forbid
	default:
		return nil, fmt.Errorf("expected permit or forbid at offset %d, got %q", effectTok.pos, effectTok.text)
	}
	p.pos++

	if err := p.expectIdent("principal"); err != nil {
		return nil, err
	}
	if err := p.expectIdent("action"); err != nil {
		return nil, err
	}
	if err := p.expectIdent("resource"); err != nil {
		return nil, err
	}

	policy := &Policy{
		ID:     fmt.Sprintf("policy-%d", index),
		Effect: effect,
	}

	if p.peek().kind == tIdent && p.peek().text == "when" {
		p.pos++
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		policy.When = expr
	}

	return policy, nil
}

// parseExpr: ||
func (p *parser) parseExpr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOp && p.peek().text == "||" {
		op := p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op.text, LHS: left, RHS: right}
	}
	return left, nil
}

// parseAnd: &&
func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseCompare()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOp && p.peek().text == "&&" {
		op := p.next()
		right, err := p.parseCompare()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op.text, LHS: left, RHS: right}
	}
	return left, nil
}

// parseCompare: == != < >
func (p *parser) parseCompare() (Expr, error) {
	left, err := p.parsePostfix()
	if err != nil {
		return nil, err
	}
	for {
		tok := p.peek()
		if tok.kind != tOp || (tok.text != "==" && tok.text != "!=" &&
			tok.text != "<" && tok.text != ">" && tok.text != "<=" && tok.text != ">=") {
			return left, nil
		}
		p.pos++
		right, err := p.parsePostfix()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: tok.text, LHS: left, RHS: right}
	}
}

// parsePostfix: 后缀属性访问，以及 has / in 中缀运算
func (p *parser) parsePostfix() (Expr, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	for {
		tok := p.peek()
		switch {
		case tok.kind == tOp && tok.text == ".":
			p.pos++
			name := p.peek()
			if name.kind != tIdent {
				return nil, fmt.Errorf("expected attribute name after '.' at offset %d", name.pos)
			}
			p.pos++
			expr = &AttrExpr{Target: expr, Name: name.text}
		case tok.kind == tIdent && tok.text == "has":
			p.pos++
			attr, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			expr = &HasExpr{Target: expr, Attr: attr}
		case tok.kind == tIdent && tok.text == "in":
			p.pos++
			scope, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			expr = &InExpr{Item: expr, Scope: scope}
		default:
			return expr, nil
		}
	}
}

func (p *parser) parsePrimary() (Expr, error) {
	tok := p.peek()

	if tok.kind == tOp && tok.text == "(" {
		p.pos++
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp(")"); err != nil {
			return nil, err
		}
		return expr, nil
	}

	switch tok.kind {
	case tString:
		p.pos++
		return &LiteralExpr{Value: tok.text}, nil
	case tNumber:
		p.pos++
		number, err := strconv.ParseFloat(tok.text, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q at offset %d", tok.text, tok.pos)
		}
		return &LiteralExpr{Value: number}, nil
	case tIdent:
		switch tok.text {
		case "true":
			p.pos++
			return &LiteralExpr{Value: true}, nil
		case "false":
			p.pos++
			return &LiteralExpr{Value: false}, nil
		case "principal", "action", "resource":
			p.pos++
			return &VarExpr{Name: tok.text}, nil
		}
		if keywords[tok.text] {
			return nil, fmt.Errorf("unexpected keyword %q at offset %d", tok.text, tok.pos)
		}
		return nil, fmt.Errorf("unexpected identifier %q at offset %d", tok.text, tok.pos)
	default:
		if tok.kind == tOp && tok.text == "[" {
			return p.parseSet()
		}
		return nil, fmt.Errorf("unexpected token %q at offset %d", tok.text, tok.pos)
	}
}

func (p *parser) parseSet() (Expr, error) {
	p.pos++ // 消费 '['
	set := &SetExpr{}
	if p.peek().kind == tOp && p.peek().text == "]" {
		p.pos++
		return set, nil
	}
	for {
		element, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		set.Elements = append(set.Elements, element)
		tok := p.peek()
		if tok.kind == tOp && tok.text == "," {
			p.pos++
			continue
		}
		if tok.kind == tOp && tok.text == "]" {
			p.pos++
			return set, nil
		}
		return nil, fmt.Errorf("expected ',' or ']' in set at offset %d", tok.pos)
	}
}
