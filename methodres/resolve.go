package methodres

import (
	"fmt"
	"sort"
)

// SourceKind describes how a (class, method) pair was resolved.
type SourceKind int

const (
	// None means no implementation was found.
	None SourceKind = iota
	// ClassOverride means the class itself overrides the method.
	ClassOverride
	// ExplicitPick means the class explicitly picked an ancestor default.
	ExplicitPick
	// Inherited means the unique most-specific inherited default won.
	Inherited
	// Conflict means two or more incomparable most-specific defaults exist.
	Conflict
)

func (k SourceKind) String() string {
	switch k {
	case None:
		return "none"
	case ClassOverride:
		return "class-override"
	case ExplicitPick:
		return "explicit-pick"
	case Inherited:
		return "inherited"
	case Conflict:
		return "conflict"
	}
	return "unknown"
}

// Resolution is the outcome of resolving one method for one class.
type Resolution struct {
	Class  string
	Method string
	Kind   SourceKind
	// From is the defining entity (class or interface) for non-conflict,
	// non-none outcomes.
	From string
	// Pairs lists the incomparable most-specific definitions when Kind is
	// Conflict. Each pair is sorted, and the slice is sorted.
	Pairs [][2]string
}

// Resolver resolves methods against a Graph, memoizing ancestor sets.
type Resolver struct {
	g *Graph
	// ancestors[i] is the set of interfaces reachable from i, including i.
	ancestors map[string]map[string]bool
}

// NewResolver builds a Resolver for g.
func NewResolver(g *Graph) *Resolver {
	return &Resolver{g: g, ancestors: make(map[string]map[string]bool)}
}

// Ancestors returns the set of interfaces reachable from iface (including
// iface itself). The result is shared; callers must not mutate it.
func (r *Resolver) Ancestors(iface string) map[string]bool {
	if set, ok := r.ancestors[iface]; ok {
		return set
	}
	set := map[string]bool{iface: true}
	r.ancestors[iface] = set // guard before recursion; graph is acyclic
	for _, p := range r.g.Interfaces[iface].Parents {
		for a := range r.Ancestors(p) {
			set[a] = true
		}
	}
	return set
}

// classReachable returns all interfaces reachable from the class's
// implemented interfaces (inclusive).
func (r *Resolver) classReachable(c *Class) map[string]bool {
	out := make(map[string]bool)
	for _, root := range c.Interfaces {
		for a := range r.Ancestors(root) {
			out[a] = true
		}
	}
	return out
}

// Resolve resolves method m for class className.
func (r *Resolver) Resolve(className, m string) (Resolution, error) {
	c, ok := r.g.Classes[className]
	if !ok {
		return Resolution{}, fmt.Errorf("unknown class %q", className)
	}
	res := Resolution{Class: className, Method: m, Kind: None}

	// 1. Class override wins.
	if c.Overrides[m] {
		res.Kind, res.From = ClassOverride, className
		return res, nil
	}

	// 2. Explicit pick: must name an inheritable definition.
	if pick, ok := c.Picks[m]; ok {
		in, exists := r.g.Interfaces[pick]
		if !exists {
			return Resolution{}, fmt.Errorf("class %q picks %q for %q: not an interface", className, pick, m)
		}
		if !r.classReachable(c)[pick] {
			return Resolution{}, fmt.Errorf("class %q picks %q for %q: not reachable through inheritance", className, pick, m)
		}
		if !in.Defaults[m] {
			return Resolution{}, fmt.Errorf("class %q picks %q for %q: no default defined there", className, pick, m)
		}
		res.Kind, res.From = ExplicitPick, pick
		return res, nil
	}

	// 3. Most specific inherited default.
	candidates := make(map[string]bool)
	for a := range r.classReachable(c) {
		if r.g.Interfaces[a].Defaults[m] {
			candidates[a] = true
		}
	}
	maximals := maximals(r, candidates)
	switch len(maximals) {
	case 0:
		return res, nil
	case 1:
		res.Kind, res.From = Inherited, maximals[0]
		return res, nil
	default:
		res.Kind = Conflict
		for i := 0; i < len(maximals); i++ {
			for j := i + 1; j < len(maximals); j++ {
				res.Pairs = append(res.Pairs, [2]string{maximals[i], maximals[j]})
			}
		}
		return res, nil
	}
}

// maximals returns the sorted most-specific members of candidates: those
// not an ancestor of any other candidate. Members of maximals are pairwise
// incomparable.
func maximals(r *Resolver, candidates map[string]bool) []string {
	names := sortedKeys(candidates)
	var out []string
	for _, cand := range names {
		dominated := false
		for other := range candidates {
			if other != cand && r.Ancestors(other)[cand] {
				dominated = true
				break
			}
		}
		if !dominated {
			out = append(out, cand)
		}
	}
	return out
}

// Edge is a directed edge in a SourceDAG.
type Edge struct {
	From, To string
	// Kind is "inherits" for inheritance edges and "resolves" for the
	// edge from a class to the chosen definition of a method.
	Kind   string
	Method string // set for "resolves" edges
}

// SourceDAG is the adjudication-source DAG for one class: the class, every
// interface reachable from it, the inheritance edges among them, and one
// "resolves" edge per resolvable method pointing at the chosen definition.
// Conflict pairs are reported per method.
type SourceDAG struct {
	Class     string
	Nodes     []string
	Edges     []Edge
	Selected  map[string]string      // method -> chosen defining entity
	Conflicts map[string][][2]string // method -> conflict pairs
}

// SourceDAG computes the adjudication-source DAG for className over the
// given methods (defaults to every method mentioned anywhere if nil).
func (r *Resolver) SourceDAG(className string, methods []string) (*SourceDAG, error) {
	c, ok := r.g.Classes[className]
	if !ok {
		return nil, fmt.Errorf("unknown class %q", className)
	}
	if methods == nil {
		seen := map[string]bool{}
		for _, in := range r.g.Interfaces {
			for m := range in.Defaults {
				seen[m] = true
			}
		}
		for m := range c.Overrides {
			seen[m] = true
		}
		for m := range c.Picks {
			seen[m] = true
		}
		methods = sortedKeys(seen)
	}

	reach := r.classReachable(c)
	dag := &SourceDAG{
		Class:     className,
		Nodes:     append([]string{className}, sortedKeys(reach)...),
		Selected:  map[string]string{},
		Conflicts: map[string][][2]string{},
	}
	for _, root := range c.Interfaces {
		dag.Edges = append(dag.Edges, Edge{From: className, To: root, Kind: "inherits"})
	}
	for _, name := range sortedKeys(reach) {
		for _, p := range r.g.Interfaces[name].Parents {
			dag.Edges = append(dag.Edges, Edge{From: name, To: p, Kind: "inherits"})
		}
	}
	for _, m := range methods {
		res, err := r.Resolve(className, m)
		if err != nil {
			return nil, err
		}
		switch res.Kind {
		case ClassOverride, ExplicitPick, Inherited:
			dag.Selected[m] = res.From
			dag.Edges = append(dag.Edges, Edge{From: className, To: res.From, Kind: "resolves", Method: m})
		case Conflict:
			dag.Conflicts[m] = res.Pairs
		}
	}
	sort.Slice(dag.Edges, func(i, j int) bool {
		a, b := dag.Edges[i], dag.Edges[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Method < b.Method
	})
	return dag, nil
}
