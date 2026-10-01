package constfold

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ---- lexer ----

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokNumber
	tokString
	tokOp
	tokLParen
	tokRParen
	tokComma
)

type token struct {
	kind tokenKind
	text string
}

func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, token{tokLParen, "("})
			i++
		case c == ')':
			toks = append(toks, token{tokRParen, ")"})
			i++
		case c == ',':
			toks = append(toks, token{tokComma, ","})
			i++
		case c == '\'':
			j := i + 1
			var sb strings.Builder
			for j < len(src) {
				if src[j] == '\'' {
					if j+1 < len(src) && src[j+1] == '\'' {
						sb.WriteByte('\'')
						j += 2
						continue
					}
					break
				}
				sb.WriteByte(src[j])
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			toks = append(toks, token{tokString, sb.String()})
			i = j + 1
		case unicode.IsDigit(rune(c)) || (c == '.' && i+1 < len(src) && unicode.IsDigit(rune(src[i+1]))):
			j := i
			for j < len(src) && (unicode.IsDigit(rune(src[j])) || src[j] == '.') {
				j++
			}
			toks = append(toks, token{tokNumber, src[i:j]})
			i = j
		case unicode.IsLetter(rune(c)) || c == '_':
			j := i
			for j < len(src) && (unicode.IsLetter(rune(src[j])) || unicode.IsDigit(rune(src[j])) || src[j] == '_' || src[j] == '.') {
				j++
			}
			toks = append(toks, token{tokIdent, src[i:j]})
			i = j
		case strings.ContainsRune("+-*/%=<>!", rune(c)):
			j := i
			for j < len(src) && strings.ContainsRune("+-*/%=<>!", rune(src[j])) {
				j++
			}
			toks = append(toks, token{tokOp, src[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q at offset %d", c, i)
		}
	}
	toks = append(toks, token{tokEOF, ""})
	return toks, nil
}

// ---- parser (recursive descent) ----
//
// Grammar (lowest to highest precedence):
//   orExpr     := andExpr ( OR andExpr )*
//   andExpr    := notExpr ( AND notExpr )*
//   notExpr    := NOT notExpr | comparison
//   comparison := additive ( (=|!=|<>|<|<=|>|>=) additive )?
//   additive   := multiplicative ( (+|-) multiplicative )*
//   multExpr   := unary ( (*|/|%) unary )*
//   unary      := - unary | primary
//   primary    := NUMBER | STRING | TRUE | FALSE | NULL
//               | INTERVAL primary UNIT
//               | ident ( .ident )* [ ( args ) ]
//               | ( orExpr )

type parser struct {
	toks []token
	pos  int
}

// Parse parses an expression string into an AST.
func Parse(src string) (Expr, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, fmt.Errorf("unexpected trailing token %q", p.peek().text)
	}
	return e, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) matchKeyword(kw string) bool {
	t := p.peek()
	if t.kind == tokIdent && strings.EqualFold(t.text, kw) {
		p.pos++
		return true
	}
	return false
}

func (p *parser) matchOp(ops ...string) string {
	t := p.peek()
	if t.kind == tokOp {
		for _, op := range ops {
			if t.text == op {
				p.pos++
				return op
			}
		}
	}
	return ""
}

func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.matchKeyword("OR") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: "OR", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.matchKeyword("AND") {
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: "AND", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseNot() (Expr, error) {
	if p.matchKeyword("NOT") {
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: "NOT", X: x}, nil
	}
	return p.parseComparison()
}

func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}
	if op := p.matchOp("=", "!=", "<>", "<", "<=", ">", ">="); op != "" {
		right, err := p.parseAdditive()
		if err != nil {
			return nil, err
		}
		if op == "<>" {
			op = "!="
		}
		return &Binary{Op: op, Left: left, Right: right}, nil
	}
	return left, nil
}

func (p *parser) parseAdditive() (Expr, error) {
	left, err := p.parseMult()
	if err != nil {
		return nil, err
	}
	for {
		op := p.matchOp("+", "-")
		if op == "" {
			return left, nil
		}
		right, err := p.parseMult()
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: op, Left: left, Right: right}
	}
}

func (p *parser) parseMult() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		op := p.matchOp("*", "/", "%")
		if op == "" {
			return left, nil
		}
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: op, Left: left, Right: right}
	}
}

func (p *parser) parseUnary() (Expr, error) {
	if op := p.matchOp("-"); op != "" {
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: "-", X: x}, nil
	}
	return p.parsePrimary()
}

var intervalUnits = map[string]bool{
	"DAY": true, "HOUR": true, "MINUTE": true, "SECOND": true,
	"MONTH": true, "YEAR": true, "WEEK": true,
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tokNumber:
		p.next()
		if strings.Contains(t.text, ".") {
			f, err := strconv.ParseFloat(t.text, 64)
			if err != nil {
				return nil, fmt.Errorf("bad number %q", t.text)
			}
			return &Literal{Kind: KindFloat, Flt: f}, nil
		}
		n, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad number %q", t.text)
		}
		return &Literal{Kind: KindInt, Int: n}, nil
	case tokString:
		p.next()
		return &Literal{Kind: KindString, Str: t.text}, nil
	case tokLParen:
		p.next()
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, fmt.Errorf("expected )")
		}
		p.next()
		return e, nil
	case tokIdent:
		switch strings.ToUpper(t.text) {
		case "TRUE":
			p.next()
			return BoolLit(true), nil
		case "FALSE":
			p.next()
			return BoolLit(false), nil
		case "NULL":
			p.next()
			return &Literal{Kind: KindNull}, nil
		case "INTERVAL":
			p.next()
			v, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			ut := p.peek()
			if ut.kind != tokIdent || !intervalUnits[strings.ToUpper(ut.text)] {
				return nil, fmt.Errorf("expected interval unit, got %q", ut.text)
			}
			p.next()
			return &Interval{Value: v, Unit: strings.ToUpper(ut.text)}, nil
		}
		p.next()
		name := t.text
		if p.peek().kind == tokLParen {
			p.next()
			var args []Expr
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
				return nil, fmt.Errorf("expected ) in call to %s", name)
			}
			p.next()
			return &FuncCall{Name: strings.ToUpper(name), Args: args}, nil
		}
		return &Column{Name: name}, nil
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}
