package promlite

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Restricted expression grammar (recursive descent, no PromQL server needed):
//
//	expr     := add
//	add      := mul (('+'|'-') matchMod? mul)*
//	mul      := postfix (('*'|'/') matchMod? postfix)*
//	matchMod := 'on' '(' labels ')' | 'ignoring' '(' labels ')'
//	postfix  := primary ('offset' DURATION)?
//	primary  := NUMBER
//	          | selector
//	          | 'rate' '(' selector '[' DURATION ']' ')'
//	          | 'sum' 'by' '(' labels ')' '(' expr ')'
//	          | '(' expr ')'
//	selector := IDENT ('{' IDENT '=' STRING (',' IDENT '=' STRING)* '}')?
//
// Deliberately unsupported: histogram fns, subqueries, group_left/right,
// comparison ops, set ops, regex matchers.

type Expr interface{ exprNode() }

type NumberLit struct{ V float64 }

type Selector struct {
	Metric   string
	Matchers []Matcher
}

type RateExpr struct {
	Sel    Selector
	Window time.Duration
}

type SumBy struct {
	Labels []string
	Expr   Expr
}

type OffsetExpr struct {
	Expr   Expr
	Offset time.Duration
}

type BinaryExpr struct {
	Op     string // + - * /
	L, R   Expr
	HasMod bool     // whether on()/ignoring() was given
	On     bool     // true: on(...); false: ignoring(...)
	Labels []string // matching labels
}

func (NumberLit) exprNode()  {}
func (Selector) exprNode()   {}
func (RateExpr) exprNode()   {}
func (SumBy) exprNode()      {}
func (OffsetExpr) exprNode() {}
func (BinaryExpr) exprNode() {}

// --- lexer ---

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tNumber
	tDuration
	tString
	tOp // + - * /
	tLParen
	tRParen
	tLBrace
	tRBrace
	tLBracket
	tRBracket
	tComma
	tEq
)

type token struct {
	kind tokKind
	text string
	pos  int
}

func isUnitStart(c byte) bool {
	return c == 's' || c == 'm' || c == 'h' || c == 'd' || c == 'w' || c == 'y'
}

func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case unicode.IsSpace(rune(c)):
			i++
		case c == '(':
			toks = append(toks, token{tLParen, "(", i})
			i++
		case c == ')':
			toks = append(toks, token{tRParen, ")", i})
			i++
		case c == '{':
			toks = append(toks, token{tLBrace, "{", i})
			i++
		case c == '}':
			toks = append(toks, token{tRBrace, "}", i})
			i++
		case c == '[':
			toks = append(toks, token{tLBracket, "[", i})
			i++
		case c == ']':
			toks = append(toks, token{tRBracket, "]", i})
			i++
		case c == ',':
			toks = append(toks, token{tComma, ",", i})
			i++
		case c == '=':
			toks = append(toks, token{tEq, "=", i})
			i++
		case c == '+' || c == '-' || c == '*' || c == '/':
			toks = append(toks, token{tOp, string(c), i})
			i++
		case c == '"':
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("unterminated string at pos %d", i)
			}
			toks = append(toks, token{tString, src[i : j+1], i})
			i = j + 1
		case c >= '0' && c <= '9' || c == '.':
			j := i
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.') {
				j++
			}
			if j < len(src) && isUnitStart(src[j]) {
				// duration literal, e.g. 5m, 1h30m, 90s
				for j < len(src) && (unicode.IsLetter(rune(src[j])) || src[j] >= '0' && src[j] <= '9') {
					j++
				}
				toks = append(toks, token{tDuration, src[i:j], i})
			} else {
				toks = append(toks, token{tNumber, src[i:j], i})
			}
			i = j
		case unicode.IsLetter(rune(c)) || c == '_' || c == ':':
			j := i
			for j < len(src) && (unicode.IsLetter(rune(src[j])) || unicode.IsDigit(rune(src[j])) || src[j] == '_' || src[j] == ':') {
				j++
			}
			toks = append(toks, token{tIdent, src[i:j], i})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q at pos %d", string(c), i)
		}
	}
	toks = append(toks, token{tEOF, "", len(src)})
	return toks, nil
}

