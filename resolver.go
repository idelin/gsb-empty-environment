// Package methodres resolves default methods for classes in a language
// where interfaces support multiple inheritance and default methods.
//
// Resolution order for a class C and method m:
//  1. An explicit override in C wins.
//  2. An explicit selection C.Selects[m] wins, provided it names an
//     interface that C actually inherits and that defines a default for m.
//  3. Otherwise the most specific default among inherited interfaces wins.
//     Definition at interface A is more specific than at B when B is a
//     proper ancestor of A. Two incomparable definitions form a conflict.
//     The same ancestor reached via multiple paths is counted once.
package methodres

import (
	"fmt"
	"sort"
)

// Interface is a node in the interface inheritance graph.
type Interface struct {
	Name     string
	Parents  []string        // directly extended interfaces
	Defaults map[string]bool // methods with a default defined here
}

// Defines reports whether the interface declares a default for method m.
func (in *Interface) Defines(m string) bool { return in.Defaults[m] }

// Graph is the interface inheritance graph. It must be acyclic.
type Graph struct {
	interfaces map[string]*Interface
}

func NewGraph() *Graph { return &Graph{interfaces: map[string]*Interface{}} }

// AddInterface registers an interface. Duplicate names are rejected.
func (g *Graph) AddInterface(in *Interface) error {
	if in == nil || in.Name == "" {
		return fmt.Errorf("interface must have a non-empty name")
	}
	if _, dup := g.interfaces[in.Name]; dup {
		return fmt.Errorf("duplicate interface %q", in.Name)
	}
	g.interfaces[in.Name] = in
	return nil
}

