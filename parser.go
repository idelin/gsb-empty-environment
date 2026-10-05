package main

import (
	"fmt"
	"strconv"
)

type tokKind int

const (
	tkEOF tokKind = iota
	tkIdent
	tkNum
	tkOp     // + - == != < <= > >=
	tkAssign // =
	tkLParen
	tkRParen
	tkLBrace
	tkRBrace
	tkComma
)

type token struct {
	kind tokKind
	text string
	line int
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func lex(src string) ([]token, error) {
	var toks []token
	line := 1
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case isDigit(c):
			j := i
			for j < len(src) && isDigit(src[j]) {
				j++
			}
			toks = append(toks, token{tkNum, src[i:j], line})
			i = j
		case isAlpha(c):
			j := i
			for j < len(src) && (isAlpha(src[j]) || isDigit(src[j])) {
				j++
			}
			toks = append(toks, token{tkIdent, src[i:j], line})
			i = j
		case c == '+' || c == '-':
			toks = append(toks, token{tkOp, string(c), line})
			i++
		case c == '=':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{tkOp, "==", line})
				i += 2
			} else {
				toks = append(toks, token{tkAssign, "=", line})
				i++
			}
		case c == '!':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{tkOp, "!=", line})
				i += 2
			} else {
				return nil, fmt.Errorf("line %d: unexpected '!'", line)
			}
		case c == '<':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{tkOp, "<=", line})
				i += 2
			} else {
				toks = append(toks, token{tkOp, "<", line})
				i++
			}
		case c == '>':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{tkOp, ">=", line})
				i += 2
			} else {
				toks = append(toks, token{tkOp, ">", line})
				i++
			}
		case c == '(':
			toks = append(toks, token{tkLParen, "(", line})
			i++
		case c == ')':
			toks = append(toks, token{tkRParen, ")", line})
			i++
		case c == '{':
			toks = append(toks, token{tkLBrace, "{", line})
			i++
		case c == '}':
			toks = append(toks, token{tkRBrace, "}", line})
			i++
		case c == ',':
			toks = append(toks, token{tkComma, ",", line})
			i++
		default:
			return nil, fmt.Errorf("line %d: unexpected character %q", line, c)
		}
	}
	toks = append(toks, token{tkEOF, "", line})
	return toks, nil
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) errf(format string, args ...interface{}) error {
	return fmt.Errorf("line %d: %s", p.peek().line, fmt.Sprintf(format, args...))
}

func (p *parser) expect(kind tokKind, what string) (token, error) {
	t := p.peek()
	if t.kind != kind {
		return t, p.errf("expected %s, got %q", what, t.text)
	}
	return p.next(), nil
}

var cmpOps = []string{"==", "!=", "<", "<=", ">", ">="}

func isCmpOp(s string) bool {
	for _, op := range cmpOps {
		if op == s {
			return true
		}
	}
	return false
}

// Parse parses a program:
//
//	program := ["input" ident ("," ident)*] stmt*
//	stmt    := ident "=" expr | "if" expr "{" stmt* "}" ["else" "{" stmt* "}"] | "return" expr
//	expr    := add [cmpOp add]
//	add     := unary (("+"|"-") unary)*
//	unary   := "-" unary | primary
//	primary := num | ident | "(" expr ")"
func Parse(src string) (*Program, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	prog := &Program{}
	if t := p.peek(); t.kind == tkIdent && t.text == "input" {
		p.next()
		for {
			t = p.peek()
			if t.kind != tkIdent {
				return nil, p.errf("expected input variable name")
			}
			prog.Inputs = append(prog.Inputs, p.next().text)
			if p.peek().kind == tkComma {
				p.next()
				continue
			}
			break
		}
	}
	for p.peek().kind != tkEOF {
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		prog.Body = append(prog.Body, s)
	}
	return prog, nil
}

func (p *parser) parseStmt() (Stmt, error) {
	t := p.peek()
	if t.kind != tkIdent {
		return nil, p.errf("expected statement, got %q", t.text)
	}
	switch t.text {
	case "if":
		p.next()
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		then, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		var els []Stmt
		if nt := p.peek(); nt.kind == tkIdent && nt.text == "else" {
			p.next()
			els, err = p.parseBlock()
			if err != nil {
				return nil, err
			}
		}
		return &If{Cond: cond, Then: then, Else: els, Line: t.line}, nil
	case "return":
		p.next()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &Return{E: e, Line: t.line}, nil
	default:
		name := p.next()
		if _, err := p.expect(tkAssign, "'='"); err != nil {
			return nil, err
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &Assign{Name: name.text, E: e, Line: name.line}, nil
	}
}

func (p *parser) parseBlock() ([]Stmt, error) {
	if _, err := p.expect(tkLBrace, "'{'"); err != nil {
		return nil, err
	}
	var stmts []Stmt
	for p.peek().kind != tkRBrace {
		if p.peek().kind == tkEOF {
			return nil, p.errf("unterminated block")
		}
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, s)
	}
	p.next()
	return stmts, nil
}

func (p *parser) parseExpr() (Expr, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	t := p.peek()
	if t.kind == tkOp && isCmpOp(t.text) {
		p.next()
		r, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		return &Cmp{Op: t.text, L: l, R: r, Line: t.line}, nil
	}
	return l, nil
}

func (p *parser) parseAdd() (Expr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind == tkOp && (t.text == "+" || t.text == "-") {
			p.next()
			r, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			l = &Bin{Op: t.text, L: l, R: r, Line: t.line}
			continue
		}
		return l, nil
	}
}

func (p *parser) parseUnary() (Expr, error) {
	t := p.peek()
	if t.kind == tkOp && t.text == "-" {
		p.next()
		e, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if c, ok := e.(*Const); ok {
			return &Const{Val: -c.Val, Line: t.line}, nil
		}
		return &Neg{E: e, Line: t.line}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tkNum:
		p.next()
		n, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: bad number %q", t.line, t.text)
		}
		// Integer literals wrap into signed 8-bit, matching runtime semantics.
		return &Const{Val: int8(n), Line: t.line}, nil
	case tkIdent:
		p.next()
		return &Var{Name: t.text, Line: t.line}, nil
	case tkLParen:
		p.next()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tkRParen, "')'"); err != nil {
			return nil, err
		}
		return e, nil
	}
	return nil, p.errf("expected expression, got %q", t.text)
}
