package impact

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// MaxVariables is the hard limit on build options understood by a Manager.
const MaxVariables = 20

type bddNode struct {
	variable int // -1 for terminals
	low      int
	high     int
}

type bddKey struct {
	variable int
	low      int
	high     int
}

type applyKey struct {
	op byte
	a  int
	b  int
}

// Manager owns a reduced ordered binary decision diagram (ROBDD) arena.
//
// A Manager is not safe for concurrent mutation. All BDDs handed out by one
// Manager use the same variable order and may be compared by node identity.
// Terminal nodes are 0 (false) and 1 (true).
type Manager struct {
	vars    []string
	byName  map[string]int
	nodes   []bddNode
	unique  map[bddKey]int
	applied map[applyKey]int
	notMemo map[int]int
}

// BDD is a handle into a Manager. The zero value is not a valid BDD. Where a
// zero Guard would be ambiguous, package helpers treat it as true for
// convenience; direct BDD methods intentionally reject it.
type BDD struct {
	m  *Manager
	id int
}

// NewManager creates a manager with a fixed variable order. At most
// MaxVariables option names are accepted, in the supplied order.
func NewManager(variables []string) (*Manager, error) {
	if len(variables) > MaxVariables {
		return nil, fmt.Errorf("impact: %d build options exceeds limit %d", len(variables), MaxVariables)
	}
	m := &Manager{
		vars:    append([]string(nil), variables...),
		byName:  make(map[string]int, len(variables)),
		nodes:   []bddNode{{variable: -1}, {variable: -1}},
		unique:  make(map[bddKey]int),
		applied: make(map[applyKey]int),
		notMemo: map[int]int{0: 1, 1: 0},
	}
	for i, name := range variables {
		if name == "" {
			return nil, fmt.Errorf("impact: empty build option name at position %d", i)
		}
		if _, ok := m.byName[name]; ok {
			return nil, fmt.Errorf("impact: duplicate build option %q", name)
		}
		m.byName[name] = i
	}
	return m, nil
}

// Variables returns a copy of the fixed variable order.
func (m *Manager) Variables() []string {
	return append([]string(nil), m.vars...)
}

// NumVariables reports the size of the fixed variable order.
func (m *Manager) NumVariables() int { return len(m.vars) }

// VariableIndex returns the fixed position of name.
func (m *Manager) VariableIndex(name string) (int, bool) {
	i, ok := m.byName[name]
	return i, ok
}

// Variable returns the BDD for a single build option.
func (m *Manager) Variable(name string) (BDD, error) {
	i, ok := m.byName[name]
	if !ok {
		return BDD{}, fmt.Errorf("impact: unknown build option %q", name)
	}
	return BDD{m: m, id: m.mk(i, 0, 1)}, nil
}

// Var is like Variable but panics on an unknown name. It is convenient in
// tests and in code that has already validated its option list.
func (m *Manager) Var(name string) BDD {
	b, err := m.Variable(name)
	if err != nil {
		panic(err)
	}
	return b
}

// VariableAt returns the BDD for the option at a fixed position.
func (m *Manager) VariableAt(index int) (BDD, error) {
	if index < 0 || index >= len(m.vars) {
		return BDD{}, fmt.Errorf("impact: variable index %d out of range", index)
	}
	return BDD{m: m, id: m.mk(index, 0, 1)}, nil
}

// True returns the constant true BDD.
func (m *Manager) True() BDD { return BDD{m: m, id: 1} }

// False returns the constant false BDD.
func (m *Manager) False() BDD { return BDD{m: m, id: 0} }

// Const returns the requested boolean constant.
func (m *Manager) Const(v bool) BDD {
	if v {
		return m.True()
	}
	return m.False()
}

// BDD returns the canonical BDD with the supplied node identity.
func (m *Manager) BDD(id int) (BDD, error) {
	if id < 0 || id >= len(m.nodes) {
		return BDD{}, fmt.Errorf("impact: BDD node %d does not belong to manager", id)
	}
	return BDD{m: m, id: id}, nil
}