// Validate checks parent references and rejects cyclic inheritance.
func (g *Graph) Validate() error {
	for _, in := range g.interfaces {
		for _, p := range in.Parents {
			if _, ok := g.interfaces[p]; !ok {
				return fmt.Errorf("interface %q extends unknown interface %q", in.Name, p)
			}
		}
	}
	const (
		white = iota // unvisited
		gray         // on the current DFS stack
		black        // fully explored
	)
	color := map[string]int{}
	var visit func(name string) error
	visit = func(name string) error {
		color[name] = gray
		for _, p := range g.interfaces[name].Parents {
			switch color[p] {
			case gray:
				return fmt.Errorf("cyclic inheritance involving %q and %q", name, p)
			case white:
				if err := visit(p); err != nil {
					return err
				}
			}
		}
		color[name] = black
		return nil
	}
	for name := range g.interfaces {
		if color[name] == white {
			if err := visit(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// reachable returns every interface reachable from roots, inclusive.
// Set semantics give diamond dedup for free: an ancestor reached via
// multiple paths appears exactly once.
func (g *Graph) reachable(roots []string) map[string]bool {
	seen := map[string]bool{}
	stack := append([]string(nil), roots...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		if in, ok := g.interfaces[n]; ok {
			stack = append(stack, in.Parents...)
		}
	}
	return seen
}

// isProperAncestor reports whether anc is reachable from desc via parent
// edges and anc != desc.
func (g *Graph) isProperAncestor(anc, desc string) bool {
	return anc != desc && g.reachable([]string{desc})[anc]
}

// Class can override methods explicitly or select a specific ancestor's
// default implementation.
type Class struct {
	Name       string
	Interfaces []string          // directly implemented interfaces
	Overrides  map[string]bool   // methods overridden in the class body
	Selects    map[string]string // method -> explicitly selected ancestor interface
}

// Kind is the outcome category of a resolution.
type Kind int

const (
	None           Kind = iota // no definition found
	ClassOverride              // resolved to the class's own override
	ExplicitSelect             // resolved to an explicitly selected ancestor
	Unique                     // resolved to the single most specific default
	Conflict                   // two or more incomparable most specific defaults
)

func (k Kind) String() string {
	switch k {
	case None:
		return "none"
	case ClassOverride:
		return "class-override"
	case ExplicitSelect:
		return "explicit-select"
	case Unique:
		return "unique"
	case Conflict:
		return "conflict"
	}
	return "unknown"
}

// ConflictPair names two incomparable most specific definitions.
type ConflictPair struct{ A, B string }

// SourceDAG is the resolution source DAG: the subgraph of the interface
// graph that participated in resolving a method. Nodes are interfaces
// lying on some inheritance path from a class root to a definition of the
// method; edges point from child to parent within that subgraph.
type SourceDAG struct {
	Nodes map[string]bool
	Edges map[string][]string // child -> parents, both restricted to Nodes
}

// Resolution is the verdict for one (class, method) pair.
type Resolution struct {
	Method    string
	Kind      Kind
	Winner    string         // interface (or class) supplying the winning definition
	Conflicts []ConflictPair // populated when Kind == Conflict
	Sources   *SourceDAG     // nil when no inherited definition exists
}

// Resolve determines which definition of method the class dispatches to.
func (g *Graph) Resolve(c *Class, method string) (*Resolution, error) {
	for _, root := range c.Interfaces {
		if _, ok := g.interfaces[root]; !ok {
			return nil, fmt.Errorf("class %q implements unknown interface %q", c.Name, root)
		}
	}
	res := &Resolution{Method: method}

	// 1. Class override wins outright.
	if c.Overrides[method] {
		res.Kind = ClassOverride
		res.Winner = c.Name
		return res, nil
	}

	reach := g.reachable(c.Interfaces)

	// 2. Explicit selection, which must name an inheritable definition.
	if sel, ok := c.Selects[method]; ok {
		if !reach[sel] {
			return nil, fmt.Errorf("class %q selects %q for %q, but %q is not an inherited interface",
				c.Name, sel, method, sel)
		}
		if !g.interfaces[sel].Defines(method) {
			return nil, fmt.Errorf("class %q selects %q for %q, but %q defines no default for it",
				c.Name, sel, method, sel)
		}
		res.Kind = ExplicitSelect
		res.Winner = sel
		res.Sources = g.sourceDAG(reach, method)
		return res, nil
	}

	// 3. Most specific inherited default.
	var candidates []string
	for name := range reach {
		if g.interfaces[name].Defines(method) {
			candidates = append(candidates, name)
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		res.Kind = None
		return res, nil
	}
	res.Sources = g.sourceDAG(reach, method)

	var winners []string
	for _, a := range candidates {
		dominated := false
		for _, b := range candidates {
			if g.isProperAncestor(a, b) {
				dominated = true
				break
			}
		}
		if !dominated {
			winners = append(winners, a)
		}
	}
	if len(winners) == 1 {
		res.Kind = Unique
		res.Winner = winners[0]
		return res, nil
	}
	res.Kind = Conflict
	for i := 0; i < len(winners); i++ {
		for j := i + 1; j < len(winners); j++ {
			res.Conflicts = append(res.Conflicts, ConflictPair{A: winners[i], B: winners[j]})
		}
	}
	return res, nil
}

// sourceDAG builds the resolution source DAG: interfaces lying on some
// path from a root to a definition of method, with child->parent edges.
func (g *Graph) sourceDAG(reach map[string]bool, method string) *SourceDAG {
	children := map[string][]string{}
	for name := range reach {
		for _, p := range g.interfaces[name].Parents {
			if reach[p] {
				children[p] = append(children[p], name)
			}
		}
	}
	// Interfaces from which a definition is reachable (within reach).
	onPath := map[string]bool{}
	var stack []string
	for name := range reach {
		if g.interfaces[name].Defines(method) {
			stack = append(stack, name)
		}
	}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if onPath[n] {
			continue
		}
		onPath[n] = true
		stack = append(stack, children[n]...)
	}
	dag := &SourceDAG{Nodes: map[string]bool{}, Edges: map[string][]string{}}
	for name := range onPath {
		if !reach[name] {
			continue
		}
		dag.Nodes[name] = true
		for _, p := range g.interfaces[name].Parents {
			if onPath[p] && reach[p] {
				dag.Edges[name] = append(dag.Edges[name], p)
			}
		}
		sort.Strings(dag.Edges[name])
	}
	return dag
}

// Methods lists every method name known for the class: anything overridden,
// selected, or given a default anywhere in the graph.
func (g *Graph) Methods(c *Class) []string {
	set := map[string]bool{}
	for m := range c.Overrides {
		set[m] = true
	}
	for m := range c.Selects {
		set[m] = true
	}
	for _, in := range g.interfaces {
		for m := range in.Defaults {
			set[m] = true
		}
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
