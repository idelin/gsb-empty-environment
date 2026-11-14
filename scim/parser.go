package scim

import (
	"strconv"
	"strings"
)

// Parse parses a SCIM filter string into a syntax tree. It performs no
// authorization checks; use Policy.Validate on the returned tree.
func Parse(filter string) (Expr, error) {
	toks, err := tokenize(filter)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, newError(ErrCodeParse, "unexpected token %q after end of filter", p.peek().lit)
	}
	return expr, nil
}

type tokKind int

const (
	tokEOF tokKind = iota
	tokIdent
	tokString
	tokNumber
	tokLParen
	tokRParen
	tokLBracket
	tokRBracket
)

type token struct {
	kind tokKind
	lit  string
}

func isIdentChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	switch r {
	case '.', ':', '_', '-', '$':
		return true
	}
	return false
}

func tokenize(s string) ([]token, error) {
	var toks []token
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		r := runes[i]
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			i++
		case r == '(':
			toks = append(toks, token{tokLParen, "("})
			i++
		case r == ')':
			toks = append(toks, token{tokRParen, ")"})
			i++
		case r == '[':
			toks = append(toks, token{tokLBracket, "["})
			i++
		case r == ']':
			toks = append(toks, token{tokRBracket, "]"})
			i++
		case r == '"':
			str, next, err := scanString(runes, i+1)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{tokString, str})
			i = next
		case r >= '0' && r <= '9' || r == '-' && i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9':
			j := i
			for j < len(runes) && (runes[j] >= '0' && runes[j] <= '9' || runes[j] == '.' || runes[j] == '-' || runes[j] == '+' || runes[j] == 'e' || runes[j] == 'E') {
				j++
			}
			lit := string(runes[i:j])
			if _, err := strconv.ParseFloat(lit, 64); err != nil {
				return nil, newError(ErrCodeParse, "invalid number %q", lit)
			}
			toks = append(toks, token{tokNumber, lit})
			i = j
		case isIdentChar(r):
			j := i
			for j < len(runes) && isIdentChar(runes[j]) {
				j++
			}
			toks = append(toks, token{tokIdent, string(runes[i:j])})
			i = j
		default:
			return nil, newError(ErrCodeParse, "unexpected character %q", string(r))
		}
	}
	toks = append(toks, token{tokEOF, ""})
	return toks, nil
}

func scanString(runes []rune, i int) (string, int, error) {
	var b strings.Builder
	for i < len(runes) {
		r := runes[i]
		switch r {
		case '"':
			return b.String(), i + 1, nil
		case '\\':
			if i+1 >= len(runes) {
				return "", 0, newError(ErrCodeParse, "unterminated escape in string literal")
			}
			esc := runes[i+1]
			switch esc {
			case '"', '\\', '/':
				b.WriteRune(esc)
			case 'n':
				b.WriteRune('\n')
			case 't':
				b.WriteRune('\t')
			case 'r':
				b.WriteRune('\r')
			case 'b':
				b.WriteRune('\b')
			case 'f':
				b.WriteRune('\f')
			case 'u':
				if i+5 >= len(runes) {
					return "", 0, newError(ErrCodeParse, "truncated \\u escape in string literal")
				}
				hex := string(runes[i+2 : i+6])
				n, err := strconv.ParseUint(hex, 16, 32)
				if err != nil {
					return "", 0, newError(ErrCodeParse, "invalid \\u escape %q", hex)
				}
				b.WriteRune(rune(n))
				i += 4
			default:
				return "", 0, newError(ErrCodeParse, "invalid escape %q", string(esc))
			}
			i += 2
		default:
			b.WriteRune(r)
			i++
		}
	}
	return "", 0, newError(ErrCodeParse, "unterminated string literal")
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) backup()     { p.pos-- }

func (p *parser) peekKeyword(kw string) bool {
	t := p.peek()
	return t.kind == tokIdent && strings.EqualFold(t.lit, kw)
}

// parseOr: orExp = andExp ("or" andExp)*
func (p *parser) parseOr() (Expr, error) {
	first, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	args := []Expr{first}
	for p.peekKeyword("or") {
		p.next()
		e, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		args = append(args, e)
	}
	if len(args) == 1 {
		return first, nil
	}
	return &LogExpr{Op: LogOr, Args: args}, nil
}

// parseAnd: andExp = unary ("and" unary)*
func (p *parser) parseAnd() (Expr, error) {
	first, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	args := []Expr{first}
	for p.peekKeyword("and") {
		p.next()
		e, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		args = append(args, e)
	}
	if len(args) == 1 {
		return first, nil
	}
	return &LogExpr{Op: LogAnd, Args: args}, nil
}

// parseUnary: "not" "(" filter ")" | primary
func (p *parser) parseUnary() (Expr, error) {
	if p.peekKeyword("not") {
		p.next()
		if p.peek().kind != tokLParen {
			return nil, newError(ErrCodeParse, "expected \"(\" after \"not\"")
		}
		p.next()
		sub, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, newError(ErrCodeParse, "expected \")\" to close \"not(\"")
		}
		p.next()
		return &NotExpr{Sub: sub}, nil
	}
	return p.parsePrimary()
}

// parsePrimary: "(" filter ")" | valuePath | attrExp
func (p *parser) parsePrimary() (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tokLParen:
		p.next()
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, newError(ErrCodeParse, "expected \")\"")
		}
		p.next()
		return e, nil
	case tokIdent:
		return p.parseAttrExpr()
	default:
		return nil, newError(ErrCodeParse, "expected attribute, \"(\" or \"not\", got %q", t.lit)
	}
}

func (p *parser) parseAttrExpr() (Expr, error) {
	path := p.next().lit
	if path == "" {
		return nil, newError(ErrCodeParse, "empty attribute path")
	}
	// valuePath: attrPath "[" filter "]"
	if p.peek().kind == tokLBracket {
		p.next()
		sub, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRBracket {
			return nil, newError(ErrCodeParse, "expected \"]\" to close value path")
		}
		p.next()
		return &ValuePath{Path: path, Sub: sub}, nil
	}
	opTok := p.next()
	if opTok.kind != tokIdent {
		return nil, newError(ErrCodeParse, "expected operator after attribute %q", path)
	}
	op := Op(strings.ToLower(opTok.lit))
	switch op {
	case OpPr:
		return &AttrExpr{Path: path, Op: op}, nil
	case OpEq, OpNe, OpCo, OpSw, OpEw, OpGt, OpLt, OpGe, OpLe:
		val, err := p.parseCompValue()
		if err != nil {
			return nil, err
		}
		return &AttrExpr{Path: path, Op: op, Value: val}, nil
	default:
		return nil, newError(ErrCodeParse, "unknown operator %q", opTok.lit)
	}
}

func (p *parser) parseCompValue() (Value, error) {
	t := p.next()
	switch t.kind {
	case tokString:
		return Value{Kind: ValString, Str: t.lit}, nil
	case tokNumber:
		n, _ := strconv.ParseFloat(t.lit, 64)
		return Value{Kind: ValNumber, Num: n}, nil
	case tokIdent:
		switch strings.ToLower(t.lit) {
		case "true":
			return Value{Kind: ValBool, Bool: true}, nil
		case "false":
			return Value{Kind: ValBool, Bool: false}, nil
		case "null":
			return Value{Kind: ValNull}, nil
		}
		p.backup()
		return Value{}, newError(ErrCodeParse, "expected comparison value, got %q", t.lit)
	default:
		p.backup()
		return Value{}, newError(ErrCodeParse, "expected comparison value, got %q", t.lit)
	}
}
