package fold

import (
	"fmt"
	"strconv"
	"strings"
)

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tNum
	tStr
	tOp
	tLParen
	tRParen
	tComma
)

type token struct {
	kind tokKind
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
			toks = append(toks, token{tLParen, "("})
			i++
		case c == ')':
			toks = append(toks, token{tRParen, ")"})
			i++
		case c == ',':
			toks = append(toks, token{tComma, ","})
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
			toks = append(toks, token{tStr, sb.String()})
			i = j + 1
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.') {
				j++
			}
			toks = append(toks, token{tNum, src[i:j]})
			i = j
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(src) && (src[j] == '_' || src[j] == '.' ||
				src[j] >= '0' && src[j] <= '9' ||
				src[j] >= 'a' && src[j] <= 'z' ||
				src[j] >= 'A' && src[j] <= 'Z') {
				j++
			}
			toks = append(toks, token{tIdent, src[i:j]})
			i = j
		case c == '+' || c == '-' || c == '*' || c == '/' || c == '%' || c == '=':
			toks = append(toks, token{tOp, string(c)})
			i++
		case c == '!':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{tOp, "!="})
				i += 2
			} else {
				return nil, fmt.Errorf("unexpected character '!'")
			}
		case c == '<':
			if i+1 < len(src) && (src[i+1] == '=' || src[i+1] == '>') {
				toks = append(toks, token{tOp, src[i : i+2]})
				i += 2
			} else {
				toks = append(toks, token{tOp, "<"})
				i++
			}
		case c == '>':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{tOp, ">="})
				i += 2
			} else {
				toks = append(toks, token{tOp, ">"})
				i++
			}
		default:
			return nil, fmt.Errorf("unexpected character %q", c)
		}
	}
	toks = append(toks, token{tEOF, ""})
	return toks, nil
}

type parser struct {
	toks []token
	pos  int
}

// Parse parses a SQL expression into an AST.
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
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected trailing token %q", p.peek().text)
	}
	return e, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) eatKeyword(kw string) bool {
	t := p.peek()
	if t.kind == tIdent && strings.EqualFold(t.text, kw) {
		p.pos++
		return true
	}
	return false
}

func (p *parser) eatOp(op string) bool {
	t := p.peek()
	if t.kind == tOp && t.text == op {
		p.pos++
		return true
	}
	return false
}

func (p *parser) parseOr() (Expr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.eatKeyword("OR") {
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = &Bin{Op: "OR", L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseAnd() (Expr, error) {
	l, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.eatKeyword("AND") {
		r, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		l = &Bin{Op: "AND", L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseNot() (Expr, error) {
	if p.eatKeyword("NOT") {
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: "NOT", X: x}, nil
	}
	return p.parseCmp()
}

func (p *parser) parseCmp() (Expr, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	t := p.peek()
	if t.kind == tOp {
		switch t.text {
		case "=", "!=", "<>", "<", "<=", ">", ">=":
			p.next()
			r, err := p.parseAdd()
			if err != nil {
				return nil, err
			}
			return &Bin{Op: t.text, L: l, R: r}, nil
		}
	}
	return l, nil
}

func (p *parser) parseAdd() (Expr, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for {
		if p.eatOp("+") {
			r, err := p.parseMul()
			if err != nil {
				return nil, err
			}
			l = &Bin{Op: "+", L: l, R: r}
		} else if p.eatOp("-") {
			r, err := p.parseMul()
			if err != nil {
				return nil, err
			}
			l = &Bin{Op: "-", L: l, R: r}
		} else {
			return l, nil
		}
	}
}

func (p *parser) parseMul() (Expr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		if p.eatOp("*") {
			r, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			l = &Bin{Op: "*", L: l, R: r}
		} else if p.eatOp("/") {
			r, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			l = &Bin{Op: "/", L: l, R: r}
		} else if p.eatOp("%") {
			r, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			l = &Bin{Op: "%", L: l, R: r}
		} else {
			return l, nil
		}
	}
}

func (p *parser) parseUnary() (Expr, error) {
	if p.eatOp("-") {
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: "-", X: x}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.next()
	switch t.kind {
	case tNum:
		if strings.Contains(t.text, ".") {
			f, err := strconv.ParseFloat(t.text, 64)
			if err != nil {
				return nil, err
			}
			return &Lit{V: f}, nil
		}
		n, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			return nil, err
		}
		return &Lit{V: n}, nil
	case tStr:
		return &Lit{V: t.text}, nil
	case tLParen:
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tRParen {
			return nil, fmt.Errorf("expected ')'")
		}
		p.next()
		return e, nil
	case tIdent:
		switch {
		case strings.EqualFold(t.text, "TRUE"):
			return &Lit{V: true}, nil
		case strings.EqualFold(t.text, "FALSE"):
			return &Lit{V: false}, nil
		case strings.EqualFold(t.text, "INTERVAL"):
			qty, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			u := p.next()
			if u.kind != tIdent {
				return nil, fmt.Errorf("expected interval unit, got %q", u.text)
			}
			return &Interval{Qty: qty, Unit: strings.ToUpper(u.text)}, nil
		}
		if p.peek().kind == tLParen {
			p.next()
			var args []Expr
			if p.peek().kind != tRParen {
				for {
					a, err := p.parseOr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.peek().kind == tComma {
						p.next()
						continue
					}
					break
				}
			}
			if p.peek().kind != tRParen {
				return nil, fmt.Errorf("expected ')' in call to %s", t.text)
			}
			p.next()
			return &Call{Name: strings.ToUpper(t.text), Args: args}, nil
		}
		return &Col{Name: t.text}, nil
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}
