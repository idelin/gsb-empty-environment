// Package uniparse is a unification-based chart parser for text annotators.
//
// Nonterminals carry finite feature maps whose values are constants or
// rule-local variables. Variables are renamed fresh on every rule
// expansion, conflicting values on a duplicate key fail, and
// substitutions are never shared across branches. The parser returns a
// shared (packed) forest of all unifiable analyses; feature structures
// are canonicalized so alpha-equivalent substitutions are deduplicated.
package uniparse

import (
	"fmt"
	"sort"
	"strings"
)

// Value is a feature value: either a constant or a variable.
type Value struct {
	Variable bool
	Name     string
}

// Const returns a constant feature value.
func Const(name string) Value { return Value{Name: name} }

// Var returns a variable feature value.
func Var(name string) Value { return Value{Variable: true, Name: name} }

func (v Value) String() string {
	if v.Variable {
		return "?" + v.Name
	}
	return v.Name
}

// Feats is a finite feature map attached to a nonterminal.
type Feats map[string]Value

func (f Feats) String() string { return featsKey(f) }

func featsKey(f Feats) string {
	if len(f) == 0 {
		return "{}"
	}
	keys := sortedKeys(f)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(f[k].String())
	}
	b.WriteByte('}')
	return b.String()
}

func sortedKeys(f Feats) []string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// subst maps variable names to values. It is treated as immutable: every
// extension clones first, so sibling branches can never pollute each other.
type subst map[string]Value

// resolve follows variable chains until a constant or an unbound variable.
func (s subst) resolve(v Value) Value {
	for v.Variable {
		next, ok := s[v.Name]
		if !ok {
			return v
		}
		v = next
	}
	return v
}

func cloneSubst(s subst) subst {
	out := make(subst, len(s)+1)
	for k, v := range s {
		out[k] = v
	}
	return out
}

// unifyValue extends s so that a and b become equal. The input substitution
// is never mutated. Two distinct constants fail; anything else succeeds.
func unifyValue(s subst, a, b Value) (subst, bool) {
	a = s.resolve(a)
	b = s.resolve(b)
	if a == b {
		return s, true
	}
	if a.Variable {
		s = cloneSubst(s)
		s[a.Name] = b
		return s, true
	}
	if b.Variable {
		s = cloneSubst(s)
		s[b.Name] = a
		return s, true
	}
	return nil, false
}

// unifyFeats unifies the values of keys present in both maps; a duplicate
// key whose values clash fails the whole unification. Keys present on only
// one side are unconstrained.
func unifyFeats(s subst, a, b Feats) (subst, bool) {
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			continue
		}
		s2, ok := unifyValue(s, va, vb)
		if !ok {
			return nil, false
		}
		s = s2
	}
	return s, true
}

// canonicalSeq resolves every map through s and renames the remaining free
// variables across the whole sequence in first-appearance order (v1, v2,
// ...), so alpha-equivalent feature structures get identical representations
// and duplicate substitutions collapse.
func canonicalSeq(s subst, maps ...Feats) []Feats {
	rename := map[string]string{}
	out := make([]Feats, len(maps))
	for i, f := range maps {
		if len(f) == 0 {
			continue
		}
		cf := make(Feats, len(f))
		for _, k := range sortedKeys(f) {
			v := s.resolve(f[k])
			if v.Variable {
				name, ok := rename[v.Name]
				if !ok {
					name = fmt.Sprintf("v%d", len(rename)+1)
					rename[v.Name] = name
				}
				v = Var(name)
			}
			cf[k] = v
		}
		out[i] = cf
	}
	return out
}

// renamer issues fresh variable names so that every rule expansion works
// with its own variables.
type renamer struct{ n int }

func (r *renamer) fresh() string {
	r.n++
	return fmt.Sprintf("x%d", r.n)
}

// instFeats returns a copy of f with every variable consistently renamed
// to a fresh name (m maps old names to fresh values shared by one
// instantiation).
func instFeats(f Feats, r *renamer, m map[string]Value) Feats {
	if len(f) == 0 {
		return nil
	}
	out := make(Feats, len(f))
	for k, v := range f {
		if v.Variable {
			nv, ok := m[v.Name]
			if !ok {
				nv = Var(r.fresh())
				m[v.Name] = nv
			}
			out[k] = nv
		} else {
			out[k] = v
		}
	}
	return out
}
