package cteopt

import (
	"fmt"
	"strings"
)

// Parse parses a small SQL subset into a plan DAG:
//
//	query  := [WITH name AS (select) [, ...]] select
//	select := SELECT (*|col[, ...]) FROM src [JOIN src ON a = b] [WHERE cond [AND ...]]
//	src    := name [alias]        -- base table or CTE name
//	cond   := col (=|!=|<|<=|>|>=) (number|'string')
//
// A CTE referenced N times yields one shared subplan pointer referenced from
// N branches, i.e. the DAG Optimize works on.
func Parse(query string) (*Node, error) {
	toks, err := tokenize(query)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	ctes := map[string]*Node{}
	if p.acceptKw("WITH") {
		for {
			name, err := p.ident()
			if err != nil {
				return nil, err
			}
			if err := p.expectKw("AS"); err != nil {
				return nil, err
			}
			if err := p.expect("("); err != nil {
				return nil, err
			}
			body, err := p.parseSelect(ctes)
			if err != nil {
				return nil, err
			}
			if err := p.expect(")"); err != nil {
				return nil, err
			}
			ctes[name] = body
			if !p.accept(",") {
				break
			}
		}
	}
	root, err := p.parseSelect(ctes)
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("unexpected trailing token %q", p.toks[p.pos])
	}
	return root, nil
}

type parser struct {
	toks []string
	pos  int
}

func (p *parser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *parser) next() (string, error) {
	if p.pos >= len(p.toks) {
		return "", fmt.Errorf("unexpected end of query")
	}
	t := p.toks[p.pos]
	p.pos++
	return t, nil
}

func (p *parser) accept(tok string) bool {
	if p.peek() == tok {
		p.pos++
		return true
	}
	return false
}

func (p *parser) acceptKw(kw string) bool {
	if strings.EqualFold(p.peek(), kw) {
		p.pos++
		return true
	}
	return false
}

func (p *parser) expect(tok string) error {
	if !p.accept(tok) {
		return fmt.Errorf("expected %q, got %q", tok, p.peek())
	}
	return nil
}

func (p *parser) expectKw(kw string) error {
	if !p.acceptKw(kw) {
		return fmt.Errorf("expected %s, got %q", kw, p.peek())
	}
	return nil
}

func (p *parser) ident() (string, error) {
	t, err := p.next()
	if err != nil {
		return "", err
	}
	if !isIdentToken(t) {
		return "", fmt.Errorf("expected identifier, got %q", t)
	}
	return t, nil
}

var reserved = map[string]bool{
	"with": true, "as": true, "select": true, "from": true,
	"where": true, "join": true, "on": true, "and": true,
}

func (p *parser) parseSelect(ctes map[string]*Node) (*Node, error) {
	if err := p.expectKw("SELECT"); err != nil {
		return nil, err
	}
	var cols []string
	if p.accept("*") {
		cols = []string{"*"}
	} else {
		for {
			c, err := p.ident()
			if err != nil {
				return nil, err
			}
			cols = append(cols, c)
			if !p.accept(",") {
				break
			}
		}
	}
	if err := p.expectKw("FROM"); err != nil {
		return nil, err
	}
	left, err := p.parseSrc(ctes)
	if err != nil {
		return nil, err
	}
	if p.acceptKw("JOIN") {
		right, err := p.parseSrc(ctes)
		if err != nil {
			return nil, err
		}
		if err := p.expectKw("ON"); err != nil {
			return nil, err
		}
		k1, err := p.ident()
		if err != nil {
			return nil, err
		}
		if err := p.expect("="); err != nil {
			return nil, err
		}
		k2, err := p.ident()
		if err != nil {
			return nil, err
		}
		left = &Node{Op: Join, JoinKey: [2]string{k1, k2}, Children: []*Node{left, right}}
	}
	if p.acceptKw("WHERE") {
		var parts []string
		for {
			col, err := p.ident()
			if err != nil {
				return nil, err
			}
			op, err := p.next()
			if err != nil {
				return nil, err
			}
			switch op {
			case "=", "!=", "<", "<=", ">", ">=":
			default:
				return nil, fmt.Errorf("expected comparison operator, got %q", op)
			}
			lit, err := p.next()
			if err != nil {
				return nil, err
			}
			parts = append(parts, col+" "+op+" "+lit)
			if !p.acceptKw("AND") {
				break
			}
		}
		left = &Node{Op: Filter, Pred: strings.Join(parts, " AND "), Children: []*Node{left}}
	}
	return &Node{Op: Project, Cols: cols, Children: []*Node{left}}, nil
}

func (p *parser) parseSrc(ctes map[string]*Node) (*Node, error) {
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	alias := ""
	if t := p.peek(); isIdentToken(t) && !reserved[strings.ToLower(t)] {
		alias, _ = p.next()
	}
	if body, ok := ctes[name]; ok {
		if alias != "" {
			return &Node{Op: Rename, Alias: alias, Children: []*Node{body}}, nil
		}
		return body, nil
	}
	if alias == "" {
		alias = name
	}
	return &Node{Op: Scan, Table: name, Alias: alias}, nil
}

func tokenize(s string) ([]string, error) {
	var toks []string
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isAlpha(c) || c == '_':
			j := i
			for j < len(s) && (isAlpha(s[j]) || isDigit(s[j]) || s[j] == '_' || s[j] == '.') {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		case isDigit(c):
			j := i
			for j < len(s) && (isDigit(s[j]) || s[j] == '.') {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		case c == '\'':
			j := i + 1
			for j < len(s) && s[j] != '\'' {
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			toks = append(toks, s[i:j+1])
			i = j + 1
		case c == '<' || c == '>' || c == '!':
			if i+1 < len(s) && s[i+1] == '=' {
				toks = append(toks, s[i:i+2])
				i += 2
			} else if c != '!' {
				toks = append(toks, string(c))
				i++
			} else {
				return nil, fmt.Errorf("unexpected character %q", c)
			}
		case strings.IndexByte("(),*=", c) >= 0:
			toks = append(toks, string(c))
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q", c)
		}
	}
	return toks, nil
}

func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentToken(t string) bool {
	if t == "" {
		return false
	}
	return isAlpha(t[0]) || t[0] == '_'
}