func (m *Manager) mk(variable, low, high int) int {
	if low == high {
		return low
	}
	k := bddKey{variable: variable, low: low, high: high}
	if id, ok := m.unique[k]; ok {
		return id
	}
	id := len(m.nodes)
	m.nodes = append(m.nodes, bddNode{variable: variable, low: low, high: high})
	m.unique[k] = id
	return id
}

func (m *Manager) rank(id int) int {
	if id <= 1 {
		return len(m.vars)
	}
	return m.nodes[id].variable
}

func (m *Manager) top(a, b int) int {
	ra, rb := m.rank(a), m.rank(b)
	if ra < rb {
		return ra
	}
	return rb
}

func (m *Manager) cofactor(id, variable, value int) int {
	if id <= 1 {
		return id
	}
	n := m.nodes[id]
	if n.variable != variable {
		return id
	}
	if value == 0 {
		return n.low
	}
	return n.high
}

func (m *Manager) apply(op byte, a, b int) int {
	switch op {
	case '&':
		if a == 0 || b == 0 {
			return 0
		}
		if a == 1 {
			return b
		}
		if b == 1 || a == b {
			return a
		}
	case '|':
		if a == 1 || b == 1 {
			return 1
		}
		if a == 0 {
			return b
		}
		if b == 0 || a == b {
			return a
		}
	case '^':
		if a == 0 {
			return b
		}
		if b == 0 {
			return a
		}
		if a == 1 {
			return m.not(b)
		}
		if b == 1 {
			return m.not(a)
		}
		if a == b {
			return 0
		}
	}
	if op == '&' && a > b {
		a, b = b, a
	}
	if op == '|' && a > b {
		a, b = b, a
	}
	if op == '^' && a > b {
		a, b = b, a
	}
	key := applyKey{op: op, a: a, b: b}
	if id, ok := m.applied[key]; ok {
		return id
	}
	v := m.top(a, b)
	a0, a1 := m.cofactor(a, v, 0), m.cofactor(a, v, 1)
	b0, b1 := m.cofactor(b, v, 0), m.cofactor(b, v, 1)
	low := m.apply(op, a0, b0)
	high := m.apply(op, a1, b1)
	id := m.mk(v, low, high)
	m.applied[key] = id
	return id
}

func (m *Manager) not(a int) int {
	if id, ok := m.notMemo[a]; ok {
		return id
	}
	n := m.nodes[a]
	id := m.mk(n.variable, m.not(n.low), m.not(n.high))
	m.notMemo[a] = id
	return id
}

func (m *Manager) restrict(id, variable int, value bool) int {
	if id <= 1 {
		return id
	}
	n := m.nodes[id]
	if n.variable > variable {
		return id
	}
	if n.variable == variable {
		if value {
			return n.high
		}
		return n.low
	}
	lo := m.restrict(n.low, variable, value)
	hi := m.restrict(n.high, variable, value)
	return m.mk(n.variable, lo, hi)
}

func (m *Manager) exists(id, variable int) int {
	if id <= 1 {
		return id
	}
	n := m.nodes[id]
	if n.variable > variable {
		return id
	}
	if n.variable == variable {
		return m.apply('|', n.low, n.high)
	}
	lo := m.exists(n.low, variable)
	hi := m.exists(n.high, variable)
	return m.mk(n.variable, lo, hi)
}

func (m *Manager) forall(id, variable int) int {
	if id <= 1 {
		return id
	}
	n := m.nodes[id]
	if n.variable > variable {
		return id
	}
	if n.variable == variable {
		return m.apply('&', n.low, n.high)
	}
	lo := m.forall(n.low, variable)
	hi := m.forall(n.high, variable)
	return m.mk(n.variable, lo, hi)
}

