package bdd

import (
	"fmt"
	"strings"
	"unicode"
)

// Parse parses a boolean expression over variable names. Variable names are
// either bare identifiers (mapped through names) or an explicit "N<index>"
// reference such as N3. Supported grammar:
//
//	expr   := or
//	or     := and ("|" and | "or" and)*
//	and    := unary ("&" unary | "and" unary)*
//	unary  := "!" unary | "not" unary | atom
//	atom   := "(" expr ")" | "true" | "false" | "1" | "0" | name | "N" digits
//
// The empty string denotes a tautology. All generated nodes follow the
// manager's variable order.
func (m *Manager) Parse(expression string, names map[string]int) (BDD, error) {
	p := &parser{
		m:      m,
		names:  names,
		tokens: tokenize(expression),
	}
	if len(p.tokens) == 0 {
		return One, nil
	}
	root, err := p.parseOr()
	if err != nil {
		return Zero, err
	}
	if p.position != len(p.tokens) {
		return Zero, fmt.Errorf("bdd: unexpected token %q", p.tokens[p.position])
	}
	return root, nil
}

type token struct {
	kind  string
	value string
}

func tokenize(expression string) []token {
	var tokens []token
	runes := []rune(expression)
	for i := 0; i < len(runes); {
		ch := runes[i]
		switch {
		case unicode.IsSpace(ch):
			i++
		case ch == '(' || ch == ')' || ch == '&' || ch == '|' || ch == '!':
			tokens = append(tokens, token{kind: string(ch), value: string(ch)})
			i++
		case unicode.IsLetter(ch) || ch == '_' || unicode.IsDigit(ch):
			start := i
			for i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_') {
				i++
			}
			tokens = append(tokens, token{kind: "word", value: string(runes[start:i])})
		default:
			i++
		}
	}
	return tokens
}

type parser struct {
	m        *Manager
	names    map[string]int
	tokens   []token
	position int
}

func (p *parser) peek() (token, bool) {
	if p.position >= len(p.tokens) {
		return token{}, false
	}
	return p.tokens[p.position], true
}

func (p *parser) consume() token {
	t := p.tokens[p.position]
	p.position++
	return t
}

func (p *parser) parseOr() (BDD, error) {
	left, err := p.parseAnd()
	if err != nil {
		return Zero, err
	}
	for {
		t, ok := p.peek()
		if !ok || (t.kind != "|" && !strings.EqualFold(t.value, "or")) {
			return left, nil
		}
		p.consume()
		right, err := p.parseAnd()
		if err != nil {
			return Zero, err
		}
		left = p.m.Or(left, right)
	}
}

func (p *parser) parseAnd() (BDD, error) {
	left, err := p.parseUnary()
	if err != nil {
		return Zero, err
	}
	for {
		t, ok := p.peek()
		if !ok || (t.kind != "&" && !strings.EqualFold(t.value, "and")) {
			return left, nil
		}
		p.consume()
		right, err := p.parseUnary()
		if err != nil {
			return Zero, err
		}
		left = p.m.And(left, right)
	}
}

func (p *parser) parseUnary() (BDD, error) {
	t, ok := p.peek()
	if !ok {
		return Zero, fmt.Errorf("bdd: expected operand")
	}
	if t.kind == "!" || strings.EqualFold(t.value, "not") {
		p.consume()
		inner, err := p.parseUnary()
		if err != nil {
			return Zero, err
		}
		return p.m.Not(inner), nil
	}
	return p.parseAtom()
}

func (p *parser) parseAtom() (BDD, error) {
	t, ok := p.peek()
	if !ok {
		return Zero, fmt.Errorf("bdd: expected operand")
	}
	switch {
	case t.kind == "(":
		p.consume()
		inner, err := p.parseOr()
		if err != nil {
			return Zero, err
		}
		closing, ok := p.peek()
		if !ok || closing.kind != ")" {
			return Zero, fmt.Errorf("bdd: expected closing parenthesis")
		}
		p.consume()
		return inner, nil
	case t.kind == "word":
		p.consume()
		return p.wordToBDD(t.value)
	default:
		return Zero, fmt.Errorf("bdd: unexpected token %q", t.value)
	}
}

func (p *parser) wordToBDD(word string) (BDD, error) {
	switch strings.ToLower(word) {
	case "true", "1":
		return One, nil
	case "false", "0":
		return Zero, nil
	}
	var index int
	if len(word) > 1 && (word[0] == 'N' || word[0] == 'n') && isAllDigits(word[1:]) {
		for _, r := range word[1:] {
			index = index*10 + int(r-'0')
		}
	} else {
		mapped, ok := p.names[word]
		if !ok {
			return Zero, fmt.Errorf("bdd: unknown variable name %q", word)
		}
		index = mapped
	}
	return p.m.Variable(index)
}

func isAllDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
