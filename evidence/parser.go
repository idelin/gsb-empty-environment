package evidence

import (
	"fmt"
	"unicode"
)

// ParseRule compiles a release rule.
//
// Grammar, from lowest to highest precedence:
//
//	or    := and ("or" and)*
//	and   := unary ("and" unary)*
//	unary := "not" unary | primary
//	primary := identifier | "(" or ")"
//
// Identifiers contain letters, digits, '_', '-', '.' and ':' but must start
// with a letter or underscore. The keywords not, and and or are reserved.
func ParseRule(text string) (Expr, error) {
	tokens, err := lex(text)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("evidence: empty rule")
	}
	p := &parser{tokens: tokens}
	expr := p.parseOr()
	if p.err != nil {
		return nil, p.err
	}
	if p.pos != len(p.tokens) {
		return nil, p.syntaxError("unexpected token %q", p.tokens[p.pos].text)
	}
	return expr, nil
}

type tokenKind uint8

const (
	tokenIdent tokenKind = iota
	tokenLParen
	tokenRParen
)

type token struct {
	kind tokenKind
	text string
	pos  int
}

type parser struct {
	tokens []token
	pos    int
	err    error
}

func (p *parser) syntaxError(format string, args ...any) error {
	return fmt.Errorf("evidence: invalid rule: "+format, args...)
}

func (p *parser) parseOr() Expr {
	left := p.parseAnd()
	if p.err != nil || left == nil {
		return left
	}
	var operands []Expr
	for p.acceptKeyword("or") {
		if len(operands) == 0 {
			operands = append(operands, left)
		}
		right := p.parseAnd()
		if p.err != nil {
			return nil
		}
		operands = append(operands, right)
	}
	if len(operands) > 0 {
		return &OrExpr{Operands: operands}
	}
	return left
}

func (p *parser) parseAnd() Expr {
	left := p.parseUnary()
	if p.err != nil || left == nil {
		return left
	}
	var operands []Expr
	for p.acceptKeyword("and") {
		if len(operands) == 0 {
			operands = append(operands, left)
		}
		right := p.parseUnary()
		if p.err != nil {
			return nil
		}
		operands = append(operands, right)
	}
	if len(operands) > 0 {
		return &AndExpr{Operands: operands}
	}
	return left
}

func (p *parser) parseUnary() Expr {
	if p.acceptKeyword("not") {
		inner := p.parseUnary()
		if p.err != nil {
			return nil
		}
		if inner == nil {
			p.err = p.syntaxError("missing operand after not")
			return nil
		}
		return &NotExpr{Inner: inner}
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() Expr {
	current, ok := p.peek()
	if !ok {
		p.err = p.syntaxError("unexpected end of rule")
		return nil
	}
	if current.text == "(" {
		p.pos++
		inner := p.parseOr()
		if p.err != nil {
			return nil
		}
		closing, ok := p.peek()
		if !ok || closing.text != ")" {
			p.err = p.syntaxError("missing closing parenthesis")
			return nil
		}
		p.pos++
		return inner
	}
	if current.kind != tokenIdent {
		p.err = p.syntaxError("unexpected token %q", current.text)
		return nil
	}
	if isKeyword(current.text) {
		p.err = p.syntaxError("reserved keyword %q cannot be a fact name", current.text)
		return nil
	}
	p.pos++
	return &Atom{Name: current.text}
}

func isKeyword(text string) bool {
	return text == "not" || text == "and" || text == "or"
}

func (p *parser) peek() (token, bool) {
	if p.pos >= len(p.tokens) {
		return token{}, false
	}
	return p.tokens[p.pos], true
}

func (p *parser) acceptKeyword(keyword string) bool {
	current, ok := p.peek()
	if !ok || current.kind != tokenIdent || current.text != keyword {
		return false
	}
	p.pos++
	return true
}

func lex(text string) ([]token, error) {
	var tokens []token
	runes := []rune(text)
	for i := 0; i < len(runes); {
		r := runes[i]
		if unicode.IsSpace(r) {
			i++
			continue
		}
		if r == '(' {
			tokens = append(tokens, token{kind: tokenLParen, text: "(", pos: i})
			i++
			continue
		}
		if r == ')' {
			tokens = append(tokens, token{kind: tokenRParen, text: ")", pos: i})
			i++
			continue
		}
		if isIdentStart(r) {
			start := i
			for i < len(runes) && isIdentPart(runes[i]) {
				i++
			}
			tokens = append(tokens, token{
				kind: tokenIdent,
				text: string(runes[start:i]),
				pos:  start,
			})
			continue
		}
		return nil, fmt.Errorf("evidence: invalid rule: unexpected character %q at offset %d", r, i)
	}
	return tokens, nil
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r) ||
		r == '-' || r == '.' || r == ':'
}