// --- parser ---

type parser struct {
	toks []token
	pos  int
}

func Parse(src string) (Expr, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected trailing token %q at pos %d", p.peek().text, p.peek().pos)
	}
	return e, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) back()       { p.pos-- }

func (p *parser) expect(k tokKind, what string) (token, error) {
	t := p.next()
	if t.kind != k {
		return t, fmt.Errorf("expected %s, got %q at pos %d", what, t.text, t.pos)
	}
	return t, nil
}

func (p *parser) parseAdd() (Expr, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOp && (p.peek().text == "+" || p.peek().text == "-") {
		op := p.next().text
		r, err := p.parseBinaryTail(l, op, p.parseMul)
		if err != nil {
			return nil, err
		}
		l = r
	}
	return l, nil
}

func (p *parser) parseMul() (Expr, error) {
	l, err := p.parsePostfix()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOp && (p.peek().text == "*" || p.peek().text == "/") {
		op := p.next().text
		r, err := p.parseBinaryTail(l, op, p.parsePostfix)
		if err != nil {
			return nil, err
		}
		l = r
	}
	return l, nil
}

// parseBinaryTail parses an optional on()/ignoring() modifier then the RHS.
func (p *parser) parseBinaryTail(l Expr, op string, rhsFn func() (Expr, error)) (Expr, error) {
	be := BinaryExpr{Op: op, L: l}
	if p.peek().kind == tIdent && (p.peek().text == "on" || p.peek().text == "ignoring") {
		be.HasMod = true
		be.On = p.next().text == "on"
		labels, err := p.parseLabelList()
		if err != nil {
			return nil, err
		}
		be.Labels = labels
	}
	r, err := rhsFn()
	if err != nil {
		return nil, err
	}
	be.R = r
	return be, nil
}

func (p *parser) parseLabelList() ([]string, error) {
	if _, err := p.expect(tLParen, "'('"); err != nil {
		return nil, err
	}
	var labels []string
	for {
		t, err := p.expect(tIdent, "label name")
		if err != nil {
			return nil, err
		}
		labels = append(labels, t.text)
		if p.peek().kind == tComma {
			p.next()
			continue
		}
		break
	}
	if _, err := p.expect(tRParen, "')'"); err != nil {
		return nil, err
	}
	return labels, nil
}

func (p *parser) parsePostfix() (Expr, error) {
	e, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if p.peek().kind == tIdent && p.peek().text == "offset" {
		p.next()
		d, err := p.expect(tDuration, "duration after offset")
		if err != nil {
			return nil, err
		}
		dur, err := time.ParseDuration(d.text)
		if err != nil {
			return nil, fmt.Errorf("bad offset duration %q: %v", d.text, err)
		}
		if dur <= 0 {
			return nil, fmt.Errorf("offset must be positive, got %q", d.text)
		}
		return OffsetExpr{Expr: e, Offset: dur}, nil
	}
	return e, nil
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tNumber:
		p.next()
		v, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, fmt.Errorf("bad number %q", t.text)
		}
		return NumberLit{V: v}, nil
	case tLParen:
		p.next()
		e, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tRParen, "')'"); err != nil {
			return nil, err
		}
		return e, nil
	case tIdent:
		switch t.text {
		case "rate":
			return p.parseRate()
		case "sum":
			return p.parseSumBy()
		default:
			return p.parseSelector()
		}
	}
	return nil, fmt.Errorf("unexpected token %q at pos %d", t.text, t.pos)
}

