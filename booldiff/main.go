// booldiff compares two boolean expressions over at most 10 variables and
// prints every minimal cube (prime implicant of the difference function)
// whose assignments all evaluate differently between the two expressions.
//
// Usage:
//   go run main.go [-table] 'expr1' 'expr2'
//
// Operators: ! ~ not, & && and, | || or, ^ xor, parentheses, constants 0/1/true/false.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

const maxVars = 10

// ---------- lexer ----------

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tConst
	tAnd
	tOr
	tXor
	tNot
	tLParen
	tRParen
)

type token struct {
	kind tokKind
	text string
	val  bool
}

func isAlpha(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isAlnum(c byte) bool {
	return isAlpha(c) || (c >= '0' && c <= '9')
}

func lex(s string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, token{kind: tLParen, text: "("})
			i++
		case c == ')':
			toks = append(toks, token{kind: tRParen, text: ")"})
			i++
		case c == '&':
			if i+1 < len(s) && s[i+1] == '&' {
				i += 2
			} else {
				i++
			}
			toks = append(toks, token{kind: tAnd, text: "&"})
		case c == '|':
			if i+1 < len(s) && s[i+1] == '|' {
				i += 2
			} else {
				i++
			}
			toks = append(toks, token{kind: tOr, text: "|"})
		case c == '^':
			toks = append(toks, token{kind: tXor, text: "^"})
			i++
		case c == '!' || c == '~':
			toks = append(toks, token{kind: tNot, text: "!"})
			i++
		case c == '0':
			toks = append(toks, token{kind: tConst, text: "0", val: false})
			i++
		case c == '1':
			toks = append(toks, token{kind: tConst, text: "1", val: true})
			i++
		case isAlpha(c):
			j := i
			for j < len(s) && isAlnum(s[j]) {
				j++
			}
			word := s[i:j]
			switch strings.ToLower(word) {
			case "and":
				toks = append(toks, token{kind: tAnd, text: word})
			case "or":
				toks = append(toks, token{kind: tOr, text: word})
			case "xor":
				toks = append(toks, token{kind: tXor, text: word})
			case "not":
				toks = append(toks, token{kind: tNot, text: word})
			case "true":
				toks = append(toks, token{kind: tConst, text: word, val: true})
			case "false":
				toks = append(toks, token{kind: tConst, text: word, val: false})
			default:
				toks = append(toks, token{kind: tIdent, text: word})
			}
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q at position %d", string(c), i)
		}
	}
	toks = append(toks, token{kind: tEOF})
	return toks, nil
}

// ---------- parser ----------
// precedence (loosest first): or > xor > and > not

type nodeKind int

const (
	kVar nodeKind = iota
	kConst
	kNot
	kAnd
	kOr
	kXor
)

type node struct {
	kind        nodeKind
	name        string
	val         bool
	varIdx      int
	left, right *node
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func parseExpr(s string) (*node, error) {
	toks, err := lex(s)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected trailing token %q", p.peek().text)
	}
	return n, nil
}

