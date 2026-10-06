// Package csp defines finite-domain constraint problems: parsing,
// representation and evaluation. Domains are inclusive integer ranges;
// constraints are arithmetic comparisons over variables and constants.
package csp

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Expr is an integer arithmetic expression over problem variables.
type Expr interface {
	Eval(env map[string]int) int
	VarsInto(set map[string]bool)
	String() string
}

// Const is an integer literal.
type Const struct{ V int }

func (c Const) Eval(env map[string]int) int  { return c.V }
func (c Const) VarsInto(set map[string]bool) {}
func (c Const) String() string               { return strconv.Itoa(c.V) }

// Var references a problem variable.
type Var struct{ Name string }

func (v Var) Eval(env map[string]int) int  { return env[v.Name] }
func (v Var) VarsInto(set map[string]bool) { set[v.Name] = true }
func (v Var) String() string               { return v.Name }

// Bin is a binary arithmetic operation: '+', '-' or '*'.
type Bin struct {
	Op   byte
	L, R Expr
}

func (b Bin) Eval(env map[string]int) int {
	switch b.Op {
	case '+':
		return b.L.Eval(env) + b.R.Eval(env)
	case '-':
		return b.L.Eval(env) - b.R.Eval(env)
	case '*':
		return b.L.Eval(env) * b.R.Eval(env)
	}
	panic("unknown operator")
}

func (b Bin) VarsInto(set map[string]bool) {
	b.L.VarsInto(set)
	b.R.VarsInto(set)
}

func (b Bin) String() string {
	return "(" + b.L.String() + " " + string(b.Op) + " " + b.R.String() + ")"
}

// Neg is unary negation.
type Neg struct{ E Expr }

func (n Neg) Eval(env map[string]int) int  { return -n.E.Eval(env) }
func (n Neg) VarsInto(set map[string]bool) { n.E.VarsInto(set) }
func (n Neg) String() string               { return "(-" + n.E.String() + ")" }

// Constraint is a named comparison between two expressions.
// Op is one of ==, !=, <, <=, >, >=.
type Constraint struct {
	Name string
	L, R Expr
	Op   string
}

// Holds reports whether the constraint is satisfied by a complete
// assignment. Unassigned variables evaluate to zero.
func (c Constraint) Holds(env map[string]int) bool {
	l, r := c.L.Eval(env), c.R.Eval(env)
	switch c.Op {
	case "==":
		return l == r
	case "!=":
		return l != r
	case "<":
		return l < r
	case "<=":
		return l <= r
	case ">":
		return l > r
	case ">=":
		return l >= r
	}
	panic("unknown relation " + c.Op)
}

// Vars returns the sorted names of variables used by the constraint.
func (c Constraint) Vars() []string {
	set := map[string]bool{}
	c.L.VarsInto(set)
	c.R.VarsInto(set)
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func (c Constraint) String() string {
	return c.Name + ": " + c.L.String() + " " + c.Op + " " + c.R.String()
}

// VarDecl declares a variable with an inclusive integer domain [Lo, Hi].
type VarDecl struct {
	Name   string
	Lo, Hi int
}

// Problem is a finite-domain constraint satisfaction problem.
// Vars are kept in declaration order, which fixes the enumeration order.
type Problem struct {
	Vars []VarDecl
	Cons []Constraint
}

// Combinations returns the number of complete assignments, capping the
// count at limit+1 so callers can enforce an enumeration budget without
// integer overflow.
func (p *Problem) Combinations(limit int64) int64 {
	total := int64(1)
	for _, v := range p.Vars {
		n := int64(v.Hi-v.Lo) + 1
		if n <= 0 {
			return 0
		}
		if total > limit/n+1 {
			return limit + 1
		}
		total *= n
		if total > limit {
			return limit + 1
		}
	}
	return total
}

// FormatEnv renders an assignment deterministically (sorted by name).
func FormatEnv(env map[string]int) string {
	names := make([]string, 0, len(env))
	for n := range env {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%s=%d", n, env[n])
	}
	return strings.Join(parts, ", ")
}

// --- parsing ---

// Parse reads a problem from r. Grammar (one statement per line):
//
//	# comment
//	var <name> = <lo>..<hi>
//	[<name>:] <expr> <rel> <expr>
//
// where <rel> is one of == != < <= > >= and <expr> uses + - * ( ) over
// integer literals and variable names. Constraints without an explicit
// name are called c1, c2, ... in input order.
func Parse(r io.Reader) (*Problem, error) {
	p := &Problem{}
	declared := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "var ") {
			v, err := parseVarDecl(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", lineNo, err)
			}
			if declared[v.Name] {
				return nil, fmt.Errorf("line %d: variable %q declared twice", lineNo, v.Name)
			}
			declared[v.Name] = true
			p.Vars = append(p.Vars, v)
			continue
		}
		c, err := parseConstraint(line, len(p.Cons)+1)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", lineNo, err)
		}
		for _, v := range c.Vars() {
			if !declared[v] {
				return nil, fmt.Errorf("line %d: constraint %s uses undeclared variable %q", lineNo, c.Name, v)
			}
		}
		p.Cons = append(p.Cons, c)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(p.Cons) > 20 {
		return nil, fmt.Errorf("too many constraints: %d (maximum is 20)", len(p.Cons))
	}
	return p, nil
}

