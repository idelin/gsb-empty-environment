package capgate

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Assignment is one concrete platform/feature combination.
// Missing features are treated as false.
type Assignment map[string]bool

func (a Assignment) value(name string) bool {
	if a == nil {
		return false
	}
	return a[name]
}

func (a Assignment) sortedKeys() []string {
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Clone returns an independent copy.
func (a Assignment) Clone() Assignment {
	out := make(Assignment, len(a))
	for k, v := range a {
		out[k] = v
	}
	return out
}

// Expr is a compiled Boolean platform/feature condition.
type Expr interface {
	Eval(Assignment) bool
	String() string
	variables() []string
}

type varExpr string

func (v varExpr) Eval(a Assignment) bool { return a.value(string(v)) }
func (v varExpr) String() string         { return string(v) }
func (v varExpr) variables() []string    { return []string{string(v)} }

type notExpr struct{ x Expr }

func (n notExpr) Eval(a Assignment) bool { return !n.x.Eval(a) }
func (n notExpr) String() string         { return "!" + parenthesizeNot(n.x) }
func (n notExpr) variables() []string    { return n.x.variables() }

func parenthesizeNot(e Expr) string {
	switch e.(type) {
	case varExpr, notExpr:
		return e.String()
	default:
		return "(" + e.String() + ")"
	}
}

type andExpr struct{ items []Expr }

func (a andExpr) Eval(as Assignment) bool {
	for _, x := range a.items {
		if !x.Eval(as) {
			return false
		}
	}
	return true
}

func (a andExpr) String() string {
	parts := make([]string, 0, len(a.items))
	for _, x := range a.items {
		if _, ok := x.(orExpr); ok {
			parts = append(parts, "("+x.String()+")")
		} else {
			parts = append(parts, x.String())
		}
	}
	return strings.Join(parts, " && ")
}

func (a andExpr) variables() []string { return collectVars(a.items) }

type orExpr struct{ items []Expr }

func (o orExpr) Eval(a Assignment) bool {
	for _, x := range o.items {
		if x.Eval(a) {
			return true
		}
	}
	return false
}

func (o orExpr) String() string {
	parts := make([]string, 0, len(o.items))
	for _, x := range o.items {
		parts = append(parts, x.String())
	}
	return strings.Join(parts, " || ")
}

func (o orExpr) variables() []string { return collectVars(o.items) }

func collectVars(items []Expr) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, item := range items {
		for _, v := range item.variables() {
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// ParseExpr parses a small Boolean expression language:
//
//	condition := or
//	or        := and ("||" and)*
//	and       := unary ("&&" unary)*
//	unary     := "!" unary | "(" or ")" | feature
//
// Feature names may contain letters, digits, '_', '.', ':', '=', and '-'.
func ParseExpr(input string) (Expr, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return nil, err
	}
	p := &exprParser{tokens: tokens}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if !p.atEnd() {
		return nil, fmt.Errorf("capgate: unexpected token %q at position %d", p.peek().text, p.peek().pos)
	}
	return e, nil
}

type tokenKind int

const (
	tokIdent tokenKind = iota
	tokAnd
	tokOr
	tokNot
	tokLParen
	tokRParen
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
		if unicode.IsSpace(r) {
			i++
			continue
		}
		pos := i
		switch {
		case r == '!' && (i+1 >= len(runes) || runes[i+1] != '='):
			tokens = append(tokens, token{tokNot, "!", pos})
			i++
		case r == '(':
			tokens = append(tokens, token{tokLParen, "(", pos})
			i++
		case r == ')':
			tokens = append(tokens, token{tokRParen, ")", pos})
			i++
		case r == '&' && i+1 < len(runes) && runes[i+1] == '&':
			tokens = append(tokens, token{tokAnd, "&&", pos})
			i += 2
		case r == '|' && i+1 < len(runes) && runes[i+1] == '|':
			tokens = append(tokens, token{tokOr, "||", pos})
			i += 2
		case isIdentStart(r):
			j := i + 1
			for j < len(runes) && isIdentPart(runes[j]) {
				j++
			}
			text := string(runes[i:j])
			tokens = append(tokens, token{tokIdent, text, pos})
			i = j
		default:
			return nil, fmt.Errorf("capgate: invalid condition character %q at position %d", r, pos)
		}
	}
	return tokens, nil
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r) || strings.ContainsRune(".:=-", r)
}

type exprParser struct {
	tokens []token
	pos    int
}

func (p *exprParser) peek() token {
	if p.pos >= len(p.tokens) {
		return token{}
	}
	return p.tokens[p.pos]
}

func (p *exprParser) atEnd() bool { return p.pos >= len(p.tokens) }

func (p *exprParser) take() token {
	t := p.peek()
	p.pos++
	return t
}

func (p *exprParser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	var items []Expr
	for p.peek().kind == tokOr {
		p.take()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			items = []Expr{left}
		}
		items = append(items, right)
	}
	if len(items) > 0 {
		return orExpr{items}, nil
	}
	return left, nil
}

func (p *exprParser) parseAnd() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	var items []Expr
	for p.peek().kind == tokAnd {
		p.take()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			items = []Expr{left}
		}
		items = append(items, right)
	}
	if len(items) > 0 {
		return andExpr{items}, nil
	}
	return left, nil
}

func (p *exprParser) parseUnary() (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tokNot:
		p.take()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return notExpr{x}, nil
	case tokLParen:
		p.take()
		x, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, fmt.Errorf("capgate: missing closing parenthesis near position %d", p.peek().pos)
		}
		p.take()
		return x, nil
	case tokIdent:
		p.take()
		return varExpr(t.text), nil
	default:
		if t.text == "" {
			return nil, fmt.Errorf("capgate: expected condition term at end of expression")
		}
		return nil, fmt.Errorf("capgate: unexpected token %q at position %d", t.text, t.pos)
	}
}

func compileCondition(source string) (Expr, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return boolExpr(true), nil
	}
	return ParseExpr(source)
}

type boolExpr bool

func (b boolExpr) Eval(Assignment) bool { return bool(b) }
func (b boolExpr) String() string {
	if b {
		return "true"
	}
	return "false"
}
func (b boolExpr) variables() []string { return nil }

// OneHot returns a legal-condition fragment requiring exactly one named feature.
func OneHot(features ...string) string {
	if len(features) == 0 {
		return "true"
	}
	var atLeastOne strings.Builder
	atLeastOne.WriteString("(")
	for i, f := range features {
		if i > 0 {
			atLeastOne.WriteString(" || ")
		}
		atLeastOne.WriteString(f)
	}
	atLeastOne.WriteString(")")

	var atMostOne []string
	for i := 0; i < len(features); i++ {
		for j := i + 1; j < len(features); j++ {
			atMostOne = append(atMostOne, "(!"+features[i]+" || !"+features[j]+")")
		}
	}
	if len(atMostOne) == 0 {
		return atLeastOne.String()
	}
	return atLeastOne.String() + " && " + strings.Join(atMostOne, " && ")
}
