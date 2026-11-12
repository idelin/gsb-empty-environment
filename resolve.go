package netsec

import "strings"

// splitField splits a dotted field path into its components. An empty field
// path yields nil.
func splitField(field string) []string {
	if field == "" {
		return nil
	}
	return strings.Split(field, ".")
}

// Resolve maps a root expression plus a dotted field path to the set of
// concrete (object, remaining-field) targets it may denote.
//
// Resolution walks the object graph: every reference edge is followed, so two
// roots that reach the same object (aliases) resolve to overlapping target
// sets. Recursive types are handled by tracking the (object, suffix) pairs
// already visited, which keeps the result finite in the presence of cycles.
//
// A chain "a.b.c" where b is a reference edge steps into the referenced
// object; where b is a non-reference field, resolution stops and the remaining
// components become the target's Field suffix.
func (g ObjectGraph) Resolve(root, field string) []Target {
	object, ok := g.Roots[root]
	if !ok {
		return nil
	}
	seen := map[[2]string]bool{}
	var out []Target
	var walk func(string, []string)
	walk = func(obj string, suffix []string) {
		key := [2]string{obj, strings.Join(suffix, ".")}
		if seen[key] {
			return
		}
		seen[key] = true

		if len(suffix) == 0 {
			out = append(out, Target{Object: obj})
			return
		}

		head, rest := suffix[0], suffix[1:]
		if edges, ok := g.Edges[obj]; ok {
			if next, ok := edges[head]; ok {
				walk(next, rest)
				return
			}
		}
		// Non-reference field: the location lives inside obj, addressed by the
		// remaining field path including head.
		out = append(out, Target{Object: obj, Field: strings.Join(suffix, ".")})
	}
	walk(object, splitField(field))
	return dedupTargets(out)
}

func dedupTargets(in []Target) []Target {
	seen := map[Target]bool{}
	out := make([]Target, 0, len(in))
	for _, t := range in {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// Prefix reports whether outer encloses inner: a write to outer affects inner.
// The objects must match and outer's field path must be a prefix (or equal) of
// inner's field path.
func Prefix(outer, inner Target) bool {
	if outer.Object != inner.Object {
		return false
	}
	if outer.Field == "" {
		return true
	}
	if inner.Field == "" {
		return false
	}
	of := strings.Split(outer.Field, ".")
	inf := strings.Split(inner.Field, ".")
	if len(of) > len(inf) {
		return false
	}
	for i := range of {
		if of[i] != inf[i] {
			return false
		}
	}
	return true
}

// overlaps reports whether a write target invalidates a fact target.
func overlaps(write, fact Target) bool {
	return Prefix(write, fact) || Prefix(fact, write)
}
