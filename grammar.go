package fparse

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits required by the task: at most 20 productions and 20 words.
const (
	MaxRules = 20
	MaxWords = 20
)

// Symbol is a grammar symbol with an optional feature map. Terminals
// (surface words) carry no features.
type Symbol struct {
	Name     string
	Features FeatureMap
	Terminal bool
}

// Rule is a single production. Variables appearing in Features are
// rule-local: they are renamed fresh on every instantiation of the rule.
type Rule struct {
	ID  int
	LHS Symbol
	RHS []Symbol
}

// Grammar is a set of at most MaxRules epsilon-free productions.
type Grammar struct {
	Rules []*Rule
}

// ParseGrammar parses a grammar from its text form, one rule per line:
//
//	S[mode=M] -> NP[case=nom num=N] VP[num=N mode=M]
//	NP -> Det N
//	N -> "dog"
//
// Feature pairs inside brackets are separated by spaces or commas.
// Values starting with an uppercase letter are rule-local variables;
// anything else is a constant. '#' starts a comment. At most MaxRules
// non-empty productions are allowed and empty (epsilon) right-hand sides
// are rejected.
func ParseGrammar(src string) (*Grammar, error) {
	var rules []*Rule
	for lineno, raw := range strings.Split(src, "\n") {
		line := raw
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		r, err := parseRule(line, len(rules))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineno+1, err)
		}
		rules = append(rules, r)
		if len(rules) > MaxRules {
			return nil, fmt.Errorf("grammar has more than %d rules", MaxRules)
		}
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("empty grammar")
	}
	return &Grammar{Rules: rules}, nil
}

// --- lexer ---

type token struct {
	kind string // "ident", "string", "arrow", "[", "]", ",", "="
	text string
}

func isIdentStart(c byte) bool {
	return c == '_' || unicode.IsLetter(rune(c))
}

func isIdentPart(c byte) bool {
	return c == '_' || unicode.IsLetter(rune(c)) || ('0' <= c && c <= '9')
}

func lex(line string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(line) {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '[' || c == ']' || c == ',' || c == '=':
			toks = append(toks, token{kind: string(c), text: string(c)})
			i++
		case c == '-':
			if i+1 < len(line) && line[i+1] == '>' {
				toks = append(toks, token{kind: "arrow", text: "->"})
				i += 2
			} else {
				return nil, fmt.Errorf("unexpected '-'")
			}
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(line) && line[j] != c {
				j++
			}
			if j >= len(line) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			toks = append(toks, token{kind: "string", text: line[i+1 : j]})
			i = j + 1
		case isIdentStart(c):
			j := i
			for j < len(line) && isIdentPart(line[j]) {
				j++
			}
			toks = append(toks, token{kind: "ident", text: line[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q", c)
		}
	}
	return toks, nil
}

// --- rule parser ---

type ruleParser struct {
	toks []token
	pos  int
	vars map[string]int
}

func (p *ruleParser) peek() token {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return token{kind: "eof"}
}

func (p *ruleParser) next() token {
	t := p.peek()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

func (p *ruleParser) expect(kind string) error {
	if t := p.next(); t.kind != kind {
		return fmt.Errorf("expected %s, got %q", kind, t.text)
	}
	return nil
}

func (p *ruleParser) varID(name string) int {
	id, ok := p.vars[name]
	if !ok {
		id = len(p.vars)
		p.vars[name] = id
	}
	return id
}

func isVarName(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

func (p *ruleParser) features() (FeatureMap, error) {
	if err := p.expect("["); err != nil {
		return nil, err
	}
	f := FeatureMap{}
	for {
		key := p.next()
		if key.kind != "ident" {
			return nil, fmt.Errorf("expected feature name, got %q", key.text)
		}
		if _, dup := f[key.text]; dup {
			return nil, fmt.Errorf("duplicate feature %q in one map", key.text)
		}
		if err := p.expect("="); err != nil {
			return nil, err
		}
		val := p.next()
		if val.kind != "ident" {
			return nil, fmt.Errorf("expected feature value, got %q", val.text)
		}
		if isVarName(val.text) {
			f[key.text] = VarVal(p.varID(val.text))
		} else {
			f[key.text] = ConstVal(val.text)
		}
		switch t := p.peek(); t.kind {
		case ",":
			p.next()
		case "]":
			p.next()
			return f, nil
		case "ident":
			// space-separated next pair
		default:
			return nil, fmt.Errorf("expected ',' or ']', got %q", t.text)
		}
	}
}

func (p *ruleParser) symbol() (Symbol, error) {
	t := p.next()
	switch t.kind {
	case "string":
		return Symbol{Name: t.text, Terminal: true}, nil
	case "ident":
		s := Symbol{Name: t.text}
		if p.peek().kind == "[" {
			f, err := p.features()
			if err != nil {
				return Symbol{}, err
			}
			s.Features = f
		}
		return s, nil
	default:
		return Symbol{}, fmt.Errorf("expected a symbol, got %q", t.text)
	}
}

func parseRule(line string, id int) (*Rule, error) {
	toks, err := lex(line)
	if err != nil {
		return nil, err
	}
	p := &ruleParser{toks: toks, vars: map[string]int{}}
	lhs, err := p.symbol()
	if err != nil {
		return nil, err
	}
	if lhs.Terminal {
		return nil, fmt.Errorf("left-hand side must be a nonterminal")
	}
	if err := p.expect("arrow"); err != nil {
		return nil, err
	}
	var rhs []Symbol
	for p.peek().kind != "eof" {
		s, err := p.symbol()
		if err != nil {
			return nil, err
		}
		rhs = append(rhs, s)
	}
	if len(rhs) == 0 {
		return nil, fmt.Errorf("empty (epsilon) productions are not allowed")
	}
	return &Rule{ID: id, LHS: lhs, RHS: rhs}, nil
}
