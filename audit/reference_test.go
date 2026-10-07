package audit

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// referenceCompare is an independent implementation used to cross-check the
// recursive walker. It expands both snapshots into the full set of node
// paths (leaf-by-leaf rule expansion) and applies the rules per path:
//
//   - immutable: every node path matching an Immutable pattern must have
//     equal values on both sides (a missing side counts as a difference);
//   - add: every added root (a node present only in the new snapshot whose
//     parent exists as a compatible container in the old one) must match an
//     AllowAdd pattern;
//   - remove: mirror of add against AllowRemove;
//   - modify: always permitted.
//
// The raw violations are then collapsed to the shallowest set exactly like
// the public API promises.
func referenceCompare(oldV, newV interface{}, r Rules) []Violation {
	var imm, aa, ar []pattern
	for _, s := range r.Immutable {
		p, err := parsePattern(s)
		if err != nil {
			panic(err)
		}
		imm = append(imm, p)
	}
	for _, s := range r.AllowAdd {
		p, err := parsePattern(s)
		if err != nil {
			panic(err)
		}
		aa = append(aa, p)
	}
	for _, s := range r.AllowRemove {
		p, err := parsePattern(s)
		if err != nil {
			panic(err)
		}
		ar = append(ar, p)
	}

	oldNodes := map[string]path{}
	oldVals := map[string]interface{}{}
	newNodes := map[string]path{}
	newVals := map[string]interface{}{}
	collectAll(oldV, nil, oldNodes, oldVals)
	collectAll(newV, nil, newNodes, newVals)

	var out []Violation

	// Immutable pass over the union of node paths.
	for k, p := range oldNodes {
		if _, ok := newNodes[k]; !ok {
			out = appendImmutable(out, imm, p, oldVals[k], true, nil, false)
			continue
		}
		out = appendImmutable(out, imm, p, oldVals[k], true, newVals[k], true)
	}
	for k, p := range newNodes {
		if _, ok := oldNodes[k]; !ok {
			out = appendImmutable(out, imm, p, nil, false, newVals[k], true)
		}
	}

	// Add pass: roots present only in the new snapshot.
	for k, p := range newNodes {
		if _, ok := oldNodes[k]; ok || len(p) == 0 {
			continue
		}
		parent := p[:len(p)-1]
		pv, ok := oldVals[parent.key()]
		if !ok || !containerHolds(pv, p[len(p)-1]) {
			continue
		}
		if matchAny(aa, p) {
			continue
		}
		out = append(out, Violation{Path: p.String(), Kind: KindAdd, Old: Absent, New: newVals[k]})
	}

	// Remove pass: roots present only in the old snapshot.
	for k, p := range oldNodes {
		if _, ok := newNodes[k]; ok || len(p) == 0 {
			continue
		}
		parent := p[:len(p)-1]
		pv, ok := newVals[parent.key()]
		if !ok || !containerHolds(pv, p[len(p)-1]) {
			continue
		}
		if matchAny(ar, p) {
			continue
		}
		out = append(out, Violation{Path: p.String(), Kind: KindRemove, Old: oldVals[k], New: Absent})
	}

	return collapseReference(out)
}

func collectAll(v interface{}, p path, nodes map[string]path, vals map[string]interface{}) {
	nodes[p.key()] = p
	vals[p.key()] = v
	switch t := v.(type) {
	case map[string]interface{}:
		for k, child := range t {
			collectAll(child, extendPath(p, keySegment(k)), nodes, vals)
		}
	case []interface{}:
		for i, child := range t {
			collectAll(child, extendPath(p, indexSegment(i)), nodes, vals)
		}
	}
}

// containerHolds reports whether v is a container able to directly hold the
// given child segment (a map for keys, a slice for indices).
func containerHolds(v interface{}, s segment) bool {
	if s.isIndex {
		_, ok := v.([]interface{})
		return ok
	}
	_, ok := v.(map[string]interface{})
	return ok
}

func appendImmutable(out []Violation, imm []pattern, p path, oldV interface{}, oldOK bool, newV interface{}, newOK bool) []Violation {
	if !matchAny(imm, p) {
		return out
	}
	if oldOK && newOK && reflect.DeepEqual(oldV, newV) {
		return out
	}
	v := Violation{Path: p.String(), Kind: KindImmutable, Old: Absent, New: Absent}
	if oldOK {
		v.Old = oldV
	}
	if newOK {
		v.New = newV
	}
	return append(out, v)
}

// collapseReference dedups by path (immutable > remove > add), drops
// violations covered by a strict ancestor, and sorts shallowest first.
func collapseReference(vs []Violation) []Violation {
	byPath := map[string]Violation{}
	for _, v := range vs {
		if cur, ok := byPath[v.Path]; !ok || kindPriority(v.Kind) < kindPriority(cur.Kind) {
			byPath[v.Path] = v
		}
	}
	kept := make([]Violation, 0, len(byPath))
	for _, v := range byPath {
		covered := false
		for _, u := range byPath {
			if u.Path != v.Path && isStrictPrefixPath(u.Path, v.Path) {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, v)
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		di, dj := pathDepth(kept[i].Path), pathDepth(kept[j].Path)
		if di != dj {
			return di < dj
		}
		return kept[i].Path < kept[j].Path
	})
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// isStrictPrefixPath reports whether a names a strict ancestor of b. Both
// are produced by path.String, so segment boundaries are unambiguous here:
// "$" is prefix of everything; "$.a" prefixes "$.a." and "$.a[".
func isStrictPrefixPath(a, b string) bool {
	if a == "$" {
		return b != "$"
	}
	return len(b) > len(a) && b[:len(a)] == a && (b[len(a)] == '.' || b[len(a)] == '[')
}

func pathDepth(p string) int {
	if p == "$" {
		return 0
	}
	depth := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '.' || p[i] == '[' {
			depth++
		}
	}
	return depth
}

