// Package methodres resolves method implementations for a language where
// interfaces may multiply inherit and provide default methods, and classes
// may override a method or explicitly pick an ancestor's implementation.
//
// Resolution order for a (class, method) pair:
//  1. a class-level override wins;
//  2. an explicit pick of an ancestor implementation wins, provided the
//     picked interface is reachable through inheritance and actually
//     defines a default for the method;
//  3. otherwise the most specific default among all interfaces reachable
//     from the class wins; an interface A is more specific than B when B
//     is an ancestor of A. Two incomparable "most specific" defaults form
//     a conflict. The same ancestor reached via multiple paths (diamond)
//     is counted once.
//
// Cyclic interface inheritance is rejected at construction time.
package methodres

import (
	"fmt"
	"sort"
)

// Interface is a named interface with direct super-interfaces and a set of
// methods for which it provides a default implementation.
type Interface struct {
	Name     string
	Parents  []string
	Defaults map[string]bool
}

// Class is a named class implementing zero or more interfaces. It may
// override methods directly or explicitly pick an ancestor implementation.
type Class struct {
	Name       string
	Interfaces []string
	Overrides  map[string]bool
	// Picks maps a method name to the name of an ancestor interface whose
	// default implementation the class explicitly selects.
	Picks map[string]string
}

// Graph is a validated program: a set of interfaces and classes.
type Graph struct {
	Interfaces map[string]*Interface
	Classes    map[string]*Class
}

// NewGraph validates and normalizes the given interfaces and classes.
// It returns an error on unknown references, duplicate names, or cyclic
// interface inheritance.
func NewGraph(ifaces []*Interface, classes []*Class) (*Graph, error) {
	g := &Graph{
		Interfaces: make(map[string]*Interface, len(ifaces)),
		Classes:    make(map[string]*Class, len(classes)),
	}
	for _, in := range ifaces {
		if in.Name == "" {
			return nil, fmt.Errorf("interface with empty name")
		}
		if _, dup := g.Interfaces[in.Name]; dup {
			return nil, fmt.Errorf("duplicate interface %q", in.Name)
		}
		cp := &Interface{
			Name:     in.Name,
			Parents:  dedup(in.Parents),
			Defaults: copySet(in.Defaults),
		}
		g.Interfaces[in.Name] = cp
	}
	for _, c := range classes {
		if c.Name == "" {
			return nil, fmt.Errorf("class with empty name")
		}
		if _, dup := g.Classes[c.Name]; dup {
			return nil, fmt.Errorf("duplicate class %q", c.Name)
		}
		if _, clash := g.Interfaces[c.Name]; clash {
			return nil, fmt.Errorf("name %q used by both a class and an interface", c.Name)
		}
		g.Classes[c.Name] = &Class{
			Name:       c.Name,
			Interfaces: dedup(c.Interfaces),
			Overrides:  copySet(c.Overrides),
			Picks:      copyMap(c.Picks),
		}
	}
	for _, in := range g.Interfaces {
		for _, p := range in.Parents {
			if _, ok := g.Interfaces[p]; !ok {
				return nil, fmt.Errorf("interface %q extends unknown interface %q", in.Name, p)
			}
		}
	}
	for _, c := range g.Classes {
		for _, p := range c.Interfaces {
			if _, ok := g.Interfaces[p]; !ok {
				return nil, fmt.Errorf("class %q implements unknown interface %q", c.Name, p)
			}
		}
		reach := g.reachableFrom(c.Interfaces)
		for m, pick := range c.Picks {
			if c.Overrides[m] {
				return nil, fmt.Errorf("class %q both overrides and picks method %q", c.Name, m)
			}
			in, ok := g.Interfaces[pick]
			if !ok {
				return nil, fmt.Errorf("class %q picks %q for %q: not an interface", c.Name, pick, m)
			}
			if !reach[pick] {
				return nil, fmt.Errorf("class %q picks %q for %q: not reachable through inheritance", c.Name, pick, m)
			}
			if !in.Defaults[m] {
				return nil, fmt.Errorf("class %q picks %q for %q: no default defined there", c.Name, pick, m)
			}
		}
	}
	if err := g.checkAcyclic(); err != nil {
		return nil, err
	}
	return g, nil
}

// reachableFrom returns all interfaces reachable from the given roots,
// inclusive. The graph must be acyclic.
func (g *Graph) reachableFrom(roots []string) map[string]bool {
	seen := map[string]bool{}
	stack := append([]string(nil), roots...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		stack = append(stack, g.Interfaces[n].Parents...)
	}
	return seen
}

// checkAcyclic rejects cyclic interface inheritance via iterative DFS.
func (g *Graph) checkAcyclic() error {
	const (
		white = 0 // unvisited
		gray  = 1 // on stack
		black = 2 // done
	)
	color := make(map[string]int, len(g.Interfaces))
	for name := range g.Interfaces {
		if color[name] != white {
			continue
		}
		type frame struct {
			node string
			next int
		}
		stack := []frame{{node: name}}
		color[name] = gray
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			in := g.Interfaces[top.node]
			if top.next < len(in.Parents) {
				p := in.Parents[top.next]
				top.next++
				switch color[p] {
				case gray:
					return fmt.Errorf("cyclic interface inheritance involving %q and %q", top.node, p)
				case white:
					color[p] = gray
					stack = append(stack, frame{node: p})
				}
			} else {
				color[top.node] = black
				stack = stack[:len(stack)-1]
			}
		}
	}
	return nil
}

func dedup(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func copySet(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
