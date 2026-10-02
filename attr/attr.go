// Package attr implements a generic attribute-grammar evaluation kernel.
//
// Attributes are attached to AST nodes and identified by a Key{Node, Name}.
// Each attribute instance is computed by exactly one Rule that declares its
// dependencies explicitly. The evaluator builds the dependency graph over all
// attribute instances, topologically sorts it (Kahn's algorithm) and then
// evaluates the rules in that order. Cycles in the dependency graph are
// reported as a *CycleError naming every attribute that participates.
package attr

import (
	"fmt"
	"sort"
	"strings"
)

// Key identifies one attribute instance: attribute Name of AST node Node.
// Node must be comparable (a pointer or a value type).
type Key struct {
	Node any
	Name string
}

func (k Key) String() string {
	return fmt.Sprintf("%v.%s", k.Node, k.Name)
}

// Value is an evaluated attribute value. The concrete type is defined by the
// language built on top of the kernel (e.g. a Type or a symbol-table Env).
type Value = any

// Getter resolves the value of a dependency inside a rule's Eval function.
// It panics if the attribute has not been evaluated yet, which cannot happen
// when rules are executed in topological order.
type Getter func(Key) Value

// Rule computes Target from the values of Deps.
type Rule struct {
	Target Key
	Deps   []Key
	Eval   func(Getter) Value
}

// CycleError is returned by Evaluate when the attribute dependency graph
// contains a cycle. Keys lists the attribute instances that could not be
// scheduled, in deterministic order.
type CycleError struct {
	Keys []Key
}

func (e *CycleError) Error() string {
	parts := make([]string, len(e.Keys))
	for i, k := range e.Keys {
		parts[i] = k.String()
	}
	return "attribute dependency cycle detected involving: " + strings.Join(parts, ", ")
}

// Evaluator collects seeds and rules, then evaluates all attributes in
// dependency order.
type Evaluator struct {
	seeds  map[Key]Value
	rules  map[Key]Rule
	values map[Key]Value
}

func NewEvaluator() *Evaluator {
	return &Evaluator{
		seeds:  make(map[Key]Value),
		rules:  make(map[Key]Rule),
		values: make(map[Key]Value),
	}
}

// Set seeds an input attribute (typically an inherited attribute of the root
// node, such as the empty top-level environment).
func (e *Evaluator) Set(k Key, v Value) {
	e.seeds[k] = v
}

// AddRule registers the semantic rule computing r.Target.
func (e *Evaluator) AddRule(r Rule) error {
	if _, ok := e.seeds[r.Target]; ok {
		return fmt.Errorf("attribute %s is seeded and cannot also have a rule", r.Target)
	}
	if _, ok := e.rules[r.Target]; ok {
		return fmt.Errorf("duplicate rule for attribute %s", r.Target)
	}
	e.rules[r.Target] = r
	return nil
}

// Get returns the evaluated (or seeded) value of an attribute.
func (e *Evaluator) Get(k Key) (Value, bool) {
	v, ok := e.values[k]
	return v, ok
}

// Evaluate topologically sorts the dependency graph and runs every rule.
// It returns a *CycleError if the attributes cannot be ordered.
func (e *Evaluator) Evaluate() error {
	order, err := e.topoSort()
	if err != nil {
		return err
	}
	for k, v := range e.seeds {
		e.values[k] = v
	}
	get := func(k Key) Value {
		v, ok := e.values[k]
		if !ok {
			panic(fmt.Sprintf("attr: attribute %s read before evaluation", k))
		}
		return v
	}
	for _, k := range order {
		e.values[k] = e.rules[k].Eval(get)
	}
	return nil
}

// topoSort orders all rule targets so that every attribute is evaluated after
// the attributes it depends on. The order is deterministic: ready nodes are
// always picked in sorted Key order.
func (e *Evaluator) topoSort() ([]Key, error) {
	indeg := make(map[Key]int, len(e.rules))
	adj := make(map[Key][]Key)
	for _, r := range e.rules {
		if _, ok := indeg[r.Target]; !ok {
			indeg[r.Target] = 0
		}
		for _, d := range r.Deps {
			if _, seeded := e.seeds[d]; seeded {
				continue
			}
			if _, ok := e.rules[d]; !ok {
				return nil, fmt.Errorf("attribute %s depends on undefined attribute %s", r.Target, d)
			}
			adj[d] = append(adj[d], r.Target)
			indeg[r.Target]++
		}
	}

	var ready []Key
	for k, d := range indeg {
		if d == 0 {
			ready = insertSorted(ready, k)
		}
	}
	var order []Key
	for len(ready) > 0 {
		k := ready[0]
		ready = ready[1:]
		order = append(order, k)
		for _, m := range adj[k] {
			indeg[m]--
			if indeg[m] == 0 {
				ready = insertSorted(ready, m)
			}
		}
	}
	if len(order) < len(e.rules) {
		var cyclic []Key
		for k, d := range indeg {
			if d > 0 {
				cyclic = append(cyclic, k)
			}
		}
		sortKeys(cyclic)
		return nil, &CycleError{Keys: cyclic}
	}
	return order, nil
}

func sortKeys(keys []Key) {
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
}

func insertSorted(keys []Key, k Key) []Key {
	i := sort.Search(len(keys), func(i int) bool { return keys[i].String() >= k.String() })
	keys = append(keys, Key{})
	copy(keys[i+1:], keys[i:])
	keys[i] = k
	return keys
}