func (p *parser) parseOr() (*node, error) {
	left, err := p.parseXor()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOr {
		p.next()
		right, err := p.parseXor()
		if err != nil {
			return nil, err
		}
		left = &node{kind: kOr, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseXor() (*node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tXor {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &node{kind: kXor, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (*node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tAnd {
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &node{kind: kAnd, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseUnary() (*node, error) {
	t := p.peek()
	switch t.kind {
	case tNot:
		p.next()
		sub, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &node{kind: kNot, left: sub}, nil
	case tConst:
		p.next()
		return &node{kind: kConst, val: t.val}, nil
	case tIdent:
		p.next()
		return &node{kind: kVar, name: t.text}, nil
	case tLParen:
		p.next()
		sub, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tRParen {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		p.next()
		return sub, nil
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}

// ---------- variable binding & evaluation ----------

func collectVars(n *node, set map[string]bool) {
	if n == nil {
		return
	}
	if n.kind == kVar {
		set[n.name] = true
	}
	collectVars(n.left, set)
	collectVars(n.right, set)
}

func bindVars(n *node, idx map[string]int) {
	if n == nil {
		return
	}
	if n.kind == kVar {
		n.varIdx = idx[n.name]
	}
	bindVars(n.left, idx)
	bindVars(n.right, idx)
}

func eval(n *node, assign uint) bool {
	switch n.kind {
	case kConst:
		return n.val
	case kVar:
		return (assign>>uint(n.varIdx))&1 == 1
	case kNot:
		return !eval(n.left, assign)
	case kAnd:
		return eval(n.left, assign) && eval(n.right, assign)
	case kOr:
		return eval(n.left, assign) || eval(n.right, assign)
	case kXor:
		return eval(n.left, assign) != eval(n.right, assign)
	}
	return false
}

// ---------- cubes ----------

type cube struct {
	care uint // bit i set => variable i is fixed
	vals uint // value of fixed variables (only care bits meaningful)
}

// cubeSubsetOfDiff reports whether every assignment matched by the cube
// is a point where the two expressions differ.
func cubeSubsetOfDiff(diff []bool, n int, care, vals uint) bool {
	total := 1 << uint(n)
	for a := 0; a < total; a++ {
		if uint(a)&care == vals && !diff[a] {
			return false
		}
	}
	return true
}

// isMinimal reports whether removing any single literal from the cube
// makes it cover at least one assignment that is not in the diff set.
func isMinimal(diff []bool, n int, care, vals uint) bool {
	for i := 0; i < n; i++ {
		bit := uint(1) << uint(i)
		if care&bit != 0 {
			nc := care &^ bit
			if cubeSubsetOfDiff(diff, n, nc, vals&nc) {
				return false
			}
		}
	}
	return true
}

// findMinimalCubes enumerates all cubes whose coverage lies entirely in the
// diff set and from which no literal can be removed. A partial cube that is
// already fully inside the diff set prunes the search: any refinement of it
// would be removable back to it, hence not minimal.
func findMinimalCubes(diff []bool, n int) []cube {
	var out []cube
	var rec func(i int, care, vals uint)
	rec = func(i int, care, vals uint) {
		if cubeSubsetOfDiff(diff, n, care, vals) {
			// Partial cube already fits entirely in the diff set: any
			// further refinement is not minimal, so prune this branch.
			// The cube itself is recorded when it is complete (i == n)
			// or when it is the full-depth cube; handle recording below.
			if isMinimal(diff, n, care, vals) {
				out = append(out, cube{care: care, vals: vals})
			}
			return
		}
		if i == n {
			return
		}
		bit := uint(1) << uint(i)
		rec(i+1, care, vals)         // variable i: don't care
		rec(i+1, care|bit, vals)     // variable i: 0
		rec(i+1, care|bit, vals|bit) // variable i: 1
	}
	rec(0, 0, 0)
	sort.Slice(out, func(a, b int) bool {
		pa, pb := popcount(out[a].care), popcount(out[b].care)
		if pa != pb {
			return pa < pb
		}
		if out[a].care != out[b].care {
			return out[a].care < out[b].care
		}
		return out[a].vals < out[b].vals
	})
	return out
}

func popcount(x uint) int {
	c := 0
	for x != 0 {
		c++
		x &= x - 1
	}
	return c
}

func cubeString(c cube, vars []string) string {
	if c.care == 0 {
		return "(true) -- every assignment differs"
	}
	var parts []string
	for i, v := range vars {
		if c.care>>uint(i)&1 == 1 {
			if c.vals>>uint(i)&1 == 1 {
				parts = append(parts, v)
			} else {
				parts = append(parts, "!"+v)
			}
		}
	}
	return strings.Join(parts, " & ")
}

// ---------- truth-table verification ----------

// verify re-checks, by exhaustive truth-table enumeration, that:
//  1. every printed cube covers only differing assignments;
//  2. every printed cube is minimal (no literal removable);
//  3. the cubes together cover exactly the diff set (complete coverage).
func verify(diff []bool, n int, cubes []cube) []string {
	var fails []string
	total := 1 << uint(n)
	covered := make([]bool, total)
	for ci, c := range cubes {
		if !cubeSubsetOfDiff(diff, n, c.care, c.vals) {
			fails = append(fails, fmt.Sprintf("cube #%d covers a non-differing assignment", ci))
		}
		if !isMinimal(diff, n, c.care, c.vals) {
			fails = append(fails, fmt.Sprintf("cube #%d is not minimal (a literal can be removed)", ci))
		}
		for a := 0; a < total; a++ {
			if uint(a)&c.care == c.vals {
				covered[a] = true
			}
		}
	}
	uncovered := 0
	extra := 0
	for a := 0; a < total; a++ {
		if diff[a] && !covered[a] {
			uncovered++
		}
		if !diff[a] && covered[a] {
			extra++
		}
	}
	if uncovered > 0 {
		fails = append(fails, fmt.Sprintf("%d differing assignment(s) not covered by any cube", uncovered))
	}
	if extra > 0 {
		fails = append(fails, fmt.Sprintf("%d non-differing assignment(s) covered", extra))
	}
	return fails
}

// ---------- main ----------

func main() {
	table := flag.Bool("table", false, "print every differing assignment")
	flag.Parse()
	args := flag.Args()
	if len(args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: booldiff [-table] 'expr1' 'expr2'\n")
		fmt.Fprintf(os.Stderr, "operators: ! ~ not, & && and, | || or, ^ xor, ( ), constants 0 1 true false\n")
		os.Exit(2)
	}
	e1, err := parseExpr(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "expr1: %v\n", err)
		os.Exit(1)
	}
	e2, err := parseExpr(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "expr2: %v\n", err)
		os.Exit(1)
	}

	set := map[string]bool{}
	collectVars(e1, set)
	collectVars(e2, set)
	var vars []string
	for v := range set {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	if len(vars) > maxVars {
		fmt.Fprintf(os.Stderr, "too many variables: %d (max %d)\n", len(vars), maxVars)
		os.Exit(1)
	}
	idx := map[string]int{}
	for i, v := range vars {
		idx[v] = i
	}
	bindVars(e1, idx)
	bindVars(e2, idx)

	n := len(vars)
	total := 1 << uint(n)
	diff := make([]bool, total)
	ndiff := 0
	for a := 0; a < total; a++ {
		d := eval(e1, uint(a)) != eval(e2, uint(a))
		diff[a] = d
		if d {
			ndiff++
		}
	}

	fmt.Printf("variables (%d): %s\n", n, strings.Join(vars, ", "))
	fmt.Printf("differing assignments: %d / %d\n", ndiff, total)

	if *table && ndiff > 0 {
		fmt.Println("differing truth-table rows:")
		for a := 0; a < total; a++ {
			if !diff[a] {
				continue
			}
			var parts []string
			for i, v := range vars {
				parts = append(parts, fmt.Sprintf("%s=%d", v, (uint(a)>>uint(i))&1))
			}
			fmt.Printf("  %s\n", strings.Join(parts, " "))
		}
	}

	cubes := findMinimalCubes(diff, n)
	if ndiff == 0 {
		fmt.Println("expressions are equivalent on every assignment; no difference cubes.")
	} else {
		fmt.Printf("minimal difference cubes (%d):\n", len(cubes))
		for _, c := range cubes {
			fmt.Printf("  %s\n", cubeString(c, vars))
		}
	}

	fails := verify(diff, n, cubes)
	if len(fails) == 0 {
		fmt.Println("truth-table check: PASS (each cube covers only differing rows, no literal removable, union of cubes == diff set)")
	} else {
		fmt.Println("truth-table check: FAIL")
		for _, f := range fails {
			fmt.Printf("  - %s\n", f)
		}
		os.Exit(1)
	}
}
