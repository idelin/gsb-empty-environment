package relalg

import (
	"fmt"
	"strings"
)

// parser 是递归下降 SQL 解析器，支持：
//   SELECT proj [, proj]* FROM t [AS] a
//     [[INNER|LEFT|RIGHT] JOIN t [AS] a ON cond]*
//     [WHERE cond]
// 表达式优先级：OR < AND < 比较运算 <  primary。
type parser struct {
	toks []token
	pos  int
}

// ParseSelect 解析一条 SELECT 查询，返回查询核心 AST。
func ParseSelect(input string) (*SelectStmt, error) {
	toks, err := lex(input)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	stmt, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tokEOF {
		return nil, fmt.Errorf("unexpected trailing input %q", p.cur().val)
	}
	return stmt, nil
}

func (p *parser) cur() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) isKeyword(kw string) bool {
	t := p.cur()
	return t.kind == tokIdent && strings.EqualFold(t.val, kw)
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
		return fmt.Errorf("expected keyword %s, got %q", kw, p.cur().val)
	}
	return nil
}

func (p *parser) acceptSymbol(s string) bool {
	if p.cur().kind == tokSymbol && p.cur().val == s {
		p.next()
		return true
	}
	return false
}

func (p *parser) expectSymbol(s string) error {
	if !p.acceptSymbol(s) {
		return fmt.Errorf("expected symbol %q, got %q", s, p.cur().val)
	}
	return nil
}

func (p *parser) expectIdent() (string, error) {
	if p.cur().kind != tokIdent {
		return "", fmt.Errorf("expected identifier, got %q", p.cur().val)
	}
	return p.next().val, nil
}

func (p *parser) parseSelect() (*SelectStmt, error) {
	if err := p.expectKeyword("SELECT"); err != nil {
		return nil, err
	}
	proj, err := p.parseSelectList()
	if err != nil {
		return nil, err
	}
	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}
	from, err := p.parseTableSource()
	if err != nil {
		return nil, err
	}
	stmt := &SelectStmt{Projection: proj, From: from}
	if p.acceptKeyword("WHERE") {
		w, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Where = w
	}
	return stmt, nil
}

func (p *parser) parseSelectList() ([]Expr, error) {
	var list []Expr
	for {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		list = append(list, e)
		if !p.acceptSymbol(",") {
			break
		}
	}
	return list, nil
}

func (p *parser) parseTableSource() (*TableSource, error) {
	t, err := p.parseTableFactor()
	if err != nil {
		return nil, err
	}
	for {
		kind := "INNER"
		switch {
		case p.isKeyword("INNER"):
			p.next()
		case p.isKeyword("LEFT"):
			p.next()
			kind = "LEFT"
		case p.isKeyword("RIGHT"):
			p.next()
			kind = "RIGHT"
		}
		if !p.isKeyword("JOIN") {
			if kind != "INNER" {
				return nil, fmt.Errorf("expected JOIN after %s", kind)
			}
			break
		}
		p.next()
		jt, err := p.parseTableFactor()
		if err != nil {
			return nil, err
		}
		if err := p.expectKeyword("ON"); err != nil {
			return nil, err
		}
		on, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		t.Joins = append(t.Joins, &JoinClause{Kind: kind, Table: jt, On: on})
	}
	return t, nil
}

func (p *parser) parseTableFactor() (*TableSource, error) {
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	ts := &TableSource{Name: name}
	p.acceptKeyword("AS")
	if p.cur().kind == tokIdent && !isReserved(p.cur().val) {
		ts.Alias, _ = p.expectIdent()
	}
	return ts, nil
}

func isReserved(s string) bool {
	switch strings.ToUpper(s) {
	case "WHERE", "JOIN", "INNER", "LEFT", "RIGHT", "ON", "AND", "OR":
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
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.acceptKeyword("AND") {
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "AND", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	t := p.cur()
	if t.kind == tokSymbol {
		switch t.val {
		case "=", ">", "<", ">=", "<=", "<>", "!=":
			p.next()
			right, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			return &BinaryExpr{Op: t.val, Left: left, Right: right}, nil
		}
	}
	return left, nil
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.cur()
	switch t.kind {
	case tokNumber:
		p.next()
		return &Literal{Kind: LitNumber, Value: t.val}, nil
	case tokString:
		p.next()
		return &Literal{Kind: LitString, Value: t.val}, nil
	case tokIdent:
		p.next()
		if p.acceptSymbol(".") {
			if p.acceptSymbol("*") {
				return &Star{Table: t.val}, nil
			}
			name, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			return &ColumnRef{Table: t.val, Name: name}, nil
		}
		return &ColumnRef{Name: t.val}, nil
	case tokSymbol:
		if t.val == "*" {
			p.next()
			return &Star{}, nil
		}
		if t.val == "(" {
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
	}
	return nil, fmt.Errorf("unexpected token %q", t.val)
}
