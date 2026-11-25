// Package bdd implements a reduced ordered binary decision diagram (ROBDD)
// over a fixed variable order. Variable indices are 0..Vars-1 and the index
// itself is the decision level, so every generated node respects the order.
//
// The manager keeps a unique table per variable order: two structurally equal
// boolean functions always share the same integer handle, which means handle
// equality is structural equality. All operations are symbolic; callers never
// have to enumerate the 2^Vars assignments.
package bdd

import (
	"errors"
	"fmt"
)

// BDD is an opaque function handle inside a Manager. The zero and one
// terminals are always Zero and One.
type BDD int

const (
	Zero BDD = 0
	One  BDD = 1
)

type node struct {
	varIdx int
	low    int
	high   int
}

type nodeKey struct {
	varIdx int
	low    int
	high   int
}

type pairKey struct{ a, b int }

type restrictKey struct {
	root     int
	variable int
	value    bool
}

// Manager owns one ordered unique table. BDDs from different managers must not
// be mixed.
type Manager struct {
	vars int

	nodes  []node
	unique map[nodeKey]int

	varCache map[int]BDD

	andCache map[pairKey]BDD
	orCache  map[pairKey]BDD
	xorCache map[pairKey]BDD
	notCache map[int]BDD

	restrictCache map[restrictKey]BDD
}

// MaxVars is the largest supported capability width.
const MaxVars = 48

// NewManager creates a manager for vars boolean variables (1..MaxVars).
func NewManager(vars int) (*Manager, error) {
	if vars < 1 || vars > MaxVars {
		return nil, fmt.Errorf("bdd: variable count %d out of range [1,%d]", vars, MaxVars)
	}
	m := &Manager{
		vars:          vars,
		nodes:         make([]node, 2),
		unique:        map[nodeKey]int{},
		varCache:      map[int]BDD{},
		andCache:      map[pairKey]BDD{},
		orCache:       map[pairKey]BDD{},
		xorCache:      map[pairKey]BDD{},
		notCache:      map[int]BDD{},
		restrictCache: map[restrictKey]BDD{},
	}
	m.nodes[Zero] = node{varIdx: -1}
	m.nodes[One] = node{varIdx: -1}
	return m, nil
}

// Vars reports the manager variable count.
func (m *Manager) Vars() int { return m.vars }

// NumNodes reports the live unique-table size including terminals.
func (m *Manager) NumNodes() int { return len(m.nodes) }

func (m *Manager) mk(varIdx, low, high int) int {
	if low == high {
		return low
	}
	key := nodeKey{varIdx: varIdx, low: low, high: high}
	if id, ok := m.unique[key]; ok {
		return id
	}
	id := len(m.nodes)
	m.nodes = append(m.nodes, node{varIdx: varIdx, low: low, high: high})
	m.unique[key] = id
	return id
}

// Variable returns the BDD of the variable with the given index.
func (m *Manager) Variable(index int) (BDD, error) {
	if index < 0 || index >= m.vars {
		return Zero, fmt.Errorf("bdd: variable %d out of range [0,%d)", index, m.vars)
	}
	if b, ok := m.varCache[index]; ok {
		return b, nil
	}
	b := BDD(m.mk(index, int(Zero), int(One)))
	m.varCache[index] = b
	return b, nil
}

func (m *Manager) top(b int) int {
	if b == int(Zero) || b == int(One) {
		return m.vars
	}
	return m.nodes[b].varIdx
}

func pair(a, b int) pairKey {
	if a > b {
		a, b = b, a
	}
	return pairKey{a: a, b: b}
}

func (m *Manager) branch(b int, variable int) (low, high int) {
	if m.top(b) == variable {
		n := m.nodes[b]
		return n.low, n.high
	}
	return b, b
}

// And returns the conjunction of the given functions.
func (m *Manager) And(parts ...BDD) BDD {
	result := One
	for _, part := range parts {
		result = m.and2(result, part)
		if result == Zero {
			return Zero
		}
	}
	return result
}

