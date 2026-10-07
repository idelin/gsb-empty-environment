package uniparse

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

// ParseGrammar parses the line-oriented grammar format:
//
//	# comment
//	start S
//	S -> NP[num=N] VP[num=N]
//	NP[num=N] -> Det N[num=N]
//	N[num=sg] -> "dog"
//	N -> cat              # bare words that never head a rule are terminals
//
// A feature value is a constant, or a variable when its first letter is
// uppercase. Quoted strings are always terminals (on the right-hand side)
// or constants (in feature values). Variables are local to one rule.
func ParseGrammar(src string) (*Grammar, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &gparser{toks: toks}
	g := &Grammar{}
	var raws []rawRule
	lhsNames := map[string]bool{}
	startSet := false
	for p.peek().kind != tkEOF {
		if p.peek().kind == tkNL {
			p.pos++
			continue
		}
		t := p.peek()
		if t.kind != tkIdent {
			return nil, fmt.Errorf("uniparse: line %d: expected a symbol or directive", t.line)
		}
		if t.text == "start" {
			p.pos++
			n, err := p.expect(tkIdent, "start category")
			if err != nil {
				return nil, err
			}
			if startSet {
				return nil, fmt.Errorf("uniparse: line %d: duplicate start directive", n.line)
			}
			startSet = true
			g.Start = NT(n.text, nil)
			if err := p.endOfLine(); err != nil {
				return nil, err
			}
			continue
		}
		lhs, err := p.symbol()
		if err != nil {
			return nil, err
		}
		if lhs.quoted {
			return nil, fmt.Errorf("uniparse: line %d: terminal %q cannot head a rule", lhs.line, lhs.name)
		}
		if _, err := p.expect(tkArrow, "->"); err != nil {
			return nil, err
		}
		var rhs []rawSym
		for p.peek().kind != tkNL && p.peek().kind != tkEOF {
			s, err := p.symbol()
			if err != nil {
				return nil, err
			}
			rhs = append(rhs, s)
		}
		if len(rhs) == 0 {
			return nil, fmt.Errorf("uniparse: line %d: empty (epsilon) production for %s", lhs.line, lhs.name)
		}
		lhsNames[lhs.name] = true
		raws = append(raws, rawRule{lhs: lhs, rhs: rhs})
		if err := p.endOfLine(); err != nil {
			return nil, err
		}
	}
	for _, rr := range raws {
		rule := Rule{LHS: NT(rr.lhs.name, rr.lhs.feats)}
		for _, s := range rr.rhs {
			switch {
			case s.quoted:
				rule.RHS = append(rule.RHS, T(s.name))
			case lhsNames[s.name]:
				rule.RHS = append(rule.RHS, NT(s.name, s.feats))
			case len(s.feats) > 0:
				return nil, fmt.Errorf("uniparse: line %d: features on terminal %q", s.line, s.name)
			default:
				rule.RHS = append(rule.RHS, T(s.name))
			}
		}
		g.Rules = append(g.Rules, rule)
	}
	if startSet && !lhsNames[g.Start.Name] {
		return nil, fmt.Errorf("uniparse: start category %q heads no rule", g.Start.Name)
	}
	if err := g.validate(); err != nil {
		return nil, err
	}
	return g, nil
}

// ParseText parses a grammar from src and runs the parser on words.
func ParseText(src string, words []string) (*Forest, error) {
	g, err := ParseGrammar(src)
	if err != nil {
		return nil, err
	}
	return Parse(g, words)
}

type rawSym struct {
	name   string
	quoted bool
	feats  Feats
	line   int
}

type rawRule struct {
	lhs rawSym
	rhs []rawSym
}

const (
	tkEOF = iota
	tkNL
	tkIdent
	tkString
	tkArrow
	tkLBrack
	tkRBrack
	tkComma
	tkEq
)

type token struct {
	kind int
	text string
	line int
}

