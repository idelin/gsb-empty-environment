// Command depcycle checks a dependency graph whose edges are guarded by
// boolean expressions over a set of switches (at most 10).
//
// It enumerates every switch configuration in bit-string order (switch
// declared first is the most significant bit), evaluates the edge guards
// under that configuration, and verifies the resulting concrete graph with
// a topological sort. The first (lexicographically smallest) configuration
// that yields a cyclic graph is reported together with one real cycle.
// If no configuration produces a cycle, the total enumeration count is
// reported instead.
//
// Input (JSON on stdin, or a file path as the first argument):
//
//	{
//	  "switches": ["a", "b"],
//	  "edges": [
//	    {"from": "A", "to": "B", "when": "a && !b"},
//	    {"from": "B", "to": "A"}              // omitted "when" means always
//	  ]
//	}
//
// Guard expression grammar:
//
//	expr   = orExpr
//	orExpr = andExpr ("||" andExpr)*
//	andExpr= unary ("&&" unary)*
//	unary  = "!" unary | "(" expr ")" | ident | "true" | "false"
//
// Output (stdout):
//
//	CYCLE config=<bitstring> cycle=N0->N1->...->N0
//	ACYCLIC count=<number of enumerated configurations>
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"strings"
)

const maxSwitches = 10

type edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	When string `json:"when"`
}

type input struct {
	Switches []string `json:"switches"`
	Edges    []edge   `json:"edges"`
}

// ---------- boolean expression parser ----------

type token struct {
	kind string // "ident", "and", "or", "not", "lparen", "rparen", "const"
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
		case c == '&':
			if i+1 >= len(src) || src[i+1] != '&' {
				return nil, fmt.Errorf("expected && at offset %d", i)
			}
			toks = append(toks, token{kind: "and"})
			i += 2
		case c == '|':
			if i+1 >= len(src) || src[i+1] != '|' {
				return nil, fmt.Errorf("expected || at offset %d", i)
			}
			toks = append(toks, token{kind: "or"})
			i += 2
		case c == '!':
			toks = append(toks, token{kind: "not"})
			i++
		case c == '(':
			toks = append(toks, token{kind: "lparen"})
			i++
		case c == ')':
			toks = append(toks, token{kind: "rparen"})
			i++
		case c == '_' || c == '.' || c == '-' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
			j := i
			for j < len(src) {
				d := src[j]
				if d == '_' || d == '.' || d == '-' ||
					(d >= 'a' && d <= 'z') || (d >= 'A' && d <= 'Z') || (d >= '0' && d <= '9') {
					j++
				} else {
					break
				}
			}
			word := src[i:j]
			if word == "true" || word == "false" {
				toks = append(toks, token{kind: "const", text: word})
			} else {
				toks = append(toks, token{kind: "ident", text: word})
			}
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q at offset %d", c, i)
		}
	}
	return toks, nil
}

// expr is a compiled boolean expression: given switch values it returns a bool.
type expr func(vals []bool) bool

type parser struct {
	toks []token
	pos  int
	vars map[string]int
}

func (p *parser) peek() (token, bool) {
	if p.pos < len(p.toks) {
		return p.toks[p.pos], true
	}
	return token{}, false
}

func (p *parser) next() (token, error) {
	t, ok := p.peek()
	if !ok {
		return token{}, fmt.Errorf("unexpected end of expression")
	}
	p.pos++
	return t, nil
}

func (p *parser) parseExpr() (expr, error) { return p.parseOr() }

func (p *parser) parseOr() (expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		if t, ok := p.peek(); ok && t.kind == "or" {
			p.pos++
			right, err := p.parseAnd()
			if err != nil {
				return nil, err
			}
			l, r := left, right
			left = func(vals []bool) bool { return l(vals) || r(vals) }
		} else {
			return left, nil
		}
	}
}

func (p *parser) parseAnd() (expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		if t, ok := p.peek(); ok && t.kind == "and" {
			p.pos++
			right, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			l, r := left, right
			left = func(vals []bool) bool { return l(vals) && r(vals) }
		} else {
			return left, nil
		}
	}
}

func (p *parser) parseUnary() (expr, error) {
	t, err := p.next()
	if err != nil {
		return nil, err
	}
	switch t.kind {
	case "not":
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return func(vals []bool) bool { return !inner(vals) }, nil
	case "lparen":
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		closing, err := p.next()
		if err != nil {
			return nil, err
		}
		if closing.kind != "rparen" {
			return nil, fmt.Errorf("expected ) but found %q", closing.text)
		}
		return inner, nil
	case "const":
		v := t.text == "true"
		return func(vals []bool) bool { return v }, nil
	case "ident":
		idx, ok := p.vars[t.text]
		if !ok {
			return nil, fmt.Errorf("unknown switch %q", t.text)
		}
		return func(vals []bool) bool { return vals[idx] }, nil
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}

func compile(src string, vars map[string]int) (expr, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, vars: vars}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(toks) {
		return nil, fmt.Errorf("trailing tokens in expression %q", src)
	}
	return e, nil
}