func (m *Manager) and2(a, b BDD) BDD {
	if a == Zero || b == Zero {
		return Zero
	}
	if a == One {
		return b
	}
	if b == One {
		return a
	}
	if a == b {
		return a
	}
	key := pair(int(a), int(b))
	if cached, ok := m.andCache[key]; ok {
		return cached
	}
	variable := min(m.top(int(a)), m.top(int(b)))
	al, ah := m.branch(int(a), variable)
	bl, bh := m.branch(int(b), variable)
	result := BDD(m.mk(variable, int(m.and2(BDD(al), BDD(bl))), int(m.and2(BDD(ah), BDD(bh)))))
	m.andCache[key] = result
	return result
}

// Or returns the disjunction of the given functions.
func (m *Manager) Or(parts ...BDD) BDD {
	result := Zero
	for _, part := range parts {
		result = m.or2(result, part)
		if result == One {
			return One
		}
	}
	return result
}

func (m *Manager) or2(a, b BDD) BDD {
	if a == One || b == One {
		return One
	}
	if a == Zero {
		return b
	}
	if b == Zero {
		return a
	}
	if a == b {
		return a
	}
	key := pair(int(a), int(b))
	if cached, ok := m.orCache[key]; ok {
		return cached
	}
	variable := min(m.top(int(a)), m.top(int(b)))
	al, ah := m.branch(int(a), variable)
	bl, bh := m.branch(int(b), variable)
	result := BDD(m.mk(variable, int(m.or2(BDD(al), BDD(bl))), int(m.or2(BDD(ah), BDD(bh)))))
	m.orCache[key] = result
	return result
}

// Not returns the complement of the given function.
func (m *Manager) Not(a BDD) BDD {
	if a == Zero {
		return One
	}
	if a == One {
		return Zero
	}
	if cached, ok := m.notCache[int(a)]; ok {
		return cached
	}
	n := m.nodes[int(a)]
	result := BDD(m.mk(n.varIdx, int(m.Not(BDD(n.low))), int(m.Not(BDD(n.high)))))
	m.notCache[int(a)] = result
	return result
}

// Xor returns the exclusive or of two functions.
func (m *Manager) Xor(a, b BDD) BDD {
	if a == Zero {
		return b
	}
	if b == Zero {
		return a
	}
	if a == One {
		return m.Not(b)
	}
	if b == One {
		return m.Not(a)
	}
	if a == b {
		return Zero
	}
	key := pair(int(a), int(b))
	if cached, ok := m.xorCache[key]; ok {
		return cached
	}
	variable := min(m.top(int(a)), m.top(int(b)))
	al, ah := m.branch(int(a), variable)
	bl, bh := m.branch(int(b), variable)
	result := BDD(m.mk(variable, int(m.Xor(BDD(al), BDD(bl))), int(m.Xor(BDD(ah), BDD(bh)))))
	m.xorCache[key] = result
	return result
}

// Ite returns if-then-else: if cond then yes else no.
func (m *Manager) Ite(cond, yes, no BDD) BDD {
	return m.Or(m.And(cond, yes), m.And(m.Not(cond), no))
}

// Restrict pins variable index to value and returns the residual function.
func (m *Manager) Restrict(root BDD, variable int, value bool) (BDD, error) {
	if variable < 0 || variable >= m.vars {
		return Zero, fmt.Errorf("bdd: variable %d out of range [0,%d)", variable, m.vars)
	}
	if root == Zero || root == One {
		return root, nil
	}
	key := restrictKey{root: int(root), variable: variable, value: value}
	if cached, ok := m.restrictCache[key]; ok {
		return cached, nil
	}
	n := m.nodes[int(root)]
	var result BDD
	switch {
	case n.varIdx > variable:
		result = root
	case n.varIdx < variable:
		low, err := m.Restrict(BDD(n.low), variable, value)
		if err != nil {
			return Zero, err
		}
		high, err := m.Restrict(BDD(n.high), variable, value)
		if err != nil {
			return Zero, err
		}
		result = BDD(m.mk(n.varIdx, int(low), int(high)))
	case value:
		rest, err := m.Restrict(BDD(n.high), variable, value)
		if err != nil {
			return Zero, err
		}
		result = rest
	default:
		rest, err := m.Restrict(BDD(n.low), variable, value)
		if err != nil {
			return Zero, err
		}
		result = rest
	}
	m.restrictCache[key] = result
	return result, nil
}

