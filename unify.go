package unify

import (
	"fmt"
	"strconv"
	"strings"
)

// Equation is a user-level unification request Left = Right.
type Equation struct {
	ID    int
	Left  *Type
	Right *Type
	Note  string
}

func (e Equation) String() string {
	s := fmt.Sprintf("eq#%d: %s = %s", e.ID, e.Left, e.Right)
	if e.Note != "" {
		s += "  (" + e.Note + ")"
	}
	return s
}

// Step is one link of a conflict chain: the equation that merged two
// equivalence classes, plus where inside that equation the merge happened.
type Step struct {
	Eq    Equation
	Where string
	A, B  *Type
}

// Conflict explains a failed unification. Steps is the shortest chain of
// equations connecting the two conflicting sides; because the proof
// structure is a forest, the chain between any two nodes is unique and
// therefore shortest.
type Conflict struct {
	Reason string
	Detail string
	Steps  []Step
}

func (c *Conflict) Error() string { return c.String() }

func (c *Conflict) String() string {
	var b strings.Builder
	b.WriteString(c.Reason)
	if c.Detail != "" {
		b.WriteString(": " + c.Detail)
	}
	if len(c.Steps) > 0 {
		b.WriteString("\nshortest conflict chain:")
		for i, st := range c.Steps {
			fmt.Fprintf(&b, "\n  %d. %s", i+1, st.Eq)
			if st.Where != "" {
				b.WriteString("  [at " + st.Where + "]")
			}
		}
	}
	return b.String()
}

// pEdge is one edge of the proof forest.
type pEdge struct {
	to   *Type
	step Step
}

// Solver is a union-find based unification engine. The union-find parent
// pointers are path-compressed for speed, but explanations live in a
// separate proof forest that is never compressed, so conflict chains keep
// their original provenance.
type Solver struct {
	parent    map[*Type]*Type
	proof     map[*Type][]pEdge
	flexNode  map[string]*Type
	rigidNode map[string]*Type
	eqSeq     int
}

func NewSolver() *Solver {
	return &Solver{
		parent:    map[*Type]*Type{},
		proof:     map[*Type][]pEdge{},
		flexNode:  map[string]*Type{},
		rigidNode: map[string]*Type{},
	}
}

// Unify requests a = b. It returns nil on success or a *Conflict explaining
// the failure with the shortest chain of equations behind it.
func (s *Solver) Unify(a, b *Type, note string) *Conflict {
	s.eqSeq++
	eq := Equation{ID: s.eqSeq, Left: a, Right: b, Note: note}
	return s.unify(a, b, eq, "", a, b)
}

// Substitution extracts the most general unifier accumulated so far. The
// result is idempotent: applying it twice is the same as applying it once.
func (s *Solver) Substitution() Subst {
	groups := map[*Type][]string{}
	for name, node := range s.flexNode {
		r := s.find(node)
		groups[r] = append(groups[r], name)
	}
	canon := map[*Type]string{}
	for r, names := range groups {
		sortStrings(names)
		canon[r] = names[0]
	}
	sub := Subst{}
	for name, node := range s.flexNode {
		img := s.resolve(node, canon)
		if img.Kind == Var && img.Name == name {
			continue
		}
		sub[name] = img
	}
	return sub
}

func (s *Solver) unify(a, b *Type, eq Equation, where string, origL, origR *Type) *Conflict {
	a = s.canon(a)
	b = s.canon(b)
	ra, rb := s.find(a), s.find(b)
	if ra == rb {
		return nil
	}

	// A rigid variable unifies only with a rigid of the same name; canon
	// already maps same-name rigids to one node, so any rigid reaching
	// this point is a clash.
	if ra.Kind == Rigid || rb.Kind == Rigid {
		return s.fail("rigid variable cannot be bound to an arbitrary type",
			fmt.Sprintf("%s versus %s", ra, rb), eq, where, origL, origR)
	}

	if ra.Kind == Var && rb.Kind != Var {
		if w, pos := s.occurs(ra, rb, nil); w != nil {
			return s.fail("occurs check failed (recursive type rejected)",
				fmt.Sprintf("variable %s occurs at %s in %s", ra.Name, formatPos(pos), rb),
				eq, where, origL, origR)
		}
		s.merge(ra, rb, eq, where)
		return nil
	}
	if rb.Kind == Var && ra.Kind != Var {
		if w, pos := s.occurs(rb, ra, nil); w != nil {
			return s.fail("occurs check failed (recursive type rejected)",
				fmt.Sprintf("variable %s occurs at %s in %s", rb.Name, formatPos(pos), ra),
				eq, where, origL, origR)
		}
		s.merge(rb, ra, eq, where)
		return nil
	}
	if ra.Kind == Var && rb.Kind == Var {
		s.merge(ra, rb, eq, where)
		return nil
	}

	if ra.Kind != rb.Kind || len(ra.Args) != len(rb.Args) {
		return s.fail("type constructor mismatch",
			fmt.Sprintf("%s versus %s", ra, rb), eq, where, origL, origR)
	}
	s.merge(ra, rb, eq, where)
	for i := range ra.Args {
		sub := where
		if sub != "" {
			sub += "."
		}
		sub += "arg" + strconv.Itoa(i)
		if c := s.unify(ra.Args[i], rb.Args[i], eq, sub, origL, origR); c != nil {
			return c
		}
	}
	return nil
}

