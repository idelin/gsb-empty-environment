package sheetengine

import (
	"fmt"
	"strings"
	"unicode"
)

// extractRefs 解析公式文本，返回其中引用的单元格名（去重、按出现顺序）。
// 公式语法：数字、单元格引用（如 A1、BC12）、+ - * /、括号、一元负号。
// 前导 "=" 可选。
func extractRefs(formula string) ([]string, error) {
	tokens, err := tokenize(formula)
	if err != nil {
		return nil, err
	}
	// 语法校验（顺便确认引用之外的 token 合法）。
	p := &parser{tokens: tokens}
	if _, err := p.parseExpr(); err != nil {
		return nil, err
	}
	if p.pos != len(p.tokens) {
		return nil, fmt.Errorf("sheetengine: unexpected token %q in formula %q", p.tokens[p.pos].text, formula)
	}

	seen := make(map[string]bool)
	var refs []string
	for _, t := range tokens {
		if t.kind == tokRef && !seen[t.text] {
			seen[t.text] = true
			refs = append(refs, t.text)
		}
	}
	return refs, nil
}

// evalFormula 用给定的单元格取值表对公式求值。
func evalFormula(formula string, values map[string]float64) (float64, error) {
	tokens, err := tokenize(formula)
	if err != nil {
		return 0, err
	}
	p := &parser{tokens: tokens, values: values}
	v, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	if p.pos != len(p.tokens) {
		return 0, fmt.Errorf("sheetengine: unexpected token %q", p.tokens[p.pos].text)
	}
	return v, nil
}

type tokenKind int

const (
	tokNumber tokenKind = iota
	tokRef
	tokOp
	tokLParen
	tokRParen
)

type token struct {
	kind tokenKind
	text string
}

func tokenize(formula string) ([]token, error) {
	s := strings.TrimSpace(formula)
	s = strings.TrimPrefix(s, "=")
	var tokens []token
	i := 0
	for i < len(s) {
		c := rune(s[i])
		switch {
		case unicode.IsSpace(c):
			i++
		case unicode.IsDigit(c) || c == '.':
			j := i
			for j < len(s) && (unicode.IsDigit(rune(s[j])) || s[j] == '.') {
				j++
			}
			tokens = append(tokens, token{tokNumber, s[i:j]})
			i = j
		case unicode.IsLetter(c):
			j := i
			for j < len(s) && unicode.IsLetter(rune(s[j])) {
				j++
			}
			k := j
			for k < len(s) && unicode.IsDigit(rune(s[k])) {
				k++
			}
			if k == j {
				return nil, fmt.Errorf("sheetengine: cell reference %q missing row number", s[i:j])
			}
			tokens = append(tokens, token{tokRef, strings.ToUpper(s[i:k])})
			i = k
		case c == '+' || c == '-' || c == '*' || c == '/':
			tokens = append(tokens, token{tokOp, string(c)})
			i++
		case c == '(':
			tokens = append(tokens, token{tokLParen, "("})
			i++
		case c == ')':
			tokens = append(tokens, token{tokRParen, ")"})
			i++
		default:
			return nil, fmt.Errorf("sheetengine: invalid character %q in formula", string(c))
		}
	}
	return tokens, nil
}

type parser struct {
	tokens []token
	pos    int
	values map[string]float64 // 为 nil 时仅做语法校验
}

func (p *parser) peek() *token {
	if p.pos < len(p.tokens) {
		return &p.tokens[p.pos]
	}
	return nil
}

// expr := term { ('+'|'-') term }
func (p *parser) parseExpr() (float64, error) {
	v, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		t := p.peek()
		if t == nil || t.kind != tokOp || (t.text != "+" && t.text != "-") {
			return v, nil
		}
		p.pos++
		rhs, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if t.text == "+" {
			v += rhs
		} else {
			v -= rhs
		}
	}
}

// term := factor { ('*'|'/') factor }
func (p *parser) parseTerm() (float64, error) {
	v, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		t := p.peek()
		if t == nil || t.kind != tokOp || (t.text != "*" && t.text != "/") {
			return v, nil
		}
		p.pos++
		rhs, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		if t.text == "*" {
			v *= rhs
		} else {
			if rhs == 0 {
				return 0, fmt.Errorf("sheetengine: division by zero")
			}
			v /= rhs
		}
	}
}

// factor := number | ref | '(' expr ')' | '-' factor
func (p *parser) parseFactor() (float64, error) {
	t := p.peek()
	if t == nil {
		return 0, fmt.Errorf("sheetengine: unexpected end of formula")
	}
	switch t.kind {
	case tokNumber:
		p.pos++
		var v float64
		if _, err := fmt.Sscanf(t.text, "%g", &v); err != nil {
			return 0, fmt.Errorf("sheetengine: bad number %q", t.text)
		}
		return v, nil
	case tokRef:
		p.pos++
		if p.values == nil {
			return 0, nil // 仅语法校验
		}
		return p.values[t.text], nil
	case tokLParen:
		p.pos++
		v, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		closing := p.peek()
		if closing == nil || closing.kind != tokRParen {
			return 0, fmt.Errorf("sheetengine: missing closing parenthesis")
		}
		p.pos++
		return v, nil
	case tokOp:
		if t.text == "-" {
			p.pos++
			v, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			return -v, nil
		}
	}
	return 0, fmt.Errorf("sheetengine: unexpected token %q", t.text)
}
