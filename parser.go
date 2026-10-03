package decor

import (
	"fmt"
	"strings"
)

var reserved = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "AND": true, "OR": true,
	"NOT": true, "EXISTS": true, "IN": true, "AS": true, "DISTINCT": true,
	"ON": true, "JOIN": true, "SEMI": true, "INNER": true, "CROSS": true,
	"LIMIT": true,
}

func isReserved(s string) bool { return reserved[strings.ToUpper(s)] }

// Parse parses a single SELECT statement.
func Parse(src string) (*SelectStmt, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	stmt, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected trailing input %q", p.peek().text)
	}
	return stmt, nil
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) peekAt(n int) token {
	if p.pos+n >= len(p.toks) {
		return token{tEOF, ""}
	}
	return p.toks[p.pos+n]
}
func (p *parser) next() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) isKeyword(kw string) bool {
	t := p.peek()
	return t.kind == tIdent && strings.EqualFold(t.text, kw)
}

func (p *parser) acceptKeyword(kw string) bool {
	if p.isKeyword(kw) {
		p.next()
		return true
	}
	return false
}

func (p *parser) expectKeyword(kw string) error {
	if !p.acceptKeyword(kw) {
		return fmt.Errorf("expected %s, got %q", kw, p.peek().text)
	}
	return nil
}

func (p *parser) acceptSymbol(s string) bool {
	t := p.peek()
	if t.kind == tSymbol && t.text == s {
		p.next()
		return true
	}
	return false
}

func (p *parser) expectSymbol(s string) error {
	if !p.acceptSymbol(s) {
		return fmt.Errorf("expected %q, got %q", s, p.peek().text)
	}
	return nil
}

func (p *parser) expectIdent() (string, error) {
	t := p.peek()
	if t.kind != tIdent {
		return "", fmt.Errorf("expected identifier, got %q", t.text)
	}
	p.next()
	return t.text, nil
}

func (p *parser) parseSelect() (*SelectStmt, error) {
	if err := p.expectKeyword("SELECT"); err != nil {
		return nil, err
	}
	stmt := &SelectStmt{}
	stmt.Distinct = p.acceptKeyword("DISTINCT")
	for {
		item, err := p.parseSelectItem()
		if err != nil {
			return nil, err
		}
		stmt.Items = append(stmt.Items, item)
		if !p.acceptSymbol(",") {
			break
		}
	}
	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}
	for {
		ref, err := p.parseTableRef()
		if err != nil {
			return nil, err
		}
		stmt.From = append(stmt.From, ref)
		if !p.acceptSymbol(",") {
			break
		}
	}
	if p.acceptKeyword("WHERE") {
		w, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Where = w
	}
	if p.acceptKeyword("LIMIT") {
		t := p.next()
		if t.kind != tNumber {
			return nil, fmt.Errorf("expected number after LIMIT, got %q", t.text)
		}
		var n int
		if _, err := fmt.Sscanf(t.text, "%d", &n); err != nil {
			return nil, fmt.Errorf("invalid LIMIT value %q", t.text)
		}
		stmt.Limit = &n
	}
	return stmt, nil
}

func (p *parser) parseSelectItem() (SelectItem, error) {
	if p.acceptSymbol("*") {
		return SelectItem{Star: true}, nil
	}
	// t.*
	if p.peek().kind == tIdent && p.peekAt(1).kind == tSymbol && p.peekAt(1).text == "." &&
		p.peekAt(2).kind == tSymbol && p.peekAt(2).text == "*" {
		t := p.next()
		p.next() // .
		p.next() // *
		return SelectItem{TableStar: t.text}, nil
	}
	e, err := p.parseExpr()
	if err != nil {
		return SelectItem{}, err
	}
	item := SelectItem{Expr: e}
	if p.acceptKeyword("AS") {
		alias, err := p.expectIdent()
		if err != nil {
			return SelectItem{}, err
		}
		item.Alias = alias
	} else if p.peek().kind == tIdent && !isReserved(p.peek().text) {
		item.Alias = p.next().text
	}
	return item, nil
}

func (p *parser) parseTableRef() (TableRef, error) {
	name, err := p.expectIdent()
	if err != nil {
		return TableRef{}, err
	}
	ref := TableRef{Name: name}
	if p.acceptKeyword("AS") {
		alias, err := p.expectIdent()
		if err != nil {
			return TableRef{}, err
		}
		ref.Alias = alias
	} else if p.peek().kind == tIdent && !isReserved(p.peek().text) {
		ref.Alias = p.next().text
	}
	return ref, nil
}

func (p *parser) parseExpr() (Expr, error) { return p.parseOr() }

func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.acceptKeyword("OR") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "OR", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.acceptKeyword("AND") {
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "AND", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseNot() (Expr, error) {
	if p.acceptKeyword("NOT") {
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &NotExpr{X: x}, nil
	}
	return p.parsePredicate()
}

var cmpOps = map[string]bool{"=": true, "<": true, ">": true, "<=": true, ">=": true, "<>": true, "!=": true}

func (p *parser) parsePredicate() (Expr, error) {
	if p.acceptKeyword("EXISTS") {
		if err := p.expectSymbol("("); err != nil {
			return nil, err
		}
		sub, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if err := p.expectSymbol(")"); err != nil {
			return nil, err
		}
		return &ExistsExpr{Subquery: sub}, nil
	}
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	// [NOT] IN (...)
	negated := false
	if p.isKeyword("NOT") && p.peekAt(1).kind == tIdent && strings.EqualFold(p.peekAt(1).text, "IN") {
		p.next()
		negated = true
	}
	if p.acceptKeyword("IN") {
		if err := p.expectSymbol("("); err != nil {
			return nil, err
		}
		in := &InExpr{Left: left, Negated: negated}
		if p.isKeyword("SELECT") {
			sub, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			in.Subquery = sub
		} else {
			for {
				e, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				in.List = append(in.List, e)
				if !p.acceptSymbol(",") {
					break
				}
			}
		}
		if err := p.expectSymbol(")"); err != nil {
			return nil, err
		}
		return in, nil
	}
	if negated {
		return nil, fmt.Errorf("expected IN after NOT")
	}
	t := p.peek()
	if t.kind == tSymbol && cmpOps[t.text] {
		p.next()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Op: t.text, Left: left, Right: right}, nil
	}
	return left, nil
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tSymbol:
		if t.text == "(" {
			p.next()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectSymbol(")"); err != nil {
				return nil, err
			}
			return e, nil
		}
	case tNumber:
		p.next()
		return &Literal{Kind: LitNumber, Value: t.text}, nil
	case tString:
		p.next()
		return &Literal{Kind: LitString, Value: t.text}, nil
	case tIdent:
		p.next()
		if p.acceptSymbol(".") {
			name, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			return &ColumnRef{Table: t.text, Name: name}, nil
		}
		return &ColumnRef{Name: t.text}, nil
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}