// canon maps same-name variables (flexible and rigid) to a single canonical
// node, so distinct *Type pointers with the same name behave as one variable.
func (s *Solver) canon(t *Type) *Type {
	switch t.Kind {
	case Var:
		if n, ok := s.flexNode[t.Name]; ok {
			return n
		}
		s.flexNode[t.Name] = t
	case Rigid:
		if n, ok := s.rigidNode[t.Name]; ok {
			return n
		}
		s.rigidNode[t.Name] = t
	}
	return t
}

// find returns the class representative, compressing the union-find path.
// The proof forest is deliberately left untouched.
func (s *Solver) find(t *Type) *Type {
	p, ok := s.parent[t]
	if !ok || p == t {
		return t
	}
	root := s.find(p)
	s.parent[t] = root
	return root
}

// merge unions the classes of x and y and records the reason in the proof
// forest. The proof edge is what later lets explain() rebuild why two nodes
// ended up in the same class.
func (s *Solver) merge(x, y *Type, eq Equation, where string) {
	rx, ry := s.find(x), s.find(y)
	if rx == ry {
		return
	}
	s.parent[rx] = ry
	step := Step{Eq: eq, Where: where, A: x, B: y}
	s.proof[x] = append(s.proof[x], pEdge{to: y, step: step})
	s.proof[y] = append(s.proof[y], pEdge{to: x, step: step})
}

// occurs reports whether the class of variable v occurs inside the type
// rooted at t. On success it returns the offending variable node and its
// argument-position path within t.
func (s *Solver) occurs(v, t *Type, pos []int) (*Type, []int) {
	t = s.canon(t)
	rt := s.find(t)
	if rt == v {
		return t, pos
	}
	if rt.Kind != Fun && rt.Kind != Tuple {
		return nil, nil
	}
	for i, arg := range rt.Args {
		p := append(append([]int{}, pos...), i)
		if w, fp := s.occurs(v, arg, p); w != nil {
			return w, fp
		}
	}
	return nil, nil
}

// explain returns the shortest chain of proof steps connecting from and to
// in the proof forest (BFS; each class forms a tree so the path is unique).
// Path compression of the union-find never touches these edges.
func (s *Solver) explain(from, to *Type) []Step {
	if from == to {
		return nil
	}
	prev := map[*Type]pEdge{from: {}}
	queue := []*Type{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range s.proof[cur] {
			if _, seen := prev[e.to]; seen {
				continue
			}
			prev[e.to] = pEdge{to: cur, step: e.step}
			if e.to == to {
				var steps []Step
				for n := to; n != from; {
					pe := prev[n]
					steps = append([]Step{pe.step}, steps...)
					n = pe.to
				}
				return steps
			}
			queue = append(queue, e.to)
		}
	}
	return nil
}

// fail builds a Conflict whose chain explains the failed equation in terms
// of the original user-level equation sides origL = origR. If earlier merges
// already connected the two sides, the chain is the (unique, hence shortest)
// proof-forest path between them plus the failing step; otherwise it is the
// evidence leading each side to its representative, closed by the failing
// equation itself.
func (s *Solver) fail(reason, detail string, eq Equation, where string, origL, origR *Type) *Conflict {
	ol, or := s.canon(origL), s.canon(origR)
	last := Step{Eq: eq, Where: where}
	var steps []Step
	if s.find(ol) == s.find(or) {
		steps = s.explain(ol, or)
		steps = append(steps, last)
	} else {
		steps = s.explain(ol, s.find(ol))
		steps = append(steps, last)
		right := s.explain(or, s.find(or))
		for i := len(right) - 1; i >= 0; i-- {
			steps = append(steps, right[i])
		}
	}
	return &Conflict{Reason: reason, Detail: detail, Steps: steps}
}

func formatPos(pos []int) string {
	if len(pos) == 0 {
		return "the root"
	}
	parts := make([]string, len(pos))
	for i, p := range pos {
		parts[i] = "arg" + strconv.Itoa(p)
	}
	return strings.Join(parts, ".")
}

func (s *Solver) resolve(t *Type, canon map[*Type]string) *Type {
	t = s.canon(t)
	rt := s.find(t)
	switch rt.Kind {
	case Var:
		return &Type{Kind: Var, Name: canon[rt]}
	case Rigid:
		return &Type{Kind: Rigid, Name: rt.Name}
	default:
		args := make([]*Type, len(rt.Args))
		for i, a := range rt.Args {
			args[i] = s.resolve(a, canon)
		}
		return &Type{Kind: rt.Kind, Args: args}
	}
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