func (m *Manager) check(b BDD) {
	if b.m == nil {
		panic("impact: use of uninitialized BDD")
	}
	if b.m != m {
		panic("impact: BDD belongs to a different manager")
	}
	if b.id < 0 || b.id >= len(m.nodes) {
		panic("impact: BDD node is out of range")
	}
}

// Manager returns the owning manager.
func (b BDD) Manager() *Manager { return b.m }

// ID returns the canonical node identity inside the owning manager.
func (b BDD) ID() int { return b.id }

// Valid reports whether the handle is initialized.
func (b BDD) Valid() bool {
	return b.m != nil && b.id >= 0 && b.id < len(b.m.nodes)
}

// IsTrue reports whether b is the true terminal.
func (b BDD) IsTrue() bool { return b.m != nil && b.id == 1 }

// IsFalse reports whether b is the false terminal.
func (b BDD) IsFalse() bool { return b.m != nil && b.id == 0 }

// Not negates b.
func (b BDD) Not() BDD {
	if b.m == nil {
		panic("impact: use of uninitialized BDD")
	}
	b.m.check(b)
	return BDD{m: b.m, id: b.m.not(b.id)}
}

// And returns the logical conjunction.
func (b BDD) And(other BDD) BDD {
	if b.m == nil || other.m == nil {
		panic("impact: use of uninitialized BDD")
	}
	b.m.check(b)
	b.m.check(other)
	return BDD{m: b.m, id: b.m.apply('&', b.id, other.id)}
}

// Or returns the logical disjunction.
func (b BDD) Or(other BDD) BDD {
	if b.m == nil || other.m == nil {
		panic("impact: use of uninitialized BDD")
	}
	b.m.check(b)
	b.m.check(other)
	return BDD{m: b.m, id: b.m.apply('|', b.id, other.id)}
}

// Xor returns the exclusive-or.
func (b BDD) Xor(other BDD) BDD {
	if b.m == nil || other.m == nil {
		panic("impact: use of uninitialized BDD")
	}
	b.m.check(b)
	b.m.check(other)
	return BDD{m: b.m, id: b.m.apply('^', b.id, other.id)}
}

// Implies returns !b | other.
func (b BDD) Implies(other BDD) BDD {
	return b.Not().Or(other)
}

// Ite returns the if-then-else of three BDDs.
func (b BDD) Ite(then, otherwise BDD) BDD {
	if b.m == nil {
		panic("impact: use of uninitialized BDD")
	}
	return b.And(then).Or(b.Not().And(otherwise))
}

// Equal reports whether two BDDs denote the same Boolean function. Because a
// Manager is canonical, identity is enough, but the manager pointers must
// agree.
func (b BDD) Equal(other BDD) bool {
	return b.m != nil && b.m == other.m && b.id == other.id
}

// Equivalent reports whether two BDDs denote the same function even when they
// come from different managers with the same fixed variable order.
func (b BDD) Equivalent(other BDD) bool {
	if b.m == nil || other.m == nil {
		return false
	}
	if b.m == other.m {
		return b.id == other.id
	}
	if len(b.m.vars) != len(other.m.vars) {
		return false
	}
	for i := range b.m.vars {
		if b.m.vars[i] != other.m.vars[i] {
			return false
		}
	}
	return b.CanonicalKey() == other.CanonicalKey()
}

// Leq reports whether b implies other.
func (b BDD) Leq(other BDD) bool {
	return b.And(other.Not()).IsFalse()
}

// Restrict fixes one variable to value.
func (b BDD) Restrict(variable string, value bool) (BDD, error) {
	if b.m == nil {
		return BDD{}, errors.New("impact: use of uninitialized BDD")
	}
	index, ok := b.m.byName[variable]
	if !ok {
		return BDD{}, fmt.Errorf("impact: unknown build option %q", variable)
	}
	return BDD{m: b.m, id: b.m.restrict(b.id, index, value)}, nil
}