// Eval evaluates the function on one full assignment.
func (m *Manager) Eval(root BDD, values []bool) (bool, error) {
	if len(values) != m.vars {
		return false, fmt.Errorf("bdd: expected %d values, got %d", m.vars, len(values))
	}
	cursor := int(root)
	for cursor != int(Zero) && cursor != int(One) {
		n := m.nodes[cursor]
		if values[n.varIdx] {
			cursor = n.high
		} else {
			cursor = n.low
		}
	}
	return cursor == int(One), nil
}

// IsZero reports whether the function is unsatisfiable.
func (m *Manager) IsZero(root BDD) bool { return root == Zero }

// IsOne reports whether the function is a tautology.
func (m *Manager) IsOne(root BDD) bool { return root == One }

// Equivalent reports whether two handles denote the same boolean function.
// The unique table makes this a handle comparison.
func (m *Manager) Equivalent(a, b BDD) bool { return a == b }

// MinSolution returns the lexicographically smallest satisfying assignment with
// bit index 0 treated as the most significant position and false < true. It
// returns nil for an unsatisfiable function.
func (m *Manager) MinSolution(root BDD) []bool {
	if root == Zero {
		return nil
	}
	values := make([]bool, m.vars)
	cursor := int(root)
	index := 0
	for cursor != int(Zero) && cursor != int(One) && index < m.vars {
		n := m.nodes[cursor]
		index = n.varIdx
		if n.low != int(Zero) {
			cursor = n.low
		} else {
			values[index] = true
			cursor = n.high
		}
		index++
	}
	if cursor == int(Zero) {
		return nil
	}
	return values
}

// MinSolutionExcluding returns the bit-order-smallest satisfying assignment of
// root that does not satisfy excluded. It returns nil when no such assignment
// exists.
func (m *Manager) MinSolutionExcluding(root, excluded BDD) []bool {
	return m.MinSolution(m.And(root, m.Not(excluded)))
}

// FromFunc builds a BDD from an explicit truth function. It is intended for
// small widths (for example exhaustive test models); it performs O(2^width)
// callback invocations.
func (m *Manager) FromFunc(width int, fn func(values []bool) bool) (BDD, error) {
	if width != m.vars {
		return Zero, fmt.Errorf("bdd: FromFunc width %d != manager vars %d", width, m.vars)
	}
	if fn == nil {
		return Zero, errors.New("bdd: nil truth function")
	}
	values := make([]bool, width)
	var build func(level int) int
	build = func(level int) int {
		if level == width {
			if fn(values) {
				return int(One)
			}
			return int(Zero)
		}
		values[level] = false
		low := build(level + 1)
		values[level] = true
		high := build(level + 1)
		values[level] = false
		return m.mk(level, low, high)
	}
	return BDD(build(0)), nil
}

// Node exposes a unique-table entry for inspection and independent verification.
type Node struct {
	Variable int
	Low      BDD
	High     BDD
	Terminal bool
}

// Node returns the unique-table entry for a handle.
func (m *Manager) Node(root BDD) (Node, error) {
	if root < 0 || int(root) >= len(m.nodes) {
		return Node{}, fmt.Errorf("bdd: handle %d out of range", root)
	}
	if root == Zero || root == One {
		return Node{Variable: -1, Low: root, High: root, Terminal: true}, nil
	}
	n := m.nodes[int(root)]
	return Node{Variable: n.varIdx, Low: BDD(n.low), High: BDD(n.high)}, nil
}
