package unify

import (
	"fmt"
	"strings"
)

// node is a union-find vertex. Only the class root carries authoritative
// kind/name/args; non-roots keep stale data that must never be read
// (always go through find first). label is immutable: it records the term
// exactly as the user wrote it, for provenance rendering.
type node struct {
	parent *node
	rank   int
	kind   Kind
	name   string
	args   []*node
	label  string // immutable, as-written rendering
	val    *node  // root only: original node that contributed structure/rigid data
}

// proofEdge records one user equation that merged two classes. Edges
// connect the two member nodes named by that equation (not the class
// roots), so BFS paths read exactly as the equations the user wrote.
// The graph is keyed by node identity, fully separate from union-find
// parent links, so path compression can never destroy explanations.
type proofEdge struct {
	to *node
	eq string
}

// Solver is a stateful unification session. Variables and rigid constants
// are interned by name, so successive Unify calls accumulate constraints
// on the same entities.
type Solver struct {
	varNodes   map[string]*node
	rigidNodes map[string]*node
	edges      map[*node][]proofEdge // provenance graph, union-find independent
}

func NewSolver() *Solver {
	return &Solver{
		varNodes:   map[string]*node{},
		rigidNodes: map[string]*node{},
		edges:      map[*node][]proofEdge{},
	}
}

func newNode(kind Kind, name string, args []*node) *node {
	n := &node{kind: kind, name: name, args: args}
	n.parent = n
	switch kind {
	case KVar:
		n.label = name
	case KRigid:
		n.label = name + "!"
		n.val = n
	case KFun:
		n.label = "(" + args[0].label + " -> " + args[1].label + ")"
		n.val = n
	case KTuple:
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.label
		}
		n.label = "(" + strings.Join(parts, ", ") + ")"
		n.val = n
	}
	return n
}

// intern converts a client Type into the node DAG. Var and rigid nodes are
// shared across the whole session by name; structural nodes are fresh per
// occurrence (sharing identical *Type pointers within one call).
func (s *Solver) intern(t *Type, memo map[*Type]*node) *node {
	if n, ok := memo[t]; ok {
		return n
	}
	var n *node
	switch t.Kind {
	case KVar:
		if existing, ok := s.varNodes[t.Name]; ok {
			n = existing
		} else {
			n = newNode(KVar, t.Name, nil)
			s.varNodes[t.Name] = n
		}
	case KRigid:
		if existing, ok := s.rigidNodes[t.Name]; ok {
			n = existing
		} else {
			n = newNode(KRigid, t.Name, nil)
			s.rigidNodes[t.Name] = n
		}
	default:
		args := make([]*node, len(t.Args))
		for i, a := range t.Args {
			args[i] = s.intern(a, memo)
		}
		n = newNode(t.Kind, "", args)
	}
	memo[t] = n
	return n
}

// find with path halving. Only parent pointers are mutated; the provenance
// graph in s.edges is untouched, so explanations survive compression.
func find(n *node) *node {
	for n.parent != n {
		n.parent = n.parent.parent
		n = n.parent
	}
	return n
}

// link merges two distinct roots whose compatibility was already checked,
// recording the user equation x = y as a provenance edge between the two
// member nodes it named.
func (s *Solver) link(rx, ry, x, y *node) {
	eq := x.label + " = " + y.label
	s.edges[x] = append(s.edges[x], proofEdge{to: y, eq: eq})
	s.edges[y] = append(s.edges[y], proofEdge{to: x, eq: eq})
	if rx.rank < ry.rank {
		rx, ry = ry, rx
	}
	ry.parent = rx
	if rx.rank == ry.rank {
		rx.rank++
	}
	if rx.kind == KVar && ry.kind != KVar {
		rx.kind, rx.name, rx.args = ry.kind, ry.name, ry.args
	}
	if rx.val == nil {
		rx.val = ry.val
	}
}

// ctxEntry is one enclosing structural equation above a failure.
type ctxEntry struct {
	x, y *node
	eq   string
}