// Exists quantifies out the named variables.
func (b BDD) Exists(variables ...string) (BDD, error) {
	if b.m == nil {
		return BDD{}, errors.New("impact: use of uninitialized BDD")
	}
	id := b.id
	for _, name := range variables {
		index, ok := b.m.byName[name]
		if !ok {
			return BDD{}, fmt.Errorf("impact: unknown build option %q", name)
		}
		id = b.m.exists(id, index)
	}
	return BDD{m: b.m, id: id}, nil
}

// ForAll quantifies all named variables universally.
func (b BDD) ForAll(variables ...string) (BDD, error) {
	if b.m == nil {
		return BDD{}, errors.New("impact: use of uninitialized BDD")
	}
	id := b.id
	for _, name := range variables {
		index, ok := b.m.byName[name]
		if !ok {
			return BDD{}, fmt.Errorf("impact: unknown build option %q", name)
		}
		id = b.m.forall(id, index)
	}
	return BDD{m: b.m, id: id}, nil
}

// Eval evaluates b under an assignment indexed by the manager's fixed option
// order. Missing trailing entries are treated as false.
func (b BDD) Eval(values []bool) bool {
	if b.m == nil {
		panic("impact: use of uninitialized BDD")
	}
	id := b.id
	for id > 1 {
		n := b.m.nodes[id]
		if n.variable >= len(values) || !values[n.variable] {
			id = n.low
		} else {
			id = n.high
		}
	}
	return id == 1
}

// Support returns the variables that b depends on, in fixed order.
func (b BDD) Support() []string {
	if b.m == nil {
		return nil
	}
	seen := make(map[int]bool)
	var walk func(int)
	walk = func(id int) {
		if id <= 1 {
			return
		}
		n := b.m.nodes[id]
		if seen[n.variable] {
			return
		}
		seen[n.variable] = true
		walk(n.low)
		walk(n.high)
	}
	walk(b.id)
	out := make([]string, 0, len(seen))
	for i := range b.m.vars {
		if seen[i] {
			out = append(out, b.m.vars[i])
		}
	}
	return out
}

// SatCount returns the number of assignments satisfying b. With at most 20
// variables the result fits in uint64.
func (b BDD) SatCount() uint64 {
	if b.m == nil {
		return 0
	}
	memo := make(map[[2]int]uint64)
	var count func(id, rank int) uint64
	count = func(id, rank int) uint64 {
		if id == 0 {
			return 0
		}
		if id == 1 {
			return uint64(1) << uint(len(b.m.vars)-rank)
		}
		key := [2]int{id, rank}
		if value, ok := memo[key]; ok {
			return value
		}
		n := b.m.nodes[id]
		gap := n.variable - rank
		sub := count(n.low, n.variable+1) + count(n.high, n.variable+1)
		value := sub << uint(gap)
		memo[key] = value
		return value
	}
	return count(b.id, 0)
}

// Assignment is a total assignment to the manager's fixed option order.
type Assignment struct {
	Values []bool
}

// Get returns the value of a named option.
func (a Assignment) Get(m *Manager, name string) bool {
	i, ok := m.byName[name]
	return ok && i < len(a.Values) && a.Values[i]
}

// Enumerate returns every satisfying total assignment in lexicographic order
// of the fixed variable order. It can materialize 2^20 assignments; callers
// that need a bound should use EnumerateLimit.
func (b BDD) Enumerate() []Assignment {
	return b.EnumerateLimit(-1)
}