func lex(src string) ([]token, error) {
	var toks []token
	line := 1
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '\n':
			toks = append(toks, token{kind: tkNL, line: line})
			line++
			i++
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '"':
			j := i + 1
			for j < len(src) && src[j] != '"' && src[j] != '\n' {
				j++
			}
			if j >= len(src) || src[j] != '"' {
				return nil, fmt.Errorf("uniparse: line %d: unterminated string", line)
			}
			toks = append(toks, token{kind: tkString, text: src[i+1 : j], line: line})
			i = j + 1
		case c == '-' && i+1 < len(src) && src[i+1] == '>':
			toks = append(toks, token{kind: tkArrow, line: line})
			i += 2
		case c == '[':
			toks = append(toks, token{kind: tkLBrack, line: line})
			i++
		case c == ']':
			toks = append(toks, token{kind: tkRBrack, line: line})
			i++
		case c == ',':
			toks = append(toks, token{kind: tkComma, line: line})
			i++
		case c == '=':
			toks = append(toks, token{kind: tkEq, line: line})
			i++
		default:
			j := i
			for j < len(src) && isIdentChar(src[j]) {
				j++
			}
			if j == i {
				return nil, fmt.Errorf("uniparse: line %d: unexpected character %q", line, c)
			}
			toks = append(toks, token{kind: tkIdent, text: src[i:j], line: line})
			i = j
		}
	}
	return append(toks, token{kind: tkEOF, line: line}), nil
}

func isIdentChar(c byte) bool {
	return c == '_' || c == '\'' ||
		c < utf8.RuneSelf && ('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9')
}

type gparser struct {
	toks []token
	pos  int
}

func (p *gparser) peek() token { return p.toks[p.pos] }

func (p *gparser) next() token {
	t := p.toks[p.pos]
	if t.kind != tkEOF {
		p.pos++
	}
	return t
}

func (p *gparser) expect(kind int, what string) (token, error) {
	t := p.next()
	if t.kind != kind {
		return t, fmt.Errorf("uniparse: line %d: expected %s", t.line, what)
	}
	return t, nil
}

func (p *gparser) endOfLine() error {
	if t := p.peek(); t.kind != tkNL && t.kind != tkEOF {
		return fmt.Errorf("uniparse: line %d: unexpected trailing input", t.line)
	}
	return nil
}

func (p *gparser) symbol() (rawSym, error) {
	t := p.next()
	switch t.kind {
	case tkString:
		return rawSym{name: t.text, quoted: true, line: t.line}, nil
	case tkIdent:
		s := rawSym{name: t.text, line: t.line}
		if p.peek().kind == tkLBrack {
			p.pos++
			feats, err := p.featList()
			if err != nil {
				return rawSym{}, err
			}
			s.feats = feats
		}
		return s, nil
	default:
		return rawSym{}, fmt.Errorf("uniparse: line %d: expected a symbol", t.line)
	}
}

func (p *gparser) featList() (Feats, error) {
	f := Feats{}
	for {
		k, err := p.expect(tkIdent, "feature key")
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tkEq, "="); err != nil {
			return nil, err
		}
		v := p.next()
		var val Value
		switch v.kind {
		case tkString:
			val = Const(v.text)
		case tkIdent:
			if isVarName(v.text) {
				val = Var(v.text)
			} else {
				val = Const(v.text)
			}
		default:
			return nil, fmt.Errorf("uniparse: line %d: expected a feature value", v.line)
		}
		if _, dup := f[k.text]; dup {
			return nil, fmt.Errorf("uniparse: line %d: duplicate feature key %q", k.line, k.text)
		}
		f[k.text] = val
		switch p.peek().kind {
		case tkComma:
			p.pos++
		case tkRBrack:
			p.pos++
			return f, nil
		default:
			return nil, fmt.Errorf("uniparse: line %d: expected ',' or ']'", p.peek().line)
		}
	}
}

func isVarName(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}
