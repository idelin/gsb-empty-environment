package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Config is a platform configuration: a truth assignment for named boolean
// platform conditions. A missing name is treated as false.
type Config map[string]bool

// Clone returns a copy of c.
func (c Config) Clone() Config {
	if c == nil {
		return Config{}
	}
	out := make(Config, len(c))
	for name, value := range c {
		out[name] = value
	}
	return out
}

// String returns a stable representation, with names sorted lexicographically.
func (c Config) String() string {
	if len(c) == 0 {
		return "{}"
	}
	names := make([]string, 0, len(c))
	for name := range c {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%t", name, c[name])
	}
	b.WriteByte('}')
	return b.String()
}

type condKind uint8

const (
	condTrue condKind = iota
	condFalse
	condVar
	condNot
	condAnd
	condOr
)

type condNode struct {
	kind     condKind
	name     string
	children []*condNode
}

// Condition is a boolean expression over platform flags. Its zero value is
// true, so an Edge with no explicit condition is unconditional.
type Condition struct {
	root *condNode
}

// Always returns a condition that is true for every configuration.
func Always() Condition {
	return Condition{}
}

// Never returns a condition that is false for every configuration.
func Never() Condition {
	return Condition{root: &condNode{kind: condFalse}}
}

// Flag returns a condition that is true when name is true in the configuration.
func Flag(name string) Condition {
	return Condition{root: &condNode{kind: condVar, name: name}}
}

// NotFlag returns a condition that is true when name is false in the
// configuration.
func NotFlag(name string) Condition {
	return Not(Flag(name))
}

// When returns Flag(name) when value is true and NotFlag(name) otherwise.
func When(name string, value bool) Condition {
	if value {
		return Flag(name)
	}
	return NotFlag(name)
}

// Not negates c.
func Not(c Condition) Condition {
	if c.root == nil {
		return Never()
	}
	switch c.root.kind {
	case condTrue:
		return Never()
	case condFalse:
		return Always()
	case condNot:
		return Condition{root: c.root.children[0]}
	default:
		return Condition{root: &condNode{kind: condNot, children: []*condNode{c.root}}}
	}
}

// And returns the conjunction of conds. With no arguments it returns Always.
func And(conds ...Condition) Condition {
	children := make([]*condNode, 0, len(conds))
	for _, c := range conds {
		if c.root == nil {
			continue
		}
		switch c.root.kind {
		case condFalse:
			return Never()
		case condTrue:
			continue
		case condAnd:
			children = append(children, c.root.children...)
		default:
			children = append(children, c.root)
		}
	}
	switch len(children) {
	case 0:
		return Always()
	case 1:
		return Condition{root: children[0]}
	default:
		return Condition{root: &condNode{kind: condAnd, children: children}}
	}
}

// Or returns the disjunction of conds. With no arguments it returns Never.
func Or(conds ...Condition) Condition {
	children := make([]*condNode, 0, len(conds))
	for _, c := range conds {
		if c.root == nil {
			return Always()
		}
		switch c.root.kind {
		case condTrue:
			return Always()
		case condFalse:
			continue
		case condOr:
			children = append(children, c.root.children...)
		default:
			children = append(children, c.root)
		}
	}
	switch len(children) {
	case 0:
		return Never()
	case 1:
		return Condition{root: children[0]}
	default:
		return Condition{root: &condNode{kind: condOr, children: children}}
	}
}

// All is an alias for And.
func All(conds ...Condition) Condition {
	return And(conds...)
}

// Any is an alias for Or.
func Any(conds ...Condition) Condition {
	return Or(conds...)
}

// Eval reports whether c holds under cfg.
func (c Condition) Eval(cfg Config) bool {
	return evalCond(c.root, func(name string) bool { return cfg[name] })
}

func (c Condition) evalMask(mask uint32, variables map[string]uint) bool {
	return evalCond(c.root, func(name string) bool {
		bit, ok := variables[name]
		return ok && mask&(1<<bit) != 0
	})
}

func evalCond(n *condNode, flag func(string) bool) bool {
	if n == nil {
		return true
	}
	switch n.kind {
	case condTrue:
		return true
	case condFalse:
		return false
	case condVar:
		return flag(n.name)
	case condNot:
		return !evalCond(n.children[0], flag)
	case condAnd:
		for _, child := range n.children {
			if !evalCond(child, flag) {
				return false
			}
		}
		return true
	case condOr:
		for _, child := range n.children {
			if evalCond(child, flag) {
				return true
			}
		}
		return false
	default:
		panic("netsec: unknown condition kind")
	}
}

// Variables returns the platform names referenced by c, sorted lexicographically.
func (c Condition) Variables() []string {
	seen := make(map[string]struct{})
	collectCondVariables(c.root, seen)
	return sortedNames(seen)
}

func collectCondVariables(n *condNode, seen map[string]struct{}) {
	if n == nil {
		return
	}
	if n.kind == condVar {
		seen[n.name] = struct{}{}
		return
	}
	for _, child := range n.children {
		collectCondVariables(child, seen)
	}
}

func sortedNames(set map[string]struct{}) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// String renders c using flag names, !, &&, || and parentheses.
func (c Condition) String() string {
	return formatCond(c.root)
}

func formatCond(n *condNode) string {
	if n == nil {
		return "true"
	}
	switch n.kind {
	case condTrue:
		return "true"
	case condFalse:
		return "false"
	case condVar:
		return n.name
	case condNot:
		return "!" + formatCond(n.children[0])
	case condAnd:
		parts := make([]string, len(n.children))
		for i, child := range n.children {
			parts[i] = formatCond(child)
		}
		return "(" + strings.Join(parts, " && ") + ")"
	case condOr:
		parts := make([]string, len(n.children))
		for i, child := range n.children {
			parts[i] = formatCond(child)
		}
		return "(" + strings.Join(parts, " || ") + ")"
	default:
		panic("netsec: unknown condition kind")
	}
}