// EnumerateLimit is Enumerate with a maximum number of returned assignments.
// A negative limit means no limit.
func (b BDD) EnumerateLimit(limit int) []Assignment {
	if b.m == nil {
		return nil
	}
	var out []Assignment
	values := make([]bool, len(b.m.vars))
	var walk func(id, rank int)
	walk = func(id, rank int) {
		if limit >= 0 && len(out) >= limit {
			return
		}
		if id == 0 {
			return
		}
		if id == 1 {
			var fill func(int)
			fill = func(r int) {
				if limit >= 0 && len(out) >= limit {
					return
				}
				if r == len(values) {
					out = append(out, Assignment{Values: append([]bool(nil), values...)})
					return
				}
				values[r] = false
				fill(r + 1)
				values[r] = true
				fill(r + 1)
				values[r] = false
			}
			fill(rank)
			return
		}
		n := b.m.nodes[id]
		if rank < n.variable {
			values[rank] = false
			walk(id, rank+1)
			values[rank] = true
			walk(id, rank+1)
			values[rank] = false
			return
		}
		values[n.variable] = false
		walk(n.low, n.variable+1)
		values[n.variable] = true
		walk(n.high, n.variable+1)
		values[n.variable] = false
	}
	walk(b.id, 0)
	return out
}

// Cube returns the conjunction of the supplied variable assignments.
func (m *Manager) Cube(values map[string]bool) (BDD, error) {
	id := 1
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		index, ok := m.byName[name]
		if !ok {
			return BDD{}, fmt.Errorf("impact: unknown build option %q", name)
		}
		lit := m.mk(index, 0, 1)
		if !values[name] {
			lit = m.not(lit)
		}
		id = m.apply('&', id, lit)
	}
	return BDD{m: m, id: id}, nil
}

// CanonicalKey returns a manager-independent structural key. Because the
// diagram is reduced and ordered, equal Boolean functions produce equal keys.
func (b BDD) CanonicalKey() string {
	if b.m == nil {
		return "0"
	}
	memo := make(map[int]string)
	var key func(int) string
	key = func(id int) string {
		if id == 0 {
			return "0"
		}
		if id == 1 {
			return "1"
		}
		if s, ok := memo[id]; ok {
			return s
		}
		n := b.m.nodes[id]
		s := "(" + b.m.vars[n.variable] + "?" + key(n.low) + ":" + key(n.high) + ")"
		memo[id] = s
		return s
	}
	return key(b.id)
}

// String renders a compact, readable expression.
func (b BDD) String() string {
	if b.m == nil {
		return "0"
	}
	if b.id == 0 {
		return "0"
	}
	if b.id == 1 {
		return "1"
	}
	memo := make(map[int]string)
	var render func(int) string
	render = func(id int) string {
		if id == 0 {
			return "0"
		}
		if id == 1 {
			return "1"
		}
		if s, ok := memo[id]; ok {
			return s
		}
		n := b.m.nodes[id]
		name := b.m.vars[n.variable]
		hi := render(n.high)
		lo := render(n.low)
		var s string
		switch {
		case n.low == 0 && n.high == 1:
			s = name
		case n.low == 0:
			s = "(" + name + " & " + hi + ")"
		case n.high == 0:
			s = "(!" + name + " & " + lo + ")"
		default:
			s = "((" + name + " & " + hi + ") | (!" + name + " & " + lo + "))"
		}
		memo[id] = s
		return s
	}
	return render(b.id)
}

// IsCanonical checks the internal reduced/ordered invariants reachable from b.
func (b BDD) IsCanonical() error {
	if b.m == nil {
		return errors.New("impact: uninitialized BDD")
	}
	m := b.m
	if b.id < 0 || b.id >= len(m.nodes) {
		return fmt.Errorf("impact: node %d out of range", b.id)
	}
	seen := make(map[int]bool)
	var walk func(int, int) error
	walk = func(id, parentRank int) error {
		if id <= 1 {
			return nil
		}
		if seen[id] {
			return nil
		}
		seen[id] = true
		n := m.nodes[id]
		if n.variable < 0 || n.variable >= len(m.vars) {
			return fmt.Errorf("impact: node %d has invalid variable %d", id, n.variable)
		}
		if n.variable <= parentRank {
			return fmt.Errorf("impact: node %d violates variable order", id)
		}
		if n.low == n.high {
			return fmt.Errorf("impact: node %d is not reduced", id)
		}
		if err := walk(n.low, n.variable); err != nil {
			return err
		}
		return walk(n.high, n.variable)
	}
	return walk(b.id, -1)
}

