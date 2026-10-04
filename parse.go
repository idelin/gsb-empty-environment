package cellengine

import (
	"fmt"
	"strings"
	"unicode"
)

// Formula grammar (a leading '=' is optional and stripped before parsing):
//
//	expr    := term   { ('+' | '-') term }
//	term    := factor { '*' factor }
//	factor  := '-' factor | primary
//	primary := INT | REF | '#ERR!' | 'IF(' expr ',' expr ',' expr ')' | '(' expr ')'
//
// REF is one or more ASCII letters followed by one or more digits (A1, BC12).
// IF and #ERR! are case-insensitive; cell references are upper-cased.

type expr interface{ isExpr() }

type numLit struct{ v int64 }
type refExpr struct{ id string }
type binExpr struct {
	op   byte // '+', '-' or '*'
	l, r expr
}
type ifExpr struct{ c, t, f expr }
type errExpr struct{}

func (numLit) isExpr()  {}
func (refExpr) isExpr() {}
func (binExpr) isExpr() {}
func (ifExpr) isExpr()  {}
func (errExpr) isExpr() {}

type tokKind int

const (
	tEOF tokKind = iota
	tNum
	tRef
	tIf
	tErrLit
	tLParen
	tRParen
	tComma
	tPlus
	tMinus
	tStar
)

type token struct {
	kind tokKind
	text string
	num  int64
}

func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			var n int64
			if _, err := fmt.Sscan(src[i:j], &n); err != nil {
				return nil, fmt.Errorf("bad number %q", src[i:j])
			}
			toks = append(toks, token{kind: tNum, text: src[i:j], num: n})
			i = j
		case c == '#':
			word := src[i:]
			if len(word) >= 4 && strings.EqualFold(word[:4], "#ERR") {
				end := i + 4
				if end < len(src) && src[end] == '!' {
					end++
				}
				toks = append(toks, token{kind: tErrLit, text: src[i:end]})
				i = end
			} else {
				return nil, fmt.Errorf("bad literal at %q", word)
			}
		case unicode.IsLetter(rune(c)):
			j := i
			for j < len(src) && unicode.IsLetter(rune(src[j])) {
				j++
			}
			word := src[i:j]
			k := j
			for k < len(src) && src[k] >= '0' && src[k] <= '9' {
				k++
			}
			if k > j {
				// letters followed by digits: a cell reference
				toks = append(toks, token{kind: tRef, text: strings.ToUpper(src[i:k])})
				i = k
			} else if strings.EqualFold(word, "IF") {
				toks = append(toks, token{kind: tIf, text: word})
				i = j
			} else {
				return nil, fmt.Errorf("unknown name %q", word)
			}
		case c == '(':
			toks = append(toks, token{kind: tLParen, text: "("})
			i++
		case c == ')':
			toks = append(toks, token{kind: tRParen, text: ")"})
			i++
		case c == ',':
			toks = append(toks, token{kind: tComma, text: ","})
			i++
		case c == '+':
			toks = append(toks, token{kind: tPlus, text: "+"})
			i++
		case c == '-':
			toks = append(toks, token{kind: tMinus, text: "-"})
			i++
		case c == '*':
			toks = append(toks, token{kind: tStar, text: "*"})
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q", string(c))
		}
	}
	toks = append(toks, token{kind: tEOF})
	return toks, nil
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token       { return p.toks[p.pos] }
func (p *parser) next() token       { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) at(k tokKind) bool { return p.toks[p.pos].kind == k }

func (p *parser) expect(k tokKind, what string) error {
	if !p.at(k) {
		return fmt.Errorf("expected %s, got %q", what, p.peek().text)
	}
	p.pos++
	return nil
}

// Parse parses a formula source into an AST.
func Parse(src string) (expr, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if !p.at(tEOF) {
		return nil, fmt.Errorf("trailing input %q", p.peek().text)
	}
	return e, nil
}

func (p *parser) parseExpr() (expr, error) {
	l, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for p.at(tPlus) || p.at(tMinus) {
		op := byte('+')
		if p.at(tMinus) {
			op = '-'
		}
		p.next()
		r, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		l = binExpr{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseTerm() (expr, error) {
	l, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for p.at(tStar) {
		p.next()
		r, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		l = binExpr{op: '*', l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseFactor() (expr, error) {
	if p.at(tMinus) {
		p.next()
		f, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return binExpr{op: '-', l: numLit{v: 0}, r: f}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (expr, error) {
	t := p.peek()
	switch t.kind {
	case tNum:
		p.next()
		return numLit{v: t.num}, nil
	case tRef:
		p.next()
		return refExpr{id: t.text}, nil
	case tErrLit:
		p.next()
		return errExpr{}, nil
	case tIf:
		p.next()
		if err := p.expect(tLParen, "'(' after IF"); err != nil {
			return nil, err
		}
		c, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tComma, "','"); err != nil {
			return nil, err
		}
		then, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tComma, "','"); err != nil {
			return nil, err
		}
		els, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tRParen, "')'"); err != nil {
			return nil, err
		}
		return ifExpr{c: c, t: then, f: els}, nil
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
	return nil, fmt.Errorf("unexpected token %q", t.text)
}
