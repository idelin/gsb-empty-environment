package main

import (
	"fmt"
	"strconv"
	"strings"
)

// ---------- AST ----------

type Expr interface{ exprNode() }

type Num struct{ Val int8 }
type Var struct{ Name string }
type Neg struct{ E Expr }

// Bin is used both for arithmetic ("+", "-") and comparison
// ("<", "<=", ">", ">=", "==", "!="). Comparisons evaluate to 1 or 0.
type Bin struct {
	Op   string
	L, R Expr
}

func (Num) exprNode() {}
func (Var) exprNode() {}
func (Neg) exprNode() {}
func (Bin) exprNode() {}

func exprString(e Expr) string {
	switch e := e.(type) {
	case Num:
		return strconv.Itoa(int(e.Val))
	case Var:
		return e.Name
	case Neg:
		return "-" + exprString(e.E)
	case Bin:
		return "(" + exprString(e.L) + " " + e.Op + " " + exprString(e.R) + ")"
	}
	return "?"
}

type Stmt interface{ stmtNode() }

type Input struct{ Names []string }
type Assign struct {
	Name string
	E    Expr
}
type If struct {
	Cond       Expr
	Then, Else []Stmt
}
type Return struct{ E Expr }

func (Input) stmtNode()  {}
func (Assign) stmtNode() {}
func (If) stmtNode()     {}
func (Return) stmtNode() {}

// ---------- Lexer ----------

type tokKind int

const (
	tEOF tokKind = iota
	tNewline
	tIdent
	tNum
	tSym
)

type token struct {
	kind tokKind
	text string
	line int
}

func lex(src string) ([]token, error) {
	var toks []token
	line := 1
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == 32 || c == 9 || c == 13:
			i++
		case c == 35: // #
			for i < len(src) && src[i] != 10 {
				i++
			}
		case c == 10:
			toks = append(toks, token{tNewline, "\n", line})
			line++
			i++
		case c >= 48 && c <= 57:
			j := i
			for j < len(src) && src[j] >= 48 && src[j] <= 57 {
				j++
			}
			toks = append(toks, token{tNum, src[i:j], line})
			i = j
		case c == 95 || (c >= 97 && c <= 122) || (c >= 65 && c <= 90):
			j := i
			for j < len(src) && (src[j] == 95 ||
				(src[j] >= 97 && src[j] <= 122) ||
				(src[j] >= 65 && src[j] <= 90) ||
				(src[j] >= 48 && src[j] <= 57)) {
				j++
			}
			toks = append(toks, token{tIdent, src[i:j], line})
			i = j
		default:
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			if two == "<=" || two == ">=" || two == "==" || two == "!=" {
				toks = append(toks, token{tSym, two, line})
				i += 2
			} else if strings.IndexByte("+-(),=<>", c) >= 0 {
				toks = append(toks, token{tSym, string(c), line})
				i++
			} else {
				return nil, fmt.Errorf("line %d: unexpected character %q", line, c)
			}
		}
	}
	toks = append(toks, token{tEOF, "", line})
	return toks, nil
}

// ---------- Parser ----------

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) atEOF() bool { return p.peek().kind == tEOF }

func (p *parser) expectSym(s string) error {
	t := p.peek()
	if t.kind != tSym || t.text != s {
		return fmt.Errorf("line %d: expected %q, got %q", t.line, s, t.text)
	}
	p.next()
	return nil
}

func (p *parser) skipNewlines() {
	for p.peek().kind == tNewline {
		p.next()
	}
}

// endStmt consumes the statement terminator (newline or EOF).
func (p *parser) endStmt() error {
	t := p.peek()
	if t.kind == tNewline || t.kind == tEOF {
		p.skipNewlines()
		return nil
	}
	return fmt.Errorf("line %d: expected end of statement, got %q", t.line, t.text)
}

func parse(src string) ([]Stmt, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	p.skipNewlines()
	stmts, err := p.parseBlock(nil)
	if err != nil {
		return nil, err
	}
	if !p.atEOF() {
		t := p.peek()
		return nil, fmt.Errorf("line %d: unexpected %q", t.line, t.text)
	}
	return stmts, nil
}

