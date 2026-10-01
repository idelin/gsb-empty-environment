package logplan

import (
	"fmt"
	"strings"
	"unicode"
)

// token kinds produced by the lexer.
type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokNumber
	tokString
	tokStar
	tokComma
	tokDot
	tokOp // comparison operator: = > < >= <= != <>
	tokLParen
	tokRParen
)

type token struct {
	kind tokenKind
	text string
}

var keywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true,
	"JOIN": true, "INNER": true, "ON": true, "AS": true,
	"AND": true, "OR": true,
}

func lex(input string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(input) {
		c := rune(input[i])
		switch {
		case unicode.IsSpace(c):
			i++
		case c == ',':
			toks = append(toks, token{tokComma, ","})
			i++
		case c == '.':
			toks = append(toks, token{tokDot, "."})
			i++
		case c == '*':
			toks = append(toks, token{tokStar, "*"})
			i++
		case c == '(':
			toks = append(toks, token{tokLParen, "("})
			i++
		case c == ')':
			toks = append(toks, token{tokRParen, ")"})
			i++
		case c == '=' || c == '>' || c == '<' || c == '!':
			j := i + 1
			if j < len(input) && (input[j] == '=' || input[j] == '>') {
				j++
			}
			op := input[i:j]
			if op == "!" {
				return nil, fmt.Errorf("unexpected character '!' at offset %d", i)
			}
			toks = append(toks, token{tokOp, op})
			i = j
		case c == '\'':
			j := i + 1
			for j < len(input) && input[j] != '\'' {
				j++
			}
			if j >= len(input) {
				return nil, fmt.Errorf("unterminated string literal at offset %d", i)
			}
			toks = append(toks, token{tokString, input[i : j+1]})
			i = j + 1
		case unicode.IsDigit(c):
			j := i
			for j < len(input) && (unicode.IsDigit(rune(input[j])) || input[j] == '.') {
				j++
			}
			toks = append(toks, token{tokNumber, input[i:j]})
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(input) && (unicode.IsLetter(rune(input[j])) || unicode.IsDigit(rune(input[j])) || input[j] == '_') {
				j++
			}
			word := input[i:j]
			upper := strings.ToUpper(word)
			if keywords[upper] {
				toks = append(toks, token{tokIdent, upper})
			} else {
				toks = append(toks, token{tokIdent, word})
			}
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q at offset %d", c, i)
		}
	}
	toks = append(toks, token{tokEOF, ""})
	return toks, nil
}

// parser is a recursive-descent parser for the SELECT core subset.
type parser struct {
	toks []token
	pos  int
}

// Parse parses a core SELECT statement into a SelectStmt AST.
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
	if p.peek().kind != tokEOF {
		return nil, fmt.Errorf("unexpected trailing input %q", p.peek().text)
	}
	return stmt, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) expect(text string) error {
	if p.peek().text != text {
		return fmt.Errorf("expected %q, got %q", text, p.peek().text)
	}
	p.pos++
	return nil
}

func (p *parser) expectIdent() (string, error) {
	t := p.peek()
	if t.kind != tokIdent || keywords[t.text] {
		return "", fmt.Errorf("expected identifier, got %q", t.text)
	}
	p.pos++
	return t.text, nil
}

func (p *parser) parseSelect() (*SelectStmt, error) {
	if err := p.expect("SELECT"); err != nil {
		return nil, err
	}
	stmt := &SelectStmt{}
	if p.peek().kind == tokStar {
		p.next()
		stmt.Star = true
	} else {
		for {
			item, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			stmt.Items = append(stmt.Items, item)
			if p.peek().kind != tokComma {
				break
			}
			p.next()
		}
	}
	if err := p.expect("FROM"); err != nil {
		return nil, err
	}
	from, err := p.parseTableRef()
	if err != nil {
		return nil, err
	}
	stmt.From = from
	for p.peek().text == "JOIN" || p.peek().text == "INNER" {
		if p.peek().text == "INNER" {
			p.next()
		}
		p.next() // JOIN
		tbl, err := p.parseTableRef()
		if err != nil {
			return nil, err
		}
		if err := p.expect("ON"); err != nil {
			return nil, err
		}
		on, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Joins = append(stmt.Joins, &JoinClause{Table: tbl, On: on})
	}
	if p.peek().text == "WHERE" {
		p.next()
		where, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Where = where
	}
	return stmt, nil
}

func (p *parser) parseTableRef() (*TableRef, error) {
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	ref := &TableRef{Name: name}
	if p.peek().text == "AS" {
		p.next()
		alias, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		ref.Alias = alias
	} else if t := p.peek(); t.kind == tokIdent && !keywords[t.text] {
		ref.Alias = p.next().text
	}
	return ref, nil
}

// parseExpr handles OR (lowest precedence).
func (p *parser) parseExpr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().text == "OR" {
		p.next()
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
	for p.peek().text == "AND" {
		p.next()
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
	if p.peek().kind == tokOp {
		op := p.next().text
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Op: op, Left: left, Right: right}, nil
	}
	return left, nil
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tokNumber, tokString:
		p.next()
		return &Literal{Value: t.text}, nil
	case tokLParen:
		p.next()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, fmt.Errorf("expected ')', got %q", p.peek().text)
		}
		p.next()
		return e, nil
	case tokIdent:
		if keywords[t.text] {
			return nil, fmt.Errorf("unexpected keyword %q in expression", t.text)
		}
		p.next()
		if p.peek().kind == tokDot {
			p.next()
			if p.peek().kind == tokStar {
				p.next()
				return &ColumnRef{Table: t.text, Name: "*"}, nil
			}
			col, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			return &ColumnRef{Table: t.text, Name: col}, nil
		}
		return &ColumnRef{Name: t.text}, nil
	}
	return nil, fmt.Errorf("unexpected token %q in expression", t.text)
}