// Parse builds a BDD from a small Boolean expression language. Identifiers are
// build option names; constants true/false/1/0 are accepted. Operators, from
// lowest to highest precedence, are <->, ->, |, ^, &, !/~.
func (m *Manager) Parse(expression string) (BDD, error) {
	p := &parser{lexer: newLexer(expression), m: m}
	if err := p.advance(); err != nil {
		return BDD{}, err
	}
	b, err := p.parseImplication()
	if err != nil {
		return BDD{}, err
	}
	if p.tok.kind != tokEOF {
		return BDD{}, fmt.Errorf("impact: unexpected token %q in expression", p.tok.text)
	}
	return b, nil
}

// MustParse is Parse with panic-on-error semantics.
func (m *Manager) MustParse(expression string) BDD {
	b, err := m.Parse(expression)
	if err != nil {
		panic(err)
	}
	return b
}

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokConst
	tokOp
	tokLParen
	tokRParen
)

type token struct {
	kind tokenKind
	text string
	bool bool
}

type lexer struct {
	s   string
	pos int
}

func newLexer(s string) *lexer { return &lexer{s: s} }

func (l *lexer) next() (token, error) {
	for l.pos < len(l.s) {
		c := l.s[l.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			l.pos++
			continue
		}
		break
	}
	if l.pos >= len(l.s) {
		return token{kind: tokEOF}, nil
	}
	c := l.s[l.pos]
	switch c {
	case '(':
		l.pos++
		return token{kind: tokLParen, text: "("}, nil
	case ')':
		l.pos++
		return token{kind: tokRParen, text: ")"}, nil
	case '!', '~':
		l.pos++
		return token{kind: tokOp, text: "!"}, nil
	case '&':
		l.pos++
		if l.pos < len(l.s) && l.s[l.pos] == '&' {
			l.pos++
		}
		return token{kind: tokOp, text: "&"}, nil
	case '|':
		l.pos++
		if l.pos < len(l.s) && l.s[l.pos] == '|' {
			l.pos++
		}
		return token{kind: tokOp, text: "|"}, nil
	case '^':
		l.pos++
		return token{kind: tokOp, text: "^"}, nil
	case '-':
		if l.pos+1 < len(l.s) && l.s[l.pos+1] == '>' {
			l.pos += 2
			return token{kind: tokOp, text: "->"}, nil
		}
	case '=':
		if l.pos+1 < len(l.s) && l.s[l.pos+1] == '>' {
			l.pos += 2
			return token{kind: tokOp, text: "->"}, nil
		}
	case '<':
		if strings.HasPrefix(l.s[l.pos:], "<->") {
			l.pos += 3
			return token{kind: tokOp, text: "<->"}, nil
		}
	}
	if isIdentStart(c) {
		start := l.pos
		l.pos++
		for l.pos < len(l.s) && isIdentPart(l.s[l.pos]) {
			l.pos++
		}
		text := l.s[start:l.pos]
		switch text {
		case "true", "TRUE", "True":
			return token{kind: tokConst, text: text, bool: true}, nil
		case "false", "FALSE", "False":
			return token{kind: tokConst, text: text, bool: false}, nil
		}
		return token{kind: tokIdent, text: text}, nil
	}
	if c >= '0' && c <= '9' {
		l.pos++
		text := l.s[l.pos-1 : l.pos]
		if text == "0" {
			return token{kind: tokConst, text: text, bool: false}, nil
		}
		if text == "1" {
			return token{kind: tokConst, text: text, bool: true}, nil
		}
	}
	return token{}, fmt.Errorf("impact: unexpected character %q", string(c))
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '/' || c == ':'
}