// parseBlock parses statements until EOF or one of the stop keywords.
func (p *parser) parseBlock(stop map[string]bool) ([]Stmt, error) {
	var stmts []Stmt
	for {
		t := p.peek()
		if t.kind == tEOF {
			return stmts, nil
		}
		if t.kind == tIdent && stop[t.text] {
			return stmts, nil
		}
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, s)
	}
}

func (p *parser) parseStmt() (Stmt, error) {
	t := p.peek()
	if t.kind != tIdent {
		return nil, fmt.Errorf("line %d: expected statement, got %q", t.line, t.text)
	}
	switch t.text {
	case "input":
		p.next()
		var names []string
		for {
			id := p.peek()
			if id.kind != tIdent || reserved[id.text] {
				return nil, fmt.Errorf("line %d: expected input name, got %q", id.line, id.text)
			}
			names = append(names, id.text)
			p.next()
			if p.peek().kind == tSym && p.peek().text == "," {
				p.next()
				continue
			}
			break
		}
		return Input{names}, p.endStmt()
	case "return":
		p.next()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return Return{e}, p.endStmt()
	case "if":
		p.next()
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.endStmt(); err != nil {
			return nil, err
		}
		then, err := p.parseBlock(map[string]bool{"else": true, "end": true})
		if err != nil {
			return nil, err
		}
		var els []Stmt
		if p.peek().kind == tIdent && p.peek().text == "else" {
			p.next()
			if err := p.endStmt(); err != nil {
				return nil, err
			}
			els, err = p.parseBlock(map[string]bool{"end": true})
			if err != nil {
				return nil, err
			}
		}
		end := p.peek()
		if end.kind != tIdent || end.text != "end" {
			return nil, fmt.Errorf("line %d: expected \"end\" to close if, got %q", end.line, end.text)
		}
		p.next()
		return If{cond, then, els}, p.endStmt()
	case "else", "end":
		return nil, fmt.Errorf("line %d: unexpected %q", t.line, t.text)
	default:
		name := t.text
		p.next()
		if err := p.expectSym("="); err != nil {
			return nil, err
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return Assign{name, e}, p.endStmt()
	}
}

var reserved = map[string]bool{
	"input": true, "return": true, "if": true, "else": true, "end": true,
}

func (p *parser) parseExpr() (Expr, error) { return p.parseCmp() }

func (p *parser) parseCmp() (Expr, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	t := p.peek()
	if t.kind == tSym {
		switch t.text {
		case "<", "<=", ">", ">=", "==", "!=":
			p.next()
			r, err := p.parseAdd()
			if err != nil {
				return nil, err
			}
			return Bin{t.text, l, r}, nil
		}
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
		if t.kind == tSym && (t.text == "+" || t.text == "-") {
			p.next()
			r, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			l = Bin{t.text, l, r}
		} else {
			return l, nil
		}
	}
}

func (p *parser) parseUnary() (Expr, error) {
	t := p.peek()
	if t.kind == tSym && t.text == "-" {
		p.next()
		// Fold negative literals so that -128 is representable even
		// though 128 is not a valid positive literal.
		if nt := p.peek(); nt.kind == tNum {
			p.next()
			n, err := strconv.Atoi(nt.text)
			if err != nil || n > 128 {
				return nil, fmt.Errorf("line %d: literal -%s out of range (-128..127)", nt.line, nt.text)
			}
			return Num{int8(-n)}, nil
		}
		e, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return Neg{e}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tNum:
		p.next()
		n, err := strconv.Atoi(t.text)
		if err != nil || n > 127 {
			return nil, fmt.Errorf("line %d: literal %q out of range (-128..127)", t.line, t.text)
		}
		return Num{int8(n)}, nil
	case tIdent:
		if reserved[t.text] {
			return nil, fmt.Errorf("line %d: unexpected keyword %q", t.line, t.text)
		}
		p.next()
		return Var{t.text}, nil
	case tSym:
		if t.text == "(" {
			p.next()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectSym(")"); err != nil {
				return nil, err
			}
			return e, nil
		}
	}
	return nil, fmt.Errorf("line %d: unexpected %q", t.line, t.text)
}
