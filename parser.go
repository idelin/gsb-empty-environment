package netsec

import "fmt"

// parser 在词法单元流上做递归下降解析。
type parser struct {
	tokens []token
	pos    int
}

func (p *parser) peek() token { return p.tokens[p.pos] }

func (p *parser) next() token {
	t := p.tokens[p.pos]
	if t.kind != tkEOF {
		p.pos++
	}
	return t
}

func (p *parser) skipTerminators() {
	for p.peek().kind == tkNewline || p.peek().kind == tkSemicolon {
		p.next()
	}
}

func (p *parser) expectTerminator() error {
	t := p.peek()
	if t.kind == tkNewline || t.kind == tkSemicolon || t.kind == tkEOF {
		p.skipTerminators()
		return nil
	}
	return fmt.Errorf("%w: line %d: expected end of statement, got %q", ErrParse, t.line, t.text)
}

func (p *parser) parseTerm() (Term, error) {
	t := p.next()
	switch t.kind {
	case tkVariable:
		return Var(t.text), nil
	case tkIdent:
		return Symbol(t.text), nil
	case tkString:
		return String(t.text), nil
	case tkInteger:
		return Integer(t.text), nil
	default:
		return Term{}, fmt.Errorf("%w: line %d: expected term, got %q", ErrParse, t.line, t.text)
	}
}

func (p *parser) parsePredicate() (Predicate, error) {
	nameTok := p.next()
	if nameTok.kind != tkIdent {
		return Predicate{}, fmt.Errorf("%w: line %d: expected predicate name, got %q", ErrParse, nameTok.line, nameTok.text)
	}
	pred := Predicate{Name: nameTok.text}
	if p.peek().kind != tkLParen {
		return pred, nil
	}
	p.next() // '('
	if p.peek().kind == tkRParen {
		p.next()
		return pred, nil
	}
	for {
		term, err := p.parseTerm()
		if err != nil {
			return Predicate{}, err
		}
		pred.Args = append(pred.Args, term)
		switch p.peek().kind {
		case tkComma:
			p.next()
			continue
		case tkRParen:
			p.next()
			return pred, nil
		default:
			t := p.peek()
			return Predicate{}, fmt.Errorf("%w: line %d: expected ',' or ')' in predicate, got %q", ErrParse, t.line, t.text)
		}
	}
}

func (p *parser) parseBody() ([]Predicate, error) {
	var body []Predicate
	for {
		pred, err := p.parsePredicate()
		if err != nil {
			return nil, err
		}
		body = append(body, pred)
		if p.peek().kind != tkComma {
			break
		}
		p.next()
	}
	return body, nil
}

func (p *parser) parseFactOrRule(headLine int) (Rule, error) {
	head, err := p.parsePredicate()
	if err != nil {
		return Rule{}, err
	}
	if p.peek().kind != tkColonDash {
		return Rule{Head: head}, nil
	}
	p.next() // :-
	body, err := p.parseBody()
	if err != nil {
		return Rule{}, err
	}
	return Rule{Head: head, Body: body}, nil
}

func (p *parser) parseCheck() ([]Predicate, error) {
	checkTok := p.next() // check
	ifTok := p.next()
	if ifTok.kind != tkIdent || ifTok.text != "if" {
		return nil, fmt.Errorf("%w: line %d: expected 'if' after 'check'", ErrParse, checkTok.line)
	}
	return p.parseBody()
}

func (p *parser) parsePolicy() (Policy, error) {
	kindTok := p.next() // allow / deny
	ifTok := p.next()
	if ifTok.kind != tkIdent || ifTok.text != "if" {
		return Policy{}, fmt.Errorf("%w: line %d: expected 'if' after %q", ErrParse, kindTok.line, kindTok.text)
	}
	body, err := p.parseBody()
	if err != nil {
		return Policy{}, err
	}
	kind := PolicyAllow
	if kindTok.text == "deny" {
		kind = PolicyDeny
	}
	return Policy{Kind: kind, Body: body}, nil
}
