package sandbox

import "fmt"

type nodeKind int

const (
	nNumber nodeKind = iota
	nString
	nBool
	nIdent
	nUnary
	nBinary
	nCall
)

// node is a single AST node. val holds the literal text, identifier name,
// operator, or function name; args holds operands / call arguments.
type node struct {
	kind nodeKind
	val  string
	args []*node
}

// maxParseDepth bounds recursion while parsing, so pathological inputs
// (e.g. a million nested parentheses) cannot overflow the host stack
// before AST validation even runs.
const maxParseDepth = 1000

type parser struct {
	toks  []token
	pos   int
	depth int
}

func (p *parser) enter() error {
	p.depth++
	if p.depth > maxParseDepth {
		return fmt.Errorf("expression nested deeper than %d levels", maxParseDepth)
	}
	return nil
}

func (p *parser) leave() { p.depth-- }

func parse(src string) (*node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tokEOF {
		return nil, fmt.Errorf("unexpected trailing token %q at position %d", t.text, t.pos)
	}
	return n, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) isOp(op string) bool {
	t := p.peek()
	return t.kind == tokOp && t.text == op
}

// expr := orExpr
func (p *parser) parseOr() (*node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isOp("||") {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &node{kind: nBinary, val: "||", args: []*node{left, right}}
	}
	return left, nil
}

func (p *parser) parseAnd() (*node, error) {
	left, err := p.parseCmp()
	if err != nil {
		return nil, err
	}
	for p.isOp("&&") {
		p.next()
		right, err := p.parseCmp()
		if err != nil {
			return nil, err
		}
		left = &node{kind: nBinary, val: "&&", args: []*node{left, right}}
	}
	return left, nil
}

func (p *parser) parseCmp() (*node, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for {
		var op string
		for _, o := range []string{"==", "!=", "<=", ">=", "<", ">"} {
			if p.isOp(o) {
				op = o
				break
			}
		}
		if op == "" {
			return left, nil
		}
		p.next()
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		left = &node{kind: nBinary, val: op, args: []*node{left, right}}
	}
}

func (p *parser) parseAdd() (*node, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.isOp("+") || p.isOp("-") {
		op := p.next().text
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		left = &node{kind: nBinary, val: op, args: []*node{left, right}}
	}
	return left, nil
}

func (p *parser) parseMul() (*node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.isOp("*") || p.isOp("/") || p.isOp("%") {
		op := p.next().text
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &node{kind: nBinary, val: op, args: []*node{left, right}}
	}
	return left, nil
}

func (p *parser) parseUnary() (*node, error) {
	if p.isOp("!") || p.isOp("-") {
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer p.leave()
		op := p.next().text
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &node{kind: nUnary, val: op, args: []*node{operand}}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (*node, error) {
	t := p.next()
	switch t.kind {
	case tokNumber:
		return &node{kind: nNumber, val: t.text}, nil
	case tokString:
		return &node{kind: nString, val: t.text}, nil
	case tokIdent:
		switch t.text {
		case "true", "false":
			return &node{kind: nBool, val: t.text}, nil
		}
		if p.peek().kind == tokLParen {
			if err := p.enter(); err != nil {
				return nil, err
			}
			defer p.leave()
			p.next() // consume '('
			var args []*node
			if p.peek().kind != tokRParen {
				for {
					a, err := p.parseOr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.peek().kind == tokComma {
						p.next()
						continue
					}
					break
				}
			}
			if p.peek().kind != tokRParen {
				return nil, fmt.Errorf("missing ')' after arguments of %q at position %d", t.text, t.pos)
			}
			p.next() // consume ')'
			return &node{kind: nCall, val: t.text, args: args}, nil
		}
		return &node{kind: nIdent, val: t.text}, nil
	case tokLParen:
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer p.leave()
		n, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, fmt.Errorf("missing ')' for group opened at position %d", t.pos)
		}
		p.next()
		return n, nil
	}
	return nil, fmt.Errorf("unexpected token %q at position %d", t.text, t.pos)
}