func parseVarDecl(line string) (VarDecl, error) {
	var v VarDecl
	rest := strings.TrimSpace(strings.TrimPrefix(line, "var "))
	eq := strings.Index(rest, "=")
	if eq < 0 {
		return v, fmt.Errorf("malformed var declaration %q (want: var name = lo..hi)", line)
	}
	v.Name = strings.TrimSpace(rest[:eq])
	if !isIdent(v.Name) {
		return v, fmt.Errorf("invalid variable name %q", v.Name)
	}
	rng := strings.Split(strings.TrimSpace(rest[eq+1:]), "..")
	if len(rng) != 2 {
		return v, fmt.Errorf("invalid domain in %q (want lo..hi)", line)
	}
	lo, err := strconv.Atoi(strings.TrimSpace(rng[0]))
	if err != nil {
		return v, fmt.Errorf("invalid domain lower bound: %v", err)
	}
	hi, err := strconv.Atoi(strings.TrimSpace(rng[1]))
	if err != nil {
		return v, fmt.Errorf("invalid domain upper bound: %v", err)
	}
	if lo > hi {
		return v, fmt.Errorf("empty domain [%d, %d]", lo, hi)
	}
	v.Lo, v.Hi = lo, hi
	return v, nil
}

var relOps = []string{"<=", ">=", "==", "!=", "<", ">"}

func parseConstraint(line string, seq int) (Constraint, error) {
	var c Constraint
	body := line
	if i := strings.Index(line, ":"); i >= 0 {
		name := strings.TrimSpace(line[:i])
		if isIdent(name) {
			c.Name = name
			body = strings.TrimSpace(line[i+1:])
		}
	}
	if c.Name == "" {
		c.Name = "c" + strconv.Itoa(seq)
	}
	// Locate the top-level relational operator (outside parentheses).
	depth := 0
	opAt, opLen := -1, 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth != 0 {
			continue
		}
		for _, op := range relOps {
			if strings.HasPrefix(body[i:], op) {
				opAt, opLen = i, len(op)
				break
			}
		}
		if opAt >= 0 {
			break
		}
	}
	if opAt < 0 {
		return c, fmt.Errorf("constraint %q has no relational operator", line)
	}
	c.Op = body[opAt : opAt+opLen]
	l, err := parseExpr(strings.TrimSpace(body[:opAt]))
	if err != nil {
		return c, fmt.Errorf("constraint %s: left side: %v", c.Name, err)
	}
	r, err := parseExpr(strings.TrimSpace(body[opAt+opLen:]))
	if err != nil {
		return c, fmt.Errorf("constraint %s: right side: %v", c.Name, err)
	}
	c.L, c.R = l, r
	return c, nil
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch == '_' || i > 0 && ch >= '0' && ch <= '9' {
			continue
		}
		return false
	}
	return true
}

// expression parser: expr := term (('+'|'-') term)* ;
// term := factor ('*' factor)* ;
// factor := '-' factor | number | ident | '(' expr ')'
type exprParser struct {
	s string
	i int
}

func parseExpr(s string) (Expr, error) {
	if s == "" {
		return nil, fmt.Errorf("empty expression")
	}
	ep := &exprParser{s: s}
	e, err := ep.parseExpr()
	if err != nil {
		return nil, err
	}
	ep.skipSpace()
	if ep.i != len(ep.s) {
		return nil, fmt.Errorf("unexpected trailing input %q", ep.s[ep.i:])
	}
	return e, nil
}

func (p *exprParser) skipSpace() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *exprParser) parseExpr() (Expr, error) {
	l, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			op := p.s[p.i]
			p.i++
			r, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			l = Bin{Op: op, L: l, R: r}
		} else {
			return l, nil
		}
	}
}

func (p *exprParser) parseTerm() (Expr, error) {
	l, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if p.i < len(p.s) && p.s[p.i] == '*' {
			p.i++
			r, err := p.parseFactor()
			if err != nil {
				return nil, err
			}
			l = Bin{Op: '*', L: l, R: r}
		} else {
			return l, nil
		}
	}
}

func (p *exprParser) parseFactor() (Expr, error) {
	p.skipSpace()
	if p.i >= len(p.s) {
		return nil, fmt.Errorf("unexpected end of expression")
	}
	ch := p.s[p.i]
	switch {
	case ch == '-':
		p.i++
		e, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return Neg{E: e}, nil
	case ch == '(':
		p.i++
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.i >= len(p.s) || p.s[p.i] != ')' {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		p.i++
		return e, nil
	case ch >= '0' && ch <= '9':
		j := p.i
		for j < len(p.s) && p.s[j] >= '0' && p.s[j] <= '9' {
			j++
		}
		n, err := strconv.Atoi(p.s[p.i:j])
		if err != nil {
			return nil, err
		}
		p.i = j
		return Const{V: n}, nil
	case ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch == '_':
		j := p.i
		for j < len(p.s) && (p.s[j] >= 'a' && p.s[j] <= 'z' || p.s[j] >= 'A' && p.s[j] <= 'Z' || p.s[j] >= '0' && p.s[j] <= '9' || p.s[j] == '_') {
			j++
		}
		name := p.s[p.i:j]
		p.i = j
		return Var{Name: name}, nil
	}
	return nil, fmt.Errorf("unexpected character %q", string(ch))
}
