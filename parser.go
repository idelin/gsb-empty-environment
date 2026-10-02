package engine

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// node is a compiled formula AST.
type node interface {
	eval(lookup func(string) (float64, error)) (float64, error)
	collectRefs(into map[string]struct{})
}

type numNode struct{ val float64 }

func (n numNode) eval(func(string) (float64, error)) (float64, error) { return n.val, nil }
func (n numNode) collectRefs(map[string]struct{})                     {}

type refNode struct{ name string }

func (n refNode) eval(lookup func(string) (float64, error)) (float64, error) {
	return lookup(n.name)
}
func (n refNode) collectRefs(into map[string]struct{}) { into[n.name] = struct{}{} }

type negNode struct{ x node }

func (n negNode) eval(lookup func(string) (float64, error)) (float64, error) {
	v, err := n.x.eval(lookup)
	return -v, err
}
func (n negNode) collectRefs(into map[string]struct{}) { n.x.collectRefs(into) }

type binNode struct {
	op   byte
	l, r node
}

func (n binNode) eval(lookup func(string) (float64, error)) (float64, error) {
	lv, err := n.l.eval(lookup)
	if err != nil {
		return 0, err
	}
	rv, err := n.r.eval(lookup)
	if err != nil {
		return 0, err
	}
	switch n.op {
	case '+':
		return lv + rv, nil
	case '-':
		return lv - rv, nil
	case '*':
		return lv * rv, nil
	case '/':
		if rv == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		return lv / rv, nil
	}
	return 0, fmt.Errorf("unknown operator %q", string(n.op))
}
func (n binNode) collectRefs(into map[string]struct{}) {
	n.l.collectRefs(into)
	n.r.collectRefs(into)
}

type tokKind int

const (
	tokNum tokKind = iota
	tokRef
	tokOp
	tokLParen
	tokRParen
	tokEOF
)

type token struct {
	kind tokKind
	num  float64
	text string
}

func lex(s string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case (c >= '0' && c <= '9') || c == '.':
			j := i
			for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == '.') {
				j++
			}
			f, err := strconv.ParseFloat(s[i:j], 64)
			if err != nil {
				return nil, fmt.Errorf("invalid number %q", s[i:j])
			}
			toks = append(toks, token{kind: tokNum, num: f})
			i = j
		case unicode.IsLetter(rune(c)):
			j := i
			for j < len(s) && (unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j]))) {
				j++
			}
			name := strings.ToUpper(s[i:j])
			if !validCellName(name) {
				return nil, fmt.Errorf("invalid cell reference %q", s[i:j])
			}
			toks = append(toks, token{kind: tokRef, text: name})
			i = j
		case c == '+' || c == '-' || c == '*' || c == '/':
			toks = append(toks, token{kind: tokOp, text: string(c)})
			i++
		case c == '(':
			toks = append(toks, token{kind: tokLParen})
			i++
		case c == ')':
			toks = append(toks, token{kind: tokRParen})
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q", string(c))
		}
	}
	return append(toks, token{kind: tokEOF}), nil
}

// validCellName reports whether s looks like a spreadsheet reference
// such as A1, BC12 (one or more letters followed by one or more digits).
func validCellName(s string) bool {
	i := 0
	for i < len(s) && unicode.IsLetter(rune(s[i])) {
		i++
	}
	if i == 0 || i == len(s) {
		return false
	}
	for ; i < len(s); i++ {
		if !unicode.IsDigit(rune(s[i])) {
			return false
		}
	}
	return true
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) parseExpr() (node, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokOp && (p.peek().text == "+" || p.peek().text == "-") {
		op := p.next().text[0]
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = binNode{op: op, l: left, r: right}
	}
	return left, nil
}

func (p *parser) parseTerm() (node, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokOp && (p.peek().text == "*" || p.peek().text == "/") {
		op := p.next().text[0]
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		left = binNode{op: op, l: left, r: right}
	}
	return left, nil
}

func (p *parser) parseFactor() (node, error) {
	t := p.peek()
	switch t.kind {
	case tokNum:
		p.next()
		return numNode{val: t.num}, nil
	case tokRef:
		p.next()
		return refNode{name: t.text}, nil
	case tokLParen:
		p.next()
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		p.next()
		return inner, nil
	case tokOp:
		if t.text == "-" {
			p.next()
			x, err := p.parseFactor()
			if err != nil {
				return nil, err
			}
			return negNode{x: x}, nil
		}
		if t.text == "+" {
			p.next()
			return p.parseFactor()
		}
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}

// compileFormula parses a formula (with optional leading '=') into an AST
// and returns the AST plus the sorted list of referenced cell names.
func compileFormula(formula string) (node, []string, error) {
	s := strings.TrimSpace(formula)
	s = strings.TrimPrefix(s, "=")
	if s == "" {
		return nil, nil, fmt.Errorf("empty formula")
	}
	toks, err := lex(s)
	if err != nil {
		return nil, nil, err
	}
	p := &parser{toks: toks}
	ast, err := p.parseExpr()
	if err != nil {
		return nil, nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, nil, fmt.Errorf("unexpected trailing input")
	}
	set := map[string]struct{}{}
	ast.collectRefs(set)
	refs := make([]string, 0, len(set))
	for r := range set {
		refs = append(refs, r)
	}
	sortStrings(refs)
	return ast, refs, nil
}
