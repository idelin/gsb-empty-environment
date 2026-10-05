package unify

// Subst is a substitution from flexible variable names to types.
// Substitutions produced by Solver.Subst are most general and idempotent:
// no domain variable occurs in any range, so Apply reaches a fixpoint in
// one pass.
type Subst map[string]*Type

// Apply rewrites the variables of t through s. It is safe on arbitrary
// (even non-idempotent) substitutions: variable expansion is chased with a
// cycle guard, and cycles are left as-is rather than looping forever.
func (s Subst) Apply(t *Type) *Type {
	return s.apply(t, map[string]bool{})
}

func (s Subst) apply(t *Type, visiting map[string]bool) *Type {
	switch t.Kind {
	case KVar:
		r, ok := s[t.Name]
		if !ok || visiting[t.Name] {
			return t
		}
		visiting[t.Name] = true
		out := s.apply(r, visiting)
		delete(visiting, t.Name)
		return out
	case KRigid:
		return t
	default:
		out := &Type{Kind: t.Kind, Args: make([]*Type, len(t.Args))}
		for i, a := range t.Args {
			out.Args[i] = s.apply(a, visiting)
		}
		return out
	}
}

// Compose returns t ∘ s: the substitution that applies s first, then t.
// The result is normalised (identity bindings dropped); composing an
// idempotent substitution with itself yields itself.
func (s Subst) Compose(t Subst) Subst {
	out := Subst{}
	for name, v := range s {
		out[name] = t.Apply(v)
	}
	for name, v := range t {
		if _, ok := out[name]; !ok {
			out[name] = v
		}
	}
	for name, v := range out {
		if v.Kind == KVar && v.Name == name {
			delete(out, name)
		}
	}
	return out
}

// Idempotent reports whether no domain variable occurs free in any range,
// i.e. whether Apply(Apply(t)) == Apply(t) for all t.
func (s Subst) Idempotent() bool {
	for _, v := range s {
		for _, name := range freeVars(v) {
			if _, ok := s[name]; ok {
				return false
			}
		}
	}
	return true
}

func freeVars(t *Type) []string {
	var out []string
	var walk func(t *Type)
	walk = func(t *Type) {
		if t.Kind == KVar {
			out = append(out, t.Name)
			return
		}
		for _, a := range t.Args {
			walk(a)
		}
	}
	walk(t)
	return out
}

// EqualSubst reports whether two substitutions agree on every variable in
// either domain.
func EqualSubst(a, b Subst) bool {
	for name, v := range a {
		bv, ok := b[name]
		if !ok || !Equal(v, bv) {
			return false
		}
	}
	for name, v := range b {
		av, ok := a[name]
		if !ok || !Equal(v, av) {
			return false
		}
	}
	return true
}
