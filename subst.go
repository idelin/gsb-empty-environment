package unify

import "strings"

// Subst is an idempotent substitution from flexible variable names to types.
// Images contain no variables from the domain, so Apply(Apply(t)) == Apply(t).
type Subst map[string]*Type

// Apply replaces every flexible variable bound in s throughout t.
func (s Subst) Apply(t *Type) *Type {
	switch t.Kind {
	case Var:
		if img, ok := s[t.Name]; ok {
			return s.Apply(img)
		}
		return t
	case Rigid:
		return t
	default:
		args := make([]*Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = s.Apply(a)
		}
		return &Type{Kind: t.Kind, Args: args}
	}
}

// ApplyTo maps s over its own images.
func (s Subst) ApplyTo(o Subst) Subst {
	out := Subst{}
	for x, img := range o {
		out[x] = s.Apply(img)
	}
	return out
}

// Compose returns g after f: the substitution that applies f first, then g.
// Images are fully applied and identity bindings dropped, so composing
// idempotent substitutions produced by Solver stays idempotent.
func Compose(g, f Subst) Subst {
	out := Subst{}
	for x, img := range f {
		out[x] = g.Apply(img)
	}
	for x, img := range g {
		if _, ok := f[x]; !ok {
			out[x] = g.Apply(img)
		}
	}
	for x, img := range out {
		if img.Kind == Var && img.Name == x {
			delete(out, x)
		}
	}
	return out
}

// Idempotent reports whether applying s to its own images changes nothing.
func (s Subst) Idempotent() bool {
	for _, img := range s {
		if !Equal(s.Apply(img), img) {
			return false
		}
	}
	return true
}

// EqualSubst reports whether two substitutions agree on every bound name.
func EqualSubst(a, b Subst) bool {
	if len(a) != len(b) {
		return false
	}
	for x, img := range a {
		o, ok := b[x]
		if !ok || !Equal(img, o) {
			return false
		}
	}
	return true
}

func (s Subst) String() string {
	if len(s) == 0 {
		return "{}"
	}
	names := make([]string, 0, len(s))
	for x := range s {
		names = append(names, x)
	}
	sortStrings(names)
	parts := make([]string, len(names))
	for i, x := range names {
		parts[i] = x + " := " + s[x].String()
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
