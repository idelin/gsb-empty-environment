package fparse

import (
	"fmt"
	"sort"
	"strings"
)

// Value is a feature value: either a constant atom or a variable.
// Variables are identified by integer IDs; constants by name.
type Value struct {
	IsVar bool
	Const string
	Var   int
}

// ConstVal builds a constant value.
func ConstVal(s string) Value { return Value{Const: s} }

// VarVal builds a variable value with the given ID.
func VarVal(id int) Value { return Value{IsVar: true, Var: id} }

func (v Value) String() string {
	if v.IsVar {
		return fmt.Sprintf("?%d", v.Var)
	}
	return v.Const
}

// Subst maps variable IDs to values. It is treated as immutable during
// unification: every branch of the search works on its own copy, so a
// failed or sibling branch can never pollute another one.
type Subst map[int]Value

func (s Subst) clone() Subst {
	out := make(Subst, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// resolve follows variable chains until a constant or an unbound variable.
func (s Subst) resolve(v Value) Value {
	for v.IsVar {
		next, ok := s[v.Var]
		if !ok || (next.IsVar && next.Var == v.Var) {
			return v
		}
		v = next
	}
	return v
}

// bind returns a copy of s with id bound to v; s itself is not modified.
func (s Subst) bind(id int, v Value) Subst {
	out := s.clone()
	out[id] = v
	return out
}

// unifyValues unifies a and b under s, returning an extended copy of s.
func unifyValues(a, b Value, s Subst) (Subst, bool) {
	a = s.resolve(a)
	b = s.resolve(b)
	switch {
	case a.IsVar:
		return s.bind(a.Var, b), true
	case b.IsVar:
		return s.bind(b.Var, a), true
	case a.Const == b.Const:
		return s, true
	default:
		return nil, false
	}
}

// FeatureMap is a finite map from attribute names to values.
type FeatureMap map[string]Value

// unifyFeatures merges a and b under subst s. Keys present in both maps
// must unify; a conflict on such a duplicate key fails the whole
// unification. The input subst is never mutated.
func unifyFeatures(a, b FeatureMap, s Subst) (merged FeatureMap, out Subst, ok bool) {
	merged = make(FeatureMap, len(a)+len(b))
	for k, v := range a {
		merged[k] = v
	}
	cur := s
	for k, v := range b {
		if old, dup := merged[k]; dup {
			ns, ok := unifyValues(old, v, cur)
			if !ok {
				return nil, nil, false
			}
			cur = ns
			merged[k] = cur.resolve(old)
		} else {
			merged[k] = v
		}
	}
	return merged, cur, true
}

func sortedKeys(f FeatureMap) []string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// canonical resolves every value through s and renames the remaining free
// variables deterministically (in sorted key order), so that two feature
// maps that differ only by a renaming of variables — equivalent
// substitutions — canonicalize to the same map and can be deduplicated.
func canonical(f FeatureMap, s Subst) FeatureMap {
	out := make(FeatureMap, len(f))
	renaming := map[int]int{}
	for _, k := range sortedKeys(f) {
		v := s.resolve(f[k])
		if v.IsVar {
			id, ok := renaming[v.Var]
			if !ok {
				id = len(renaming)
				renaming[v.Var] = id
			}
			out[k] = VarVal(id)
		} else {
			out[k] = v
		}
	}
	return out
}

// key is a stable string form of a (canonical) feature map, used for
// deduplication of forest nodes and trees.
func (f FeatureMap) key() string {
	var b strings.Builder
	for _, k := range sortedKeys(f) {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(f[k].String())
		b.WriteByte(';')
	}
	return b.String()
}

func (f FeatureMap) String() string {
	if len(f) == 0 {
		return ""
	}
	parts := make([]string, 0, len(f))
	for _, k := range sortedKeys(f) {
		parts = append(parts, k+"="+f[k].String())
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// varGen hands out fresh variable IDs so that every rule instantiation and
// every read of a stored node's variables works on freshly renamed
// variables, never on shared ones.
type varGen struct{ next int }

// freshen returns a copy of f in which every variable is replaced by a
// fresh, previously unused variable ID (consistently within the map).
func (g *varGen) freshen(f FeatureMap) FeatureMap {
	return g.newFreshener().freshen(f)
}

// freshener renames variables consistently across several feature maps,
// so that a rule-local variable keeps one fresh identity throughout a
// single instantiation of the whole rule (LHS and every RHS item).
type freshener struct {
	gen     *varGen
	renamed map[int]int
}

func (g *varGen) newFreshener() *freshener {
	return &freshener{gen: g, renamed: map[int]int{}}
}

func (fr *freshener) freshen(f FeatureMap) FeatureMap {
	out := make(FeatureMap, len(f))
	for k, v := range f {
		if v.IsVar {
			id, ok := fr.renamed[v.Var]
			if !ok {
				id = fr.gen.next
				fr.gen.next++
				fr.renamed[v.Var] = id
			}
			out[k] = VarVal(id)
		} else {
			out[k] = v
		}
	}
	return out
}
