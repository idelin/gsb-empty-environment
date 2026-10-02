package mini

import "fmt"

// Parse parses a mini-language program:
//
//	stmt   := "var" IDENT [type] "=" expr | "print" "(" expr ")" | expr
//	type   := "int" | "float" | "bool" | "string"
//
// Statements are terminated by newlines or ';'. "var x = e" infers the type
// of x from e; "var x T = e" checks e against T.
func Parse(src string) (*Program, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	return p.program()
}

type parser struct {
	toks []token
	i    int
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) next() token { t := p.toks[p.i]; p.i++; return t }
func (p *parser) errf(pos Pos, format string, args ...any) error {
	return fmt.Errorf("%s: %s", pos, fmt.Sprintf(format, args...))
}

func (p *parser) skipNewlines() {
	for p.peek().kind == kNewline {
		p.next()
	}
}

func (p *parser) program() (*Program, error) {
	prog := &Program{node: node{Pos{1, 1}}}
	p.skipNewlines()
	for p.peek().kind != kEOF {
		s, err := p.stmt()
		if err != nil {
			return nil, err
		}
		prog.Stmts = append(prog.Stmts, s)
		if t := p.peek(); t.kind != kNewline && t.kind != kEOF {
			return nil, p.errf(t.pos, "expected end of statement, got %q", t.text)
		}
		p.skipNewlines()
	}
	return prog, nil
}

var typeNames = map[string]Type{
	"int":    TInt,
	"float":  TFloat,
	"bool":   TBool,
	"string": TString,
}

func (p *parser) stmt() (Stmt, error) {
	t := p.peek()
	if t.kind == kIdent {
		switch t.text {
		case "var":
			return p.varDecl()
		case "print":
			return p.printStmt()
		}
	}
	x, err := p.expr()
	if err != nil {
		return nil, err
	}
	return &ExprStmt{node: node{x.Pos()}, X: x}, nil
}

func (p *parser) varDecl() (Stmt, error) {
	kw := p.next() // "var"
	name := p.next()
	if name.kind != kIdent {
		return nil, p.errf(name.pos, "expected variable name, got %q", name.text)
	}
	d := &VarDecl{node: node{kw.pos}, Name: name.text}
	if t := p.peek(); t.kind == kIdent {
		if typ, ok := typeNames[t.text]; ok {
			p.next()
			d.HasType = true
			d.DeclType = typ
		}
	}
	eq := p.next()
	if eq.kind != kOp || eq.text != "=" {
		return nil, p.errf(eq.pos, "expected = in variable declaration, got %q", eq.text)
	}
	init, err := p.expr()
	if err != nil {
		return nil, err
	}
	d.Init = init
	return d, nil
}

func (p *parser) printStmt() (Stmt, error) {
	kw := p.next() // "print"
	if t := p.next(); t.kind != kOp || t.text != "(" {
		return nil, p.errf(t.pos, "expected ( after print, got %q", t.text)
	}
	x, err := p.expr()
	if err != nil {
		return nil, err
	}
	if t := p.next(); t.kind != kOp || t.text != ")" {
		return nil, p.errf(t.pos, "expected ) after print argument, got %q", t.text)
	}
	return &PrintStmt{node: node{kw.pos}, X: x}, nil
}

var binPrec = map[string]int{
	"||": 1,
	"&&": 2,
	"==": 3, "!=": 3,
	"<": 4, "<=": 4, ">": 4, ">=": 4,
	"+": 5, "-": 5,
	"*": 6, "/": 6, "%": 6,
}

func (p *parser) expr() (Expr, error) { return p.binary(1) }

func (p *parser) binary(minPrec int) (Expr, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		prec, ok := binPrec[t.text]
		if t.kind != kOp || !ok || prec < minPrec {
			return left, nil
		}
		p.next()
		right, err := p.binary(prec + 1)
		if err != nil {
			return nil, err
		}
		left = &Binary{node: node{t.pos}, Op: t.text, L: left, R: right}
	}
}

func (p *parser) unary() (Expr, error) {
	if t := p.peek(); t.kind == kOp && (t.text == "-" || t.text == "!") {
		p.next()
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &Unary{node: node{t.pos}, Op: t.text, X: x}, nil
	}
	return p.primary()
}

func (p *parser) primary() (Expr, error) {
	t := p.next()
	switch t.kind {
	case kInt:
		return &Literal{node: node{t.pos}, Kind: TInt, Text: t.text}, nil
	case kFloat:
		return &Literal{node: node{t.pos}, Kind: TFloat, Text: t.text}, nil
	case kString:
		return &Literal{node: node{t.pos}, Kind: TString, Text: t.text}, nil
	case kIdent:
		switch t.text {
		case "true", "false":
			return &Literal{node: node{t.pos}, Kind: TBool, Text: t.text}, nil
		}
		return &Ident{node: node{t.pos}, Name: t.text}, nil
	case kOp:
		if t.text == "(" {
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			if c := p.next(); c.kind != kOp || c.text != ")" {
				return nil, p.errf(c.pos, "expected ), got %q", c.text)
			}
			return x, nil
		}
	}
	return nil, p.errf(t.pos, "expected expression, got %q", t.text)
}