// failure describes a single failed equation.
type failure struct {
	reason string
	x, y   *node // member nodes of the failing equation
	rx, ry *node // their class roots
	ctx    []ctxEntry
	occurs bool
	ox     *node // node inside ry's structure with find(ox) == rx
	opath  []int // argument path from ry down to ox
}

// Conflict is the user-facing failure report: a reason plus the shortest
// equation chain explaining it.
type Conflict struct {
	Reason string
	Chain  []string
}

func (c *Conflict) Error() string {
	var b strings.Builder
	b.WriteString("unification conflict: " + c.Reason)
	for i, eq := range c.Chain {
		fmt.Fprintf(&b, "\n  %d. %s", i+1, eq)
	}
	return b.String()
}

// Unify merges the constraint a = b into the session. It returns nil on
// success, or a Conflict carrying the shortest explanation chain.
func (s *Solver) Unify(a, b *Type) *Conflict {
	memo := map[*Type]*node{}
	na, nb := s.intern(a, memo), s.intern(b, memo)
	if f := s.unifyNodes(na, nb); f != nil {
		return s.buildConflict(f)
	}
	return nil
}

func (s *Solver) unifyNodes(x, y *node) *failure {
	rx, ry := find(x), find(y)
	if rx == ry {
		return nil
	}
	switch {
	case rx.kind == KVar && ry.kind == KVar:
		s.link(rx, ry, x, y)
	case rx.kind == KVar:
		if f := s.bindVar(rx, ry, x, y); f != nil {
			return f
		}
	case ry.kind == KVar:
		if f := s.bindVar(ry, rx, y, x); f != nil {
			return f
		}
	case rx.kind == KRigid && ry.kind == KRigid && rx.name == ry.name:
		s.link(rx, ry, x, y)
	case rx.kind == KRigid || ry.kind == KRigid:
		return &failure{
			reason: fmt.Sprintf("rigid variable %s cannot be bound to %s",
				s.renderNode(rx), s.renderNode(ry)),
			x: x, y: y, rx: rx, ry: ry,
		}
	default:
		if rx.kind != ry.kind || len(rx.args) != len(ry.args) {
			return &failure{
				reason: fmt.Sprintf("shape mismatch: %s vs %s",
					s.renderNode(rx), s.renderNode(ry)),
				x: x, y: y, rx: rx, ry: ry,
			}
		}
		s.link(rx, ry, x, y)
		eq := x.label + " = " + y.label
		for i := range rx.args {
			if f := s.unifyNodes(rx.args[i], ry.args[i]); f != nil {
				f.ctx = append([]ctxEntry{{x: x, y: y, eq: eq}}, f.ctx...)
				return f
			}
		}
	}
	return nil
}

// bindVar links variable root v to non-variable root other, performing the
// occurs check first so no infinite type is ever constructed.
func (s *Solver) bindVar(v, other, x, y *node) *failure {
	if other.kind != KRigid {
		if ox, path, ok := s.occursPath(v, other); ok {
			return &failure{
				reason: fmt.Sprintf("occurs check: %s occurs in %s (infinite type rejected)",
					s.renderNode(v), s.renderNode(other)),
				x: x, y: y, rx: v, ry: other,
				occurs: true, ox: ox, opath: path,
			}
		}
	}
	s.link(v, other, x, y)
	return nil
}

// occursPath BFS-searches other's reachable DAG for a node in v's class,
// returning the shortest argument path to it.
func (s *Solver) occursPath(v, root *node) (*node, []int, bool) {
	type item struct {
		n    *node
		path []int
	}
	seen := map[*node]bool{}
	queue := []item{{n: root}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		r := find(cur.n)
		if r == v {
			return cur.n, cur.path, true
		}
		if seen[r] {
			continue
		}
		seen[r] = true
		for i, a := range r.args {
			p := make([]int, len(cur.path)+1)
			copy(p, cur.path)
			p[len(cur.path)] = i
			queue = append(queue, item{n: a, path: p})
		}
	}
	return nil, nil, false
}

