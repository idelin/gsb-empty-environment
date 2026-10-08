package configeval

import (
	"fmt"
	"strings"
)

// nodeKind classifies AST nodes.
type nodeKind int

const (
	nNum nodeKind = iota
	nStr
	nBool
	nRefVar // reference to another config variable
	nRefEnv // reference to the explicit environment snapshot
	nBin    // binary arithmetic / concatenation
	nInterp // string template with embedded references
)

type node struct {
	kind    nodeKind
	num     float64
	str     string
	boolean bool
	op      byte // '+', '-', '*', '/'
	left    *node
	right   *node
	parts   []*node // nInterp: sequence of nStr / nRefVar / nRefEnv
}

// Expr is a parsed, immutable expression ready for evaluation.
type Expr struct {
	root *node
	src  string
}

// Source returns the original expression text.
func (e *Expr) Source() string { return e.src }

// ParseExpr compiles src into an Expr.
//
// Grammar:
//
//	expr    := term (('+' | '-') term)*
//	term    := factor (('*' | '/') factor)*
//	factor  := NUMBER | STRING | 'true' | 'false' | ref | '(' expr ')'
//	ref     := '${' ident '}' | '${' 'env' '.' ident '}'
//
// STRING literals may embed refs, e.g. "host=${env.HOST}".
func ParseExpr(src string) (*Expr, error) {
	p := &parser{src: src}
	root, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.src) {
		return nil, fmt.Errorf("configeval: unexpected trailing input at offset %d in %q", p.pos, src)
	}
	return &Expr{root: root, src: src}, nil
}

type parser struct {
	src string
	pos int
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t' || p.src[p.pos] == '\n') {
		p.pos++
	}
}

func (p *parser) parseExpr() (*node, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.src) || (p.src[p.pos] != '+' && p.src[p.pos] != '-') {
			return left, nil
		}
		op := p.src[p.pos]
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = &node{kind: nBin, op: op, left: left, right: right}
	}
}

func (p *parser) parseTerm() (*node, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.src) || (p.src[p.pos] != '*' && p.src[p.pos] != '/') {
			return left, nil
		}
		op := p.src[p.pos]
		p.pos++
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		left = &node{kind: nBin, op: op, left: left, right: right}
	}
}

func (p *parser) parseFactor() (*node, error) {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return nil, fmt.Errorf("configeval: unexpected end of expression %q", p.src)
	}
	c := p.src[p.pos]
	switch {
	case c == '(':
		p.pos++
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.pos >= len(p.src) || p.src[p.pos] != ')' {
			return nil, fmt.Errorf("configeval: missing ')' in %q", p.src)
		}
		p.pos++
		return inner, nil
	case c == '$':
		return p.parseRef()
	case c == '"' || c == '\'':
		return p.parseString()
	case c == '-' || (c >= '0' && c <= '9') || c == '.':
		return p.parseNumber()
	case isIdentStart(c):
		word := p.readIdent()
		switch word {
		case "true":
			return &node{kind: nBool, boolean: true}, nil
		case "false":
			return &node{kind: nBool, boolean: false}, nil
		}
		return nil, fmt.Errorf("configeval: unknown identifier %q in %q", word, p.src)
	}
	return nil, fmt.Errorf("configeval: unexpected character %q in %q", string(c), p.src)
}

func (p *parser) parseNumber() (*node, error) {
	start := p.pos
	if p.src[p.pos] == '-' {
		p.pos++
	}
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if (c >= '0' && c <= '9') || c == '.' {
			p.pos++
			continue
		}
		break
	}
	text := p.src[start:p.pos]
	var f float64
	if _, err := fmt.Sscanf(text, "%g", &f); err != nil {
		return nil, fmt.Errorf("configeval: invalid number %q in %q", text, p.src)
	}
	return &node{kind: nNum, num: f}, nil
}

// parseString reads a quoted literal and splits it into interpolation parts.
func (p *parser) parseString() (*node, error) {
	quote := p.src[p.pos]
	p.pos++
	var raw strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '\\' && p.pos+1 < len(p.src) {
			raw.WriteByte(p.src[p.pos+1])
			p.pos += 2
			continue
		}
		if c == quote {
			p.pos++
			return buildStringNode(raw.String())
		}
		raw.WriteByte(c)
		p.pos++
	}
	return nil, fmt.Errorf("configeval: unterminated string in %q", p.src)
}

// buildStringNode splits raw text on ${...} references.
func buildStringNode(raw string) (*node, error) {
	var parts []*node
	for len(raw) > 0 {
		i := strings.Index(raw, "${")
		if i < 0 {
			parts = append(parts, &node{kind: nStr, str: raw})
			break
		}
		if i > 0 {
			parts = append(parts, &node{kind: nStr, str: raw[:i]})
		}
		j := strings.Index(raw[i:], "}")
		if j < 0 {
			return nil, fmt.Errorf("configeval: unterminated reference in string %q", raw)
		}
		ref, err := refFromName(raw[i+2 : i+j])
		if err != nil {
			return nil, err
		}
		parts = append(parts, ref)
		raw = raw[i+j+1:]
	}
	if len(parts) == 1 && parts[0].kind == nStr {
		return parts[0], nil
	}
	return &node{kind: nInterp, parts: parts}, nil
}

func (p *parser) parseRef() (*node, error) {
	if p.pos+1 >= len(p.src) || p.src[p.pos+1] != '{' {
		return nil, fmt.Errorf("configeval: expected '{' after '$' in %q", p.src)
	}
	end := strings.Index(p.src[p.pos:], "}")
	if end < 0 {
		return nil, fmt.Errorf("configeval: unterminated reference in %q", p.src)
	}
	name := p.src[p.pos+2 : p.pos+end]
	p.pos += end + 1
	return refFromName(name)
}

func refFromName(name string) (*node, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("configeval: empty reference")
	}
	if rest, ok := strings.CutPrefix(name, "env."); ok {
		if !isIdent(rest) {
			return nil, fmt.Errorf("configeval: invalid environment reference %q", name)
		}
		return &node{kind: nRefEnv, str: rest}, nil
	}
	if !isIdent(name) {
		return nil, fmt.Errorf("configeval: invalid reference %q", name)
	}
	return &node{kind: nRefVar, str: name}, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdent(s string) bool {
	if s == "" || !isIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !isIdentStart(c) && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func (p *parser) readIdent() string {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if !isIdentStart(c) && !(c >= '0' && c <= '9') {
			break
		}
		p.pos++
	}
	return p.src[start:p.pos]
}
