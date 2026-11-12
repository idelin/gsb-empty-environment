// Package spdx parses and evaluates SPDX license expressions (SPDX 2.x
// appendix IV syntax): license identifiers joined by AND/OR, optionally
// carrying WITH exceptions and grouped by parentheses.
package spdx

import (
	"fmt"
	"strings"
	"unicode"
)

// Expr is a parsed SPDX license expression.
type Expr interface {
	// Allowed reports whether the expression is satisfied under the policy:
	// every AND branch must be allowed, any OR branch may be allowed, and a
	// WITH clause is allowed only when the full id-with-exception token (or
	// the base id) is in the allowed set.
	Allowed(allowed map[string]bool) bool

	// String renders the canonical, parenthesized form of the expression.
	String() string
}

// License is a terminal SPDX identifier, or a non-SPDX license name used as
// a bare token.
type License struct {
	ID string
}

func (l License) Allowed(allowed map[string]bool) bool {
	return allowed[normalize(l.ID)]
}

func (l License) String() string { return l.ID }

// With models "id WITH exception".
type With struct {
	License   Expr
	Exception string
}

func (w With) Allowed(allowed map[string]bool) bool {
	// The exception can change obligations, so the gate may list either the
	// exact "id WITH exception" grant or the base license id.
	token := normalize(fmt.Sprintf("%s WITH %s", exprToken(w.License), w.Exception))
	return allowed[token] || w.License.Allowed(allowed)
}

func (w With) String() string {
	return exprToken(w.License) + " WITH " + w.Exception
}

// And requires both sides to be allowed.
type And struct {
	Left, Right Expr
}

func (a And) Allowed(allowed map[string]bool) bool {
	return a.Left.Allowed(allowed) && a.Right.Allowed(allowed)
}

func (a And) String() string {
	return "(" + a.Left.String() + " AND " + a.Right.String() + ")"
}

// Or requires at least one side to be allowed.
type Or struct {
	Left, Right Expr
}

func (o Or) Allowed(allowed map[string]bool) bool {
	return o.Left.Allowed(allowed) || o.Right.Allowed(allowed)
}

func (o Or) String() string {
	return "(" + o.Left.String() + " OR " + o.Right.String() + ")"
}

func exprToken(e Expr) string {
	if l, ok := e.(License); ok {
		return l.ID
	}
	return e.String()
}

func normalize(token string) string {
	return strings.ToLower(strings.Join(strings.Fields(token), " "))
}

type tokenKind int

const (
	tkWord tokenKind = iota
	tkLParen
	tkRParen
	tkAnd
	tkOr
	tkWith
)

type token struct {
	kind tokenKind
	text string
	pos  int
}

func tokenize(input string) ([]token, error) {
	var tokens []token
	runes := []rune(input)
	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case unicode.IsSpace(r):
			i++
		case r == '(':
			tokens = append(tokens, token{tkLParen, "(", i})
			i++
		case r == ')':
			tokens = append(tokens, token{tkRParen, ")", i})
			i++
		default:
			start := i
			for i < len(runes) && !unicode.IsSpace(runes[i]) && runes[i] != '(' && runes[i] != ')' {
				i++
			}
			word := string(runes[start:i])
			switch strings.ToUpper(word) {
			case "AND":
				tokens = append(tokens, token{tkAnd, word, start})
			case "OR":
				tokens = append(tokens, token{tkOr, word, start})
			case "WITH":
				tokens = append(tokens, token{tkWith, word, start})
			default:
				tokens = append(tokens, token{tkWord, word, start})
			}
		}
	}
	return tokens, nil
}

type parser struct {
	tokens []token
	pos    int
}

// Parse parses an SPDX license expression. Grammar (OR lowest precedence,
// WITH binds tightest to its left-hand identifier):
//
//	or    := and (OR and)*
//	and   := with (AND with)*
//	with  := primary (WITH word)?
//	primary := word | "(" or ")"
func Parse(input string) (Expr, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("spdx: empty license expression")
	}
	p := &parser{tokens: tokens}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.tokens) {
		return nil, fmt.Errorf("spdx: unexpected token %q at position %d",
			p.tokens[p.pos].text, p.tokens[p.pos].pos)
	}
	return expr, nil
}

func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.accept(tkOr) {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = Or{Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseWith()
	if err != nil {
		return nil, err
	}
	for p.accept(tkAnd) {
		right, err := p.parseWith()
		if err != nil {
			return nil, err
		}
		left = And{Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseWith() (Expr, error) {
	base, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if p.accept(tkWith) {
		tok, err := p.expectWord()
		if err != nil {
			return nil, err
		}
		base = With{License: base, Exception: tok.text}
	}
	return base, nil
}

func (p *parser) parsePrimary() (Expr, error) {
	if p.accept(tkLParen) {
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tkRParen); err != nil {
			return nil, fmt.Errorf("spdx: missing closing parenthesis")
		}
		return inner, nil
	}
	tok, err := p.expectWord()
	if err != nil {
		return nil, err
	}
	return License{ID: tok.text}, nil
}

func (p *parser) accept(kind tokenKind) bool {
	if p.pos < len(p.tokens) && p.tokens[p.pos].kind == kind {
		p.pos++
		return true
	}
	return false
}

func (p *parser) expect(kind tokenKind) (token, error) {
	if p.pos >= len(p.tokens) {
		return token{}, fmt.Errorf("spdx: unexpected end of expression, want token kind %d", kind)
	}
	tok := p.tokens[p.pos]
	if tok.kind != kind {
		return token{}, fmt.Errorf("spdx: unexpected token %q at position %d", tok.text, tok.pos)
	}
	p.pos++
	return tok, nil
}

func (p *parser) expectWord() (token, error) {
	if p.pos >= len(p.tokens) {
		return token{}, fmt.Errorf("spdx: unexpected end of expression, want license identifier")
	}
	tok := p.tokens[p.pos]
	if tok.kind != tkWord {
		return token{}, fmt.Errorf("spdx: unexpected operator %q at position %d, want license identifier",
			tok.text, tok.pos)
	}
	p.pos++
	return tok, nil
}
