package policysafe

import "fmt"

type parser struct {
	tokens []token
	pos    int
}

// Parse compiles source text into a Program.
//
// The supported surface syntax is a Datalog subset:
//
//	pred(const1, const2).                    // ground fact
//	head(X) :- body1(X), not body2(X).      // derivation rule with negation
//	default allow := false.                 // explicit fail-closed default
//
// Identifiers starting with an uppercase letter or '_' are variables;
// lowercase identifiers, numbers and quoted strings are constants.
// '#' and '//' introduce line comments.
func Parse(source string) (*Program, error) {
	tokens, err := tokenize(source)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	prog := &Program{}
	for p.peek().kind != tkEOF {
		if err := p.parseDecl(prog); err != nil {
			return nil, err
		}
	}
	return prog, nil
}

// MustParse parses source and panics on error. It is intended for static
// test fixtures and package examples.
func MustParse(source string) *Program {
	prog, err := Parse(source)
	if err != nil {
		panic(err)
	}
	return prog
}

func (p *parser) parseDecl(prog *Program) error {
	tok := p.peek()
	if tok.kind == tkIdent && tok.text == "default" {
		return p.parseDefault(prog)
	}
	head, err := p.parseAtom()
	if err != nil {
		return err
	}
	if p.peek().kind == tkDot {
		p.next()
		prog.Facts = append(prog.Facts, head)
		return nil
	}
	if err := p.expect(tkColonDash, "expected ':-' or '.' after rule head"); err != nil {
		return err
	}
	body, err := p.parseBody()
	if err != nil {
		return err
	}
	if err := p.expect(tkDot, "expected '.' to terminate rule"); err != nil {
		return err
	}
	prog.Rules = append(prog.Rules, Rule{Head: head, Body: body})
	return nil
}

func (p *parser) parseDefault(prog *Program) error {
	defaultTok := p.next() // 'default'
	predTok := p.peek()
	if predTok.kind != tkIdent {
		return p.parseError(defaultTok, "expected predicate name after 'default'")
	}
	p.next()
	if err := p.expect(tkColonEq, "expected ':=' in default declaration"); err != nil {
		return err
	}
	valueTok := p.peek()
	if valueTok.kind != tkIdent || (valueTok.text != "false" && valueTok.text != "true") {
		return p.parseError(valueTok, "expected boolean literal 'true' or 'false'")
	}
	p.next()
	if err := p.expect(tkDot, "expected '.' to terminate default declaration"); err != nil {
		return err
	}
	prog.Defaults = append(prog.Defaults, DefaultDecl{Pred: predTok.text})
	return nil
}

func (p *parser) parseBody() ([]Literal, error) {
	var body []Literal
	for {
		negated := false
		if tok := p.peek(); tok.kind == tkIdent && tok.text == "not" {
			negated = true
			p.next()
		}
		atom, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		body = append(body, Literal{Neg: negated, Atom: atom})
		if p.peek().kind != tkComma {
			break
		}
		p.next()
	}
	return body, nil
}

func (p *parser) parseAtom() (Atom, error) {
	tok := p.peek()
	if tok.kind != tkIdent {
		return Atom{}, p.parseError(tok, "expected predicate name (lowercase identifier)")
	}
	p.next()
	atom := Atom{Pred: tok.text}
	if p.peek().kind != tkLParen {
		return atom, nil
	}
	p.next()
	if p.peek().kind == tkRParen {
		p.next()
		return atom, nil
	}
	for {
		term, err := p.parseTerm()
		if err != nil {
			return Atom{}, err
		}
		atom.Args = append(atom.Args, term)
		if p.peek().kind != tkComma {
			break
		}
		p.next()
	}
	if err := p.expect(tkRParen, "expected ')' to close atom argument list"); err != nil {
		return Atom{}, err
	}
	return atom, nil
}

func (p *parser) parseTerm() (Term, error) {
	tok := p.peek()
	switch tok.kind {
	case tkVar:
		p.next()
		if tok.text == "_" {
			return &Wildcard{}, nil
		}
		return &Var{Name: tok.text}, nil
	case tkIdent:
		p.next()
		return &Const{Value: tok.text}, nil
	case tkNumber, tkString:
		p.next()
		return &Const{Value: tok.text}, nil
	default:
		return nil, p.parseError(tok, "expected variable or constant term")
	}
}

func (p *parser) peek() token {
	return p.tokens[p.pos]
}

func (p *parser) next() token {
	tok := p.tokens[p.pos]
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return tok
}

func (p *parser) expect(kind tokenKind, message string) error {
	tok := p.peek()
	if tok.kind != kind {
		return p.parseError(tok, message)
	}
	p.next()
	return nil
}

func (p *parser) parseError(tok token, message string) error {
	return fmt.Errorf("policysafe: line %d col %d: %s (got %q)", tok.line, tok.col, message, tok.text)
}
