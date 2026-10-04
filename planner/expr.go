package planner

import (
	"fmt"
	"sort"
	"strings"
)

// NodeKind classifies expression tree nodes.
type NodeKind int

const (
	KindEvidence NodeKind = iota
	KindAnd
	KindOr
	KindNot
)

// Node is a boolean expression tree node.
type Node struct {
	Kind     NodeKind
	ID       string  // set when Kind == KindEvidence
	Children []*Node // AND/OR: any number; NOT: exactly one
}

// ReferencedEvidence returns the sorted set of evidence ids used in the tree.
func ReferencedEvidence(n *Node) []string {
	set := make(map[string]bool)
	var walk func(*Node)
	walk = func(cur *Node) {
		if cur.Kind == KindEvidence {
			set[cur.ID] = true
		}
		for _, ch := range cur.Children {
			walk(ch)
		}
	}
	walk(n)
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// String renders the expression tree in canonical form.
func (n *Node) String() string {
	switch n.Kind {
	case KindEvidence:
		return n.ID
	case KindNot:
		return "NOT " + n.Children[0].String()
	case KindAnd, KindOr:
		op := " AND "
		if n.Kind == KindOr {
			op = " OR "
		}
		parts := make([]string, len(n.Children))
		for i, ch := range n.Children {
			s := ch.String()
			if ch.Kind == KindOr && n.Kind == KindAnd {
				s = "(" + s + ")"
			}
			parts[i] = s
		}
		return strings.Join(parts, op)
	}
	return "?"
}

type token struct {
	kind string // "ident", "and", "or", "not", "lparen", "rparen"
	text string
}

func tokenize(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, token{kind: "lparen", text: "("})
			i++
		case c == ')':
			toks = append(toks, token{kind: "rparen", text: ")"})
			i++
		case c == '&':
			if i+1 < len(src) && src[i+1] == '&' {
				i++
			}
			toks = append(toks, token{kind: "and", text: "AND"})
			i++
		case c == '|':
			if i+1 < len(src) && src[i+1] == '|' {
				i++
			}
			toks = append(toks, token{kind: "or", text: "OR"})
			i++
		case c == '!' || c == '~':
			toks = append(toks, token{kind: "not", text: "NOT"})
			i++
		case c == ',':
			toks = append(toks, token{kind: "and", text: "AND"})
			i++
		case isIdentChar(c):
			j := i
			for j < len(src) && isIdentChar(src[j]) {
				j++
			}
			word := src[i:j]
			switch strings.ToUpper(word) {
			case "AND":
				toks = append(toks, token{kind: "and", text: word})
			case "OR":
				toks = append(toks, token{kind: "or", text: word})
			case "NOT":
				toks = append(toks, token{kind: "not", text: word})
			default:
				toks = append(toks, token{kind: "ident", text: word})
			}
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q in expression", string(c))
		}
	}
	return toks, nil
}

func isIdentChar(c byte) bool {
	return c == '_' || c == '-' || c == '.' || c == ':' || c == '/' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

type parser struct {
	toks []token
	pos  int
}

// ParseExpr parses a boolean expression into a tree.
// Operators: AND/OR/NOT (case-insensitive), &&, ||, !, parentheses.
func ParseExpr(src string) (*Node, error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("empty expression")
	}
	p := &parser{toks: toks}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("unexpected trailing token %q", p.toks[p.pos].text)
	}
	return root, nil
}

func (p *parser) peek() (token, bool) {
	if p.pos < len(p.toks) {
		return p.toks[p.pos], true
	}
	return token{}, false
}

func (p *parser) parseOr() (*Node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	children := []*Node{left}
	for {
		tok, ok := p.peek()
		if !ok || tok.kind != "or" {
			break
		}
		p.pos++
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		children = append(children, right)
	}
	if len(children) == 1 {
		return left, nil
	}
	return &Node{Kind: KindOr, Children: children}, nil
}

func (p *parser) parseAnd() (*Node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	children := []*Node{left}
	for {
		tok, ok := p.peek()
		if !ok || tok.kind != "and" {
			break
		}
		p.pos++
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		children = append(children, right)
	}
	if len(children) == 1 {
		return left, nil
	}
	return &Node{Kind: KindAnd, Children: children}, nil
}

func (p *parser) parseNot() (*Node, error) {
	tok, ok := p.peek()
	if ok && tok.kind == "not" {
		p.pos++
		inner, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &Node{Kind: KindNot, Children: []*Node{inner}}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (*Node, error) {
	tok, ok := p.peek()
	if !ok {
		return nil, fmt.Errorf("unexpected end of expression")
	}
	switch tok.kind {
	case "ident":
		p.pos++
		return &Node{Kind: KindEvidence, ID: tok.text}, nil
	case "lparen":
		p.pos++
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		closing, ok := p.peek()
		if !ok || closing.kind != "rparen" {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return inner, nil
	}
	return nil, fmt.Errorf("unexpected token %q", tok.text)
}
