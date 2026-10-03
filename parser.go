package decorrelator

import (
	"fmt"
	"strings"
)

// Parse parses a SELECT statement into an AST.
func Parse(sql string) (*SelectStmt, error) {
	toks, err := lex(sql)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	stmt, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	if p.peek().kind == tSemi {
		p.next()
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected token %q after end of statement", p.peek().val)
	}
	return stmt, nil
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) isKeyword(kw string) bool {
	t := p.peek()
	return t.kind == tIdent && strings.EqualFold(t.val, kw)
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
		return fmt.Errorf("expected %s, got %q", kw, p.peek().val)
	}
	return nil
}

func (p *parser) expect(kind tokenKind, what string) error {
	if p.peek().kind != kind {
		return fmt.Errorf("expected %s, got %q", what, p.peek().val)
	}
	p.next()
	return nil
}

func (p *parser) parseSelect() (*SelectStmt, error) {
	if err := p.expectKeyword("SELECT"); err != nil {
		return nil, err
	}
	stmt := &SelectStmt{}
	if p.peek().kind == tStar {
		p.next()
		stmt.Star = true
	} else {
		for {
			item, err := p.parseSelectItem()
			if err != nil {
				return nil, err
			}
			stmt.Columns = append(stmt.Columns, item)
			if p.peek().kind != tComma {
				break
			}
			p.next()
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
		if p.peek().kind != tComma {
			break
		}
		p.next()
	}
	if p.acceptKeyword("WHERE") {
		w, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Where = w
	}
	return stmt, nil
}

func (p *parser) parseSelectItem() (SelectItem, error) {
	t := p.peek()
	if t.kind == tNumber {
		p.next()
		return SelectItem{Name: t.val, Literal: true}, nil
	}
	if t.kind != tIdent {
		return SelectItem{}, fmt.Errorf("expected column name, got %q", t.val)
	}
	p.next()
	item := SelectItem{Name: t.val}
	if p.peek().kind == tDot {
		p.next()
		item.Table = t.val
		if p.peek().kind == tStar {
			p.next()
			item.Name = "*"
		} else {
			c := p.peek()
			if c.kind != tIdent {
				return SelectItem{}, fmt.Errorf("expected column name after '.', got %q", c.val)
			}
			p.next()
			item.Name = c.val
		}
	}
	return item, nil
}

func (p *parser) parseTableRef() (TableRef, error) {
	t := p.peek()
	if t.kind != tIdent {
		return TableRef{}, fmt.Errorf("expected table name, got %q", t.val)
	}
	p.next()
	ref := TableRef{Name: t.val, Alias: t.val}
	p.acceptKeyword("AS")
	if p.peek().kind == tIdent && !isReserved(p.peek().val) {
		ref.Alias = p.next().val
	}
	return ref, nil
}

func isReserved(s string) bool {
	switch strings.ToUpper(s) {
	case "WHERE", "SELECT", "FROM", "AND", "OR", "NOT", "EXISTS", "IN", "AS", "ON", "JOIN":
		return true
	}
	return false
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
	left, err := p.parsePredicate()
	if err != nil {
		return nil, err
	}
	for p.acceptKeyword("AND") {
		right, err := p.parsePredicate()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "AND", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parsePredicate() (Expr, error) {
	if p.acceptKeyword("EXISTS") {
		if err := p.expect(tLParen, "'('"); err != nil {
			return nil, err
		}
		sub, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tRParen, "')'"); err != nil {
			return nil, err
		}
		return &ExistsExpr{Subquery: sub}, nil
	}
	if p.peek().kind == tLParen {
		p.next()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tRParen, "')'"); err != nil {
			return nil, err
		}
		return e, nil
	}
	left, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	if p.acceptKeyword("IN") {
		if err := p.expect(tLParen, "'('"); err != nil {
			return nil, err
		}
		sub, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tRParen, "')'"); err != nil {
			return nil, err
		}
		return &InExpr{Left: left, Subquery: sub}, nil
	}
	if p.peek().kind == tOp {
		op := p.next().val
		right, err := p.parseOperand()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Op: op, Left: left, Right: right}, nil
	}
	return left, nil
}

func (p *parser) parseOperand() (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tNumber:
		p.next()
		return &NumberLit{Value: t.val}, nil
	case tString:
		p.next()
		return &StringLit{Value: t.val}, nil
	case tIdent:
		p.next()
		ref := &ColumnRef{Name: t.val}
		if p.peek().kind == tDot {
			p.next()
			c := p.peek()
			if c.kind != tIdent {
				return nil, fmt.Errorf("expected column name after '.', got %q", c.val)
			}
			p.next()
			ref.Table = t.val
			ref.Name = c.val
		}
		return ref, nil
	case tLParen:
		p.next()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tRParen, "')'"); err != nil {
			return nil, err
		}
		return e, nil
	}
	return nil, fmt.Errorf("unexpected token %q in expression", t.val)
}