type parser struct {
	lexer *lexer
	m     *Manager
	tok   token
}

func (p *parser) advance() error {
	t, err := p.lexer.next()
	if err != nil {
		return err
	}
	p.tok = t
	return nil
}

func (p *parser) acceptOp(op string) bool {
	if p.tok.kind == tokOp && p.tok.text == op {
		_ = p.advance()
		return true
	}
	return false
}

func (p *parser) parseImplication() (BDD, error) {
	left, err := p.parseOr()
	if err != nil {
		return BDD{}, err
	}
	if p.acceptOp("<->") {
		right, err := p.parseImplication()
		if err != nil {
			return BDD{}, err
		}
		return left.Ite(right, right.Not()), nil
	}
	if p.acceptOp("->") {
		right, err := p.parseImplication()
		if err != nil {
			return BDD{}, err
		}
		return left.Not().Or(right), nil
	}
	return left, nil
}

func (p *parser) parseOr() (BDD, error) {
	left, err := p.parseXor()
	if err != nil {
		return BDD{}, err
	}
	for p.acceptOp("|") {
		right, err := p.parseXor()
		if err != nil {
			return BDD{}, err
		}
		left = left.Or(right)
	}
	return left, nil
}

func (p *parser) parseXor() (BDD, error) {
	left, err := p.parseAnd()
	if err != nil {
		return BDD{}, err
	}
	for p.acceptOp("^") {
		right, err := p.parseAnd()
		if err != nil {
			return BDD{}, err
		}
		left = left.Xor(right)
	}
	return left, nil
}

func (p *parser) parseAnd() (BDD, error) {
	left, err := p.parseUnary()
	if err != nil {
		return BDD{}, err
	}
	for p.acceptOp("&") {
		right, err := p.parseUnary()
		if err != nil {
			return BDD{}, err
		}
		left = left.And(right)
	}
	return left, nil
}

func (p *parser) parseUnary() (BDD, error) {
	if p.acceptOp("!") {
		b, err := p.parseUnary()
		if err != nil {
			return BDD{}, err
		}
		return b.Not(), nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (BDD, error) {
	switch p.tok.kind {
	case tokConst:
		b := p.m.Const(p.tok.bool)
		_ = p.advance()
		return b, nil
	case tokIdent:
		name := p.tok.text
		index, ok := p.m.byName[name]
		_ = p.advance()
		if !ok {
			return BDD{}, fmt.Errorf("impact: unknown build option %q", name)
		}
		return BDD{m: p.m, id: p.m.mk(index, 0, 1)}, nil
	case tokLParen:
		_ = p.advance()
		b, err := p.parseImplication()
		if err != nil {
			return BDD{}, err
		}
		if p.tok.kind != tokRParen {
			return BDD{}, errors.New("impact: missing ')'")
		}
		_ = p.advance()
		return b, nil
	default:
		return BDD{}, fmt.Errorf("impact: unexpected token %q", p.tok.text)
	}
}

// TruthTable is a dense truth table indexed by the binary value of the fixed
// variable order. It is mainly useful for tests and diagnostics.
func (b BDD) TruthTable() []bool {
	if b.m == nil {
		return nil
	}
	n := 1 << uint(len(b.m.vars))
	out := make([]bool, n)
	for i := range out {
		out[i] = b.Eval(indexValues(i, len(b.m.vars)))
	}
	return out
}

func indexValues(index, width int) []bool {
	out := make([]bool, width)
	for i := 0; i < width; i++ {
		out[i] = index&(1<<uint(i)) != 0
	}
	return out
}

// FormatAssignment renders an assignment in fixed variable order.
func (m *Manager) FormatAssignment(a Assignment) string {
	var parts []string
	for i, name := range m.vars {
		v := false
		if i < len(a.Values) {
			v = a.Values[i]
		}
		parts = append(parts, name+"="+strconv.FormatBool(v))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