// --- random cross-check ---------------------------------------------------

var (
	propKeys   = []string{"a", "b", "c"}
	propLeaves = []interface{}{nil, true, false, 0.0, 1.0, "s", "t"}
	propSegs   = []string{"a", "b", "c", "0", "1", "*"}
)

func randomValue(rng *rand.Rand, depth int) interface{} {
	if depth >= 4 {
		return propLeaves[rng.Intn(len(propLeaves))]
	}
	switch rng.Intn(6) {
	case 0, 1:
		return propLeaves[rng.Intn(len(propLeaves))]
	case 2:
		return map[string]interface{}{}
	case 3:
		return []interface{}{}
	case 4:
		m := map[string]interface{}{}
		for i, n := 0, rng.Intn(4); i < n; i++ {
			m[propKeys[rng.Intn(len(propKeys))]] = randomValue(rng, depth+1)
		}
		return m
	default:
		n := rng.Intn(3)
		s := make([]interface{}, n)
		for i := range s {
			s[i] = randomValue(rng, depth+1)
		}
		return s
	}
}

func randomPattern(rng *rand.Rand) string {
	n := rng.Intn(4)
	if n == 0 {
		return "$"
	}
	s := "$"
	for i := 0; i < n; i++ {
		seg := propSegs[rng.Intn(len(propSegs))]
		if isDigits(seg) {
			s += "[" + seg + "]"
		} else if seg == "*" && rng.Intn(2) == 0 {
			s += "[*]"
		} else {
			s += "." + seg
		}
	}
	return s
}

func randomRules(rng *rand.Rand) Rules {
	var r Rules
	for i, n := 0, rng.Intn(4); i < n; i++ {
		r.Immutable = append(r.Immutable, randomPattern(rng))
	}
	for i, n := 0, rng.Intn(3); i < n; i++ {
		r.AllowAdd = append(r.AllowAdd, randomPattern(rng))
	}
	for i, n := 0, rng.Intn(3); i < n; i++ {
		r.AllowRemove = append(r.AllowRemove, randomPattern(rng))
	}
	return r
}

func violationsEqual(a, b []Violation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path || a[i].Kind != b[i].Kind {
			return false
		}
		if !reflect.DeepEqual(a[i].Old, b[i].Old) || !reflect.DeepEqual(a[i].New, b[i].New) {
			return false
		}
	}
	return true
}

// TestPropertyLeafExpansion cross-checks the recursive walker against the
// independent leaf-expansion reference on random snapshots and rules.
func TestPropertyLeafExpansion(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 5000; iter++ {
		oldV := randomValue(rng, 0)
		newV := randomValue(rng, 0)
		rules := randomRules(rng)

		got, err := Compare(oldV, newV, rules)
		if err != nil {
			t.Fatalf("iter %d: Compare: %v", iter, err)
		}
		want := referenceCompare(oldV, newV, rules)
		if !violationsEqual(got, want) {
			t.Fatalf("iter %d mismatch\nrules: %+v\nold: %#v\nnew: %#v\ngot:  %v\nwant: %v",
				iter, rules, oldV, newV, got, want)
		}
	}
}

// TestPropertyShallowest verifies the output invariant directly: no reported
// violation is a descendant of another, and paths are sorted shallowest
// first.
func TestPropertyShallowest(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 2000; iter++ {
		oldV := randomValue(rng, 0)
		newV := randomValue(rng, 0)
		rules := randomRules(rng)
		got, err := Compare(oldV, newV, rules)
		if err != nil {
			t.Fatalf("iter %d: Compare: %v", iter, err)
		}
		for i, v := range got {
			for j, u := range got {
				if i != j && isStrictPrefixPath(u.Path, v.Path) {
					t.Fatalf("iter %d: %s covers %s", iter, u.Path, v.Path)
				}
			}
			if i > 0 {
				prev := got[i-1]
				if pathDepth(prev.Path) > pathDepth(v.Path) {
					t.Fatalf("iter %d: not sorted shallowest first: %v", iter, got)
				}
			}
		}
	}
}

func ExampleCompare() {
	oldV := map[string]interface{}{"spec": map[string]interface{}{"replicas": 1.0}}
	newV := map[string]interface{}{"spec": map[string]interface{}{"replicas": 2.0}}
	vs, _ := Compare(oldV, newV, Rules{Immutable: []string{"$.spec.replicas"}})
	for _, v := range vs {
		fmt.Println(v)
	}
	// Output: immutable violation at $.spec.replicas: old=1 new=2
}
