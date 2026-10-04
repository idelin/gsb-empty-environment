package main

import "fmt"

type NodeKind int

const (
	nNum NodeKind = iota
	nIdent
	nBinary
	nUnary
	nCall
	nParen
)

func (k NodeKind) String() string {
	switch k {
	case nNum:
		return "Number"
	case nIdent:
		return "Ident"
	case nBinary:
		return "BinaryExpr"
	case nUnary:
		return "UnaryExpr"
	case nCall:
		return "CallExpr"
	case nParen:
		return "ParenExpr"
	}
	return "?"
}

// Node is a syntax node. Start/End are byte offsets into the source and
// cover exactly the node's own tokens (including grouping parens).
type Node struct {
	Kind    NodeKind
	Start   int
	End     int
	Op      string  // nBinary / nUnary
	Name    string  // nIdent / nCall (callee)
	Left    *Node   // nBinary
	Right   *Node   // nBinary
	Operand *Node   // nUnary
	Args    []*Node // nCall
	Inner   *Node   // nParen
}

func (n *Node) children() []*Node {
	switch n.Kind {
	case nBinary:
		return []*Node{n.Left, n.Right}
	case nUnary:
		return []*Node{n.Operand}
	case nCall:
		return n.Args
	case nParen:
		return []*Node{n.Inner}
	}
	return nil
}

type parser struct {
	toks []Token
	sig  []int // indexes of non-comment tokens in toks
	pos  int   // cursor into sig
}

// parse parses the whole token stream as a single expression.
func parse(toks []Token) (*Node, error) {
	p := &parser{toks: toks}
	for i, t := range toks {
		if !t.isComment() {
			p.sig = append(p.sig, i)
		}
	}
	if len(p.sig) == 0 {
		return nil, fmt.Errorf("empty input: no expression found")
	}
	n, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.sig) {
		t := p.toks[p.sig[p.pos]]
		return nil, fmt.Errorf("unexpected token %q at byte %d", t.Text, t.Start)
	}
	return n, nil
}

func (p *parser) peek() *Token {
	if p.pos >= len(p.sig) {
		return nil
	}
	return &p.toks[p.sig[p.pos]]
}

func (p *parser) next() *Token {
	t := p.peek()
	if t != nil {
		p.pos++
	}
	return t
}

func (p *parser) parseExpr() (*Node, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t == nil || t.Kind != kOp || (t.Text != "+" && t.Text != "-") {
			return left, nil
		}
		p.next()
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = &Node{Kind: nBinary, Op: t.Text, Left: left, Right: right, Start: left.Start, End: right.End}
	}
}

func (p *parser) parseTerm() (*Node, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t == nil || t.Kind != kOp || (t.Text != "*" && t.Text != "/") {
			return left, nil
		}
		p.next()
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		left = &Node{Kind: nBinary, Op: t.Text, Left: left, Right: right, Start: left.Start, End: right.End}
	}
}

func (p *parser) parseFactor() (*Node, error) {
	t := p.peek()
	if t == nil {
		return nil, fmt.Errorf("unexpected end of input")
	}
	switch t.Kind {
	case kNum:
		p.next()
		return &Node{Kind: nNum, Name: t.Text, Start: t.Start, End: t.End}, nil
	case kIdent:
		p.next()
		if nt := p.peek(); nt != nil && nt.Kind == kLParen {
			// function call
			p.toks[p.sig[p.pos]].CallOpen = true
			p.next() // consume '('
			call := &Node{Kind: nCall, Name: t.Text, Start: t.Start}
			if pt := p.peek(); pt != nil && pt.Kind == kRParen {
				p.next()
				call.End = pt.End
				return call, nil
			}
			for {
				arg, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				call.Args = append(call.Args, arg)
				nt = p.peek()
				if nt == nil {
					return nil, fmt.Errorf("unterminated call starting at byte %d", t.Start)
				}
				if nt.Kind == kComma {
					p.next()
					continue
				}
				if nt.Kind == kRParen {
					p.next()
					call.End = nt.End
					return call, nil
				}
				return nil, fmt.Errorf("expected ',' or ')' at byte %d", nt.Start)
			}
		}
		return &Node{Kind: nIdent, Name: t.Text, Start: t.Start, End: t.End}, nil
	case kLParen:
		p.next()
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		nt := p.peek()
		if nt == nil || nt.Kind != kRParen {
			return nil, fmt.Errorf("unterminated '(' at byte %d", t.Start)
		}
		p.next()
		return &Node{Kind: nParen, Inner: inner, Start: t.Start, End: nt.End}, nil
	case kOp:
		if t.Text == "-" {
			p.next()
			p.toks[p.sig[p.pos-1]].Unary = true
			operand, err := p.parseFactor()
			if err != nil {
				return nil, err
			}
			return &Node{Kind: nUnary, Op: "-", Operand: operand, Start: t.Start, End: operand.End}, nil
		}
	}
	return nil, fmt.Errorf("unexpected token %q at byte %d", t.Text, t.Start)
}
