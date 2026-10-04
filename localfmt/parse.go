package localfmt

import "fmt"

type NodeKind int

const (
	NNum NodeKind = iota
	NIdent
	NCall
	NBinary
	NUnary
	NParen
)

func (k NodeKind) String() string {
	switch k {
	case NNum:
		return "num"
	case NIdent:
		return "ident"
	case NCall:
		return "call"
	case NBinary:
		return "binary"
	case NUnary:
		return "unary"
	case NParen:
		return "paren"
	}
	return "?"
}

// Node spans cover the tokens of the subtree, including any comments
// interleaved between those tokens.
type Node struct {
	Kind       NodeKind
	Start, End int
	Op         string  // NBinary / NUnary
	Name       string  // NIdent / NCall / NNum literal
	Args       []*Node // NCall
	L, R       *Node   // NBinary
	X          *Node   // NUnary / NParen
}

type parser struct {
	toks []Token
	pos  int
}

func (p *parser) peek() Token { return p.toks[p.pos] }
func (p *parser) next() Token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) skipComments() {
	for p.peek().Kind == TComment {
		p.pos++
	}
}

// Parse parses a single expression; trailing tokens are an error.
func Parse(src string) (*Node, []Token, error) {
	toks, err := Lex(src)
	if err != nil {
		return nil, nil, err
	}
	p := &parser{toks: toks}
	n, err := p.parseExpr()
	if err != nil {
		return nil, nil, err
	}
	p.skipComments()
	if p.peek().Kind != TEOF {
		return nil, nil, fmt.Errorf("unexpected trailing token %v", p.peek())
	}
	return n, toks, nil
}

func (p *parser) parseExpr() (*Node, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		p.skipComments()
		t := p.peek()
		if t.Kind != TOp || (t.Text != "+" && t.Text != "-") {
			return left, nil
		}
		p.next()
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = &Node{Kind: NBinary, Start: left.Start, End: right.End, Op: t.Text, L: left, R: right}
	}
}

func (p *parser) parseTerm() (*Node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		p.skipComments()
		t := p.peek()
		if t.Kind != TOp || (t.Text != "*" && t.Text != "/") {
			return left, nil
		}
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &Node{Kind: NBinary, Start: left.Start, End: right.End, Op: t.Text, L: left, R: right}
	}
}

func (p *parser) parseUnary() (*Node, error) {
	p.skipComments()
	t := p.peek()
	if t.Kind == TOp && t.Text == "-" {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Node{Kind: NUnary, Start: t.Start, End: x.End, Op: "-", X: x}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (*Node, error) {
	p.skipComments()
	t := p.next()
	switch t.Kind {
	case TNum:
		return &Node{Kind: NNum, Start: t.Start, End: t.End, Name: t.Text}, nil
	case TIdent:
		n := &Node{Kind: NIdent, Start: t.Start, End: t.End, Name: t.Text}
		p.skipComments()
		if p.peek().Kind == TLParen {
			open := p.next()
			call := &Node{Kind: NCall, Start: t.Start, Name: t.Text}
			p.skipComments()
			if p.peek().Kind == TRParen {
				close := p.next()
				call.End = close.End
				_ = open
				return call, nil
			}
			for {
				arg, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				call.Args = append(call.Args, arg)
				p.skipComments()
				nt := p.next()
				if nt.Kind == TComma {
					continue
				}
				if nt.Kind == TRParen {
					call.End = nt.End
					return call, nil
				}
				return nil, fmt.Errorf("expected , or ) at %d, got %v", nt.Start, nt)
			}
		}
		return n, nil
	case TLParen:
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipComments()
		close := p.next()
		if close.Kind != TRParen {
			return nil, fmt.Errorf("expected ) at %d, got %v", close.Start, close)
		}
		return &Node{Kind: NParen, Start: t.Start, End: close.End, X: inner}, nil
	}
	return nil, fmt.Errorf("unexpected token %v", t)
}

// Contains reports whether n fully covers byte range [s, e).
func (n *Node) Contains(s, e int) bool { return n.Start <= s && n.End >= e }

// SmallestCovering returns the deepest node whose span fully covers [s, e).
func SmallestCovering(root *Node, s, e int) *Node {
	if root == nil || !root.Contains(s, e) {
		return nil
	}
	children := []*Node{}
	switch root.Kind {
	case NBinary:
		children = []*Node{root.L, root.R}
	case NUnary, NParen:
		children = []*Node{root.X}
	case NCall:
		children = root.Args
	}
	for _, c := range children {
		if sub := SmallestCovering(c, s, e); sub != nil {
			return sub
		}
	}
	return root
}