// chainBetween returns the shortest sequence of recorded equations leading
// from node a to node b inside their shared class (BFS on the provenance
// graph). It is empty when a and b coincide or live in different classes.
func (s *Solver) chainBetween(a, b *node) []string {
	if a == b {
		return nil
	}
	prev := map[*node]*node{a: nil}
	via := map[*node]string{}
	queue := []*node{a}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == b {
			break
		}
		for _, e := range s.edges[cur] {
			if _, ok := prev[e.to]; !ok {
				prev[e.to] = cur
				via[e.to] = e.eq
				queue = append(queue, e.to)
			}
		}
	}
	if _, ok := prev[b]; !ok {
		return nil
	}
	var chain []string
	for n := b; n != a; n = prev[n] {
		chain = append([]string{via[n]}, chain...)
	}
	return chain
}

// explainValue returns the shortest chain of equations showing why the
// class of n carries its current structure/rigid value.
func (s *Solver) explainValue(n *node) []string {
	r := find(n)
	if r.val == nil || r.val == n {
		return nil
	}
	return s.chainBetween(n, r.val)
}

func (s *Solver) buildConflict(f *failure) *Conflict {
	c := &Conflict{Reason: f.reason}
	if f.occurs {
		for _, e := range f.ctx {
			c.Chain = append(c.Chain, s.explainValue(e.x)...)
			c.Chain = append(c.Chain, e.eq)
		}
		// Explain every structured class along the occurrence path.
		cur := f.ry
		for _, idx := range f.opath {
			c.Chain = append(c.Chain, s.explainValue(cur)...)
			r := find(cur)
			if idx >= len(r.args) {
				break
			}
			cur = r.args[idx]
		}
		c.Chain = append(c.Chain, s.chainBetween(f.rx, f.ox)...)
		c.Chain = append(c.Chain, fmt.Sprintf("but %s = %s was required, and %s re-occurs at path %v",
			f.x.label, f.y.label, f.x.label, f.opath))
		return c
	}
	for _, e := range f.ctx {
		c.Chain = append(c.Chain, s.explainValue(e.x)...)
		c.Chain = append(c.Chain, e.eq)
	}
	c.Chain = append(c.Chain, s.explainValue(f.x)...)
	c.Chain = append(c.Chain, s.explainValue(f.y)...)
	c.Chain = append(c.Chain, fmt.Sprintf("but %s = %s was required (clash: %s vs %s)",
		f.x.label, f.y.label, s.renderNode(f.rx), s.renderNode(f.ry)))
	return c
}

// renderNode fully resolves a node's class to a Type and prints it.
func (s *Solver) renderNode(n *node) string {
	return s.resolveNode(n).String()
}

// resolveNode reads a (necessarily acyclic, thanks to the occurs check)
// class back out as an immutable Type.
func (s *Solver) resolveNode(n *node) *Type {
	r := find(n)
	switch r.kind {
	case KVar:
		return Var(r.name)
	case KRigid:
		return Rigid(r.name)
	default:
		t := &Type{Kind: r.kind, Args: make([]*Type, len(r.args))}
		for i, a := range r.args {
			t.Args[i] = s.resolveNode(a)
		}
		return t
	}
}

// Resolve reads back the current value of any type term in this session.
func (s *Solver) Resolve(t *Type) *Type {
	return s.resolveNode(s.intern(t, map[*Type]*node{}))
}

// Subst extracts the most general unifier as an idempotent substitution
// over the flexible variables seen so far. Variables mapped to themselves
// (free representatives) are omitted.
func (s *Solver) Subst() Subst {
	out := Subst{}
	for name, n := range s.varNodes {
		t := s.resolveNode(n)
		if t.Kind == KVar && t.Name == name {
			continue
		}
		out[name] = t
	}
	return out
}