// ---------- graph checking ----------

// findCycle runs a DFS on the concrete graph and returns one real cycle as a
// list of nodes where the first node is repeated at the end, or nil if the
// graph is acyclic.
func findCycle(nodes []string, adj map[string][]string) []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(nodes))
	var stack []string
	var cycle []string

	var visit func(n string) bool
	visit = func(n string) bool {
		color[n] = gray
		stack = append(stack, n)
		for _, m := range adj[n] {
			switch color[m] {
			case gray:
				// Back edge: extract the loop from the stack.
				start := 0
				for i, s := range stack {
					if s == m {
						start = i
						break
					}
				}
				cycle = append(cycle, stack[start:]...)
				cycle = append(cycle, m)
				return true
			case white:
				if visit(m) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return false
	}

	for _, n := range nodes {
		if color[n] == white {
			if visit(n) {
				return cycle
			}
		}
	}
	return nil
}

// topoAcyclic performs Kahn's topological sort and reports whether every node
// could be ordered (i.e. the concrete graph is acyclic).
func topoAcyclic(nodes []string, adj map[string][]string) bool {
	indeg := make(map[string]int, len(nodes))
	for _, n := range nodes {
		indeg[n] = 0
	}
	for _, n := range nodes {
		for _, m := range adj[n] {
			indeg[m]++
		}
	}
	var queue []string
	for _, n := range nodes {
		if indeg[n] == 0 {
			queue = append(queue, n)
		}
	}
	ordered := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		ordered++
		for _, m := range adj[n] {
			indeg[m]--
			if indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
	}
	return ordered == len(nodes)
}

// checkConfig evaluates all guards under one configuration, builds the
// concrete graph, and returns the real cycle if the graph is cyclic.
func checkConfig(nodes []string, guards []expr, edges []edge, vals []bool) []string {
	adj := make(map[string][]string, len(nodes))
	for i, e := range edges {
		if guards[i](vals) {
			adj[e.From] = append(adj[e.From], e.To)
		}
	}
	// Per-configuration topological sort: only if Kahn's algorithm cannot
	// order every node do we accept that this configuration is cyclic.
	if topoAcyclic(nodes, adj) {
		return nil
	}
	return findCycle(nodes, adj)
}

func run(in *input) (string, error) {
	if len(in.Switches) == 0 {
		return "", fmt.Errorf("at least one switch is required")
	}
	if len(in.Switches) > maxSwitches {
		return "", fmt.Errorf("too many switches: %d (max %d)", len(in.Switches), maxSwitches)
	}
	seen := make(map[string]bool)
	vars := make(map[string]int)
	for i, s := range in.Switches {
		if seen[s] {
			return "", fmt.Errorf("duplicate switch %q", s)
		}
		seen[s] = true
		vars[s] = i
	}

	nodeSet := make(map[string]bool)
	for _, e := range in.Edges {
		nodeSet[e.From] = true
		nodeSet[e.To] = true
	}
	var nodes []string
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	guards := make([]expr, len(in.Edges))
	for i, e := range in.Edges {
		if strings.TrimSpace(e.When) == "" {
			guards[i] = func(vals []bool) bool { return true }
		} else {
			g, err := compile(e.When, vars)
			if err != nil {
				return "", fmt.Errorf("edge %s->%s: %v", e.From, e.To, err)
			}
			guards[i] = g
		}
	}

	n := len(in.Switches)
	total := 1 << uint(n)
	for mask := 0; mask < total; mask++ {
		vals := make([]bool, n)
		var bits []string
		for i := 0; i < n; i++ {
			// Switch i is bit (n-1-i): ascending mask order equals
			// lexicographic order of the bit string in declaration order.
			v := mask&(1<<uint(n-1-i)) != 0
			vals[i] = v
			if v {
				bits = append(bits, "1")
			} else {
				bits = append(bits, "0")
			}
		}
		if cycle := checkConfig(nodes, guards, in.Edges, vals); cycle != nil {
			return fmt.Sprintf("CYCLE config=%s cycle=%s",
				strings.Join(bits, ""), strings.Join(cycle, "->")), nil
		}
	}
	return fmt.Sprintf("ACYCLIC count=%d", total), nil
}

func main() {
	var data []byte
	var err error
	if len(os.Args) > 1 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "depcycle:", err)
		os.Exit(2)
	}
	var in input
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintln(os.Stderr, "depcycle: invalid JSON:", err)
		os.Exit(2)
	}
	result, err := run(&in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "depcycle:", err)
		os.Exit(2)
	}
	fmt.Println(result)
}