func (p *parser) parseRate() (Expr, error) {
	p.next() // rate
	if _, err := p.expect(tLParen, "'(' after rate"); err != nil {
		return nil, err
	}
	sel, err := p.parseSelector()
	if err != nil {
		return nil, err
	}
	s, ok := sel.(Selector)
	if !ok {
		return nil, fmt.Errorf("rate() requires a plain metric selector")
	}
	if _, err := p.expect(tLBracket, "'[' before range"); err != nil {
		return nil, err
	}
	d, err := p.expect(tDuration, "range duration")
	if err != nil {
		return nil, err
	}
	dur, err := time.ParseDuration(d.text)
	if err != nil {
		return nil, fmt.Errorf("bad range duration %q: %v", d.text, err)
	}
	if dur <= 0 {
		return nil, fmt.Errorf("range duration must be positive, got %q", d.text)
	}
	if _, err := p.expect(tRBracket, "']'"); err != nil {
		return nil, err
	}
	if _, err := p.expect(tRParen, "')'"); err != nil {
		return nil, err
	}
	return RateExpr{Sel: s, Window: dur}, nil
}

func (p *parser) parseSumBy() (Expr, error) {
	p.next() // sum
	t, err := p.expect(tIdent, "'by' after sum")
	if err != nil {
		return nil, err
	}
	if t.text != "by" {
		return nil, fmt.Errorf("expected 'by' after sum, got %q", t.text)
	}
	labels, err := p.parseLabelList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tLParen, "'(' before aggregated expression"); err != nil {
		return nil, err
	}
	e, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tRParen, "')'"); err != nil {
		return nil, err
	}
	return SumBy{Labels: labels, Expr: e}, nil
}

func (p *parser) parseSelector() (Expr, error) {
	name, err := p.expect(tIdent, "metric name")
	if err != nil {
		return nil, err
	}
	sel := Selector{Metric: name.text}
	if p.peek().kind != tLBrace {
		return sel, nil
	}
	p.next()
	for {
		ln, err := p.expect(tIdent, "label name")
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tEq, "'='"); err != nil {
			return nil, err
		}
		lv, err := p.expect(tString, "quoted label value")
		if err != nil {
			return nil, err
		}
		v, err := strconv.Unquote(lv.text)
		if err != nil {
			return nil, fmt.Errorf("bad label value %q", lv.text)
		}
		sel.Matchers = append(sel.Matchers, Matcher{Name: ln.text, Value: v})
		if p.peek().kind == tComma {
			p.next()
			continue
		}
		break
	}
	if _, err := p.expect(tRBrace, "'}'"); err != nil {
		return nil, err
	}
	return sel, nil
}

// Describe renders an expression back to a canonical string (for traces).
func Describe(e Expr) string {
	switch n := e.(type) {
	case NumberLit:
		return strconv.FormatFloat(n.V, 'g', -1, 64)
	case Selector:
		var b strings.Builder
		b.WriteString(n.Metric)
		if len(n.Matchers) > 0 {
			b.WriteByte('{')
			for i, m := range n.Matchers {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "%s=%q", m.Name, m.Value)
			}
			b.WriteByte('}')
		}
		return b.String()
	case RateExpr:
		return fmt.Sprintf("rate(%s[%s])", Describe(n.Sel), n.Window)
	case SumBy:
		return fmt.Sprintf("sum by (%s) (%s)", strings.Join(n.Labels, ", "), Describe(n.Expr))
	case OffsetExpr:
		return fmt.Sprintf("%s offset %s", Describe(n.Expr), n.Offset)
	case BinaryExpr:
		mod := ""
		if n.HasMod {
			kw := "ignoring"
			if n.On {
				kw = "on"
			}
			mod = fmt.Sprintf(" %s(%s)", kw, strings.Join(n.Labels, ", "))
		}
		return fmt.Sprintf("%s %s%s %s", describeOperand(n.L), n.Op, mod, describeOperand(n.R))
	}
	return "?"
}

// describeOperand parenthesizes nested binary expressions so the rendered
// form preserves the original grouping.
func describeOperand(e Expr) string {
	if _, ok := e.(BinaryExpr); ok {
		return "(" + Describe(e) + ")"
	}
	return Describe(e)
}
