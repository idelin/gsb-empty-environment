package audit

import (
	"reflect"
	"sort"
	"strconv"
)

// violation is the internal, path-carrying form of Violation.
type violation struct {
	p    path
	kind Kind
	oldV interface{}
	newV interface{}
}

type walker struct {
	checker    *Checker
	violations []violation
}

// walk compares oldV and newV at path p. oldOK/newOK mark presence; a node
// is absent when it was added or removed relative to the other snapshot.
func (w *walker) walk(p path, oldV interface{}, oldOK bool, newV interface{}, newOK bool) {
	if oldOK && newOK && deepEqual(oldV, newV) {
		return
	}
	// An immutable pattern matching this exact path protects its whole
	// subtree. Since ancestors were already checked and passed, p is the
	// shallowest protected path here.
	if matchAny(w.checker.immutable, p) {
		w.add(p, KindImmutable, oldV, oldOK, newV, newOK)
		return
	}
	if !oldOK {
		// Addition rooted at p. Even when allowed, protected descendants
		// created inside the added subtree are still violations.
		w.checkProtectedDescendants(p, nil, false, newV, true)
		if !matchAny(w.checker.allowAdd, p) {
			w.add(p, KindAdd, nil, false, newV, true)
		}
		return
	}
	if !newOK {
		// Removal rooted at p. Protected descendants inside the removed
		// subtree are still checked.
		w.checkProtectedDescendants(p, oldV, true, nil, false)
		if !matchAny(w.checker.allowRemove, p) {
			w.add(p, KindRemove, oldV, true, nil, false)
		}
		return
	}
	oldMap, oldIsMap := oldV.(map[string]interface{})
	newMap, newIsMap := newV.(map[string]interface{})
	if oldIsMap && newIsMap {
		keys := unionKeys(oldMap, newMap)
		for _, k := range keys {
			ov, ook := oldMap[k]
			nv, nok := newMap[k]
			w.walk(extendPath(p, keySegment(k)), ov, ook, nv, nok)
		}
		return
	}
	oldSlice, oldIsSlice := oldV.([]interface{})
	newSlice, newIsSlice := newV.([]interface{})
	if oldIsSlice && newIsSlice {
		n := len(oldSlice)
		if len(newSlice) > n {
			n = len(newSlice)
		}
		for i := 0; i < n; i++ {
			var ov, nv interface{}
			ook, nok := i < len(oldSlice), i < len(newSlice)
			if ook {
				ov = oldSlice[i]
			}
			if nok {
				nv = newSlice[i]
			}
			w.walk(extendPath(p, indexSegment(i)), ov, ook, nv, nok)
		}
		return
	}
	// Scalar change or container/scalar type replacement at p. The
	// replacement itself is a permitted modification, but protected
	// descendants of the old (or new) subtree must still be verified.
	w.checkProtectedDescendants(p, oldV, true, newV, true)
}

// checkProtectedDescendants expands the subtrees at p leaf by leaf and
// reports every immutable-matched path strictly below p whose old and new
// values differ (including paths existing on only one side).
func (w *walker) checkProtectedDescendants(p path, oldSub interface{}, oldOK bool, newSub interface{}, newOK bool) {
	seen := map[string]path{}
	if oldOK {
		collectPaths(oldSub, p, seen)
	}
	if newOK {
		collectPaths(newSub, p, seen)
	}
	for _, q := range seen {
		if !matchAny(w.checker.immutable, q) {
			continue
		}
		ov, ook := subAt(oldSub, oldOK, q[len(p):])
		nv, nok := subAt(newSub, newOK, q[len(p):])
		if !ook || !nok || !deepEqual(ov, nv) {
			w.add(q, KindImmutable, ov, ook, nv, nok)
		}
	}
}

// collectPaths records every strict descendant node path of base.
func collectPaths(v interface{}, base path, out map[string]path) {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, child := range t {
			q := extendPath(base, keySegment(k))
			out[q.key()] = q
			collectPaths(child, q, out)
		}
	case []interface{}:
		for i, child := range t {
			q := extendPath(base, indexSegment(i))
			out[q.key()] = q
			collectPaths(child, q, out)
		}
	}
}

// subAt navigates rel below v, returning the value and whether it exists.
func subAt(v interface{}, ok bool, rel path) (interface{}, bool) {
	if !ok {
		return nil, false
	}
	cur := v
	for _, s := range rel {
		if s.isIndex {
			slice, isSlice := cur.([]interface{})
			if !isSlice {
				return nil, false
			}
			i, err := strconv.Atoi(s.text)
			if err != nil || i < 0 || i >= len(slice) {
				return nil, false
			}
			cur = slice[i]
		} else {
			m, isMap := cur.(map[string]interface{})
			if !isMap {
				return nil, false
			}
			var exists bool
			cur, exists = m[s.text]
			if !exists {
				return nil, false
			}
		}
	}
	return cur, true
}

func (w *walker) add(p path, kind Kind, oldV interface{}, oldOK bool, newV interface{}, newOK bool) {
	v := violation{p: p, kind: kind}
	if oldOK {
		v.oldV = oldV
	} else {
		v.oldV = Absent
	}
	if newOK {
		v.newV = newV
	} else {
		v.newV = Absent
	}
	w.violations = append(w.violations, v)
}

func unionKeys(a, b map[string]interface{}) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func deepEqual(a, b interface{}) bool {
	return reflect.DeepEqual(a, b)
}

// kindPriority orders kinds for same-path dedup: immutable is strongest.
func kindPriority(k Kind) int {
	switch k {
	case KindImmutable:
		return 0
	case KindRemove:
		return 1
	default:
		return 2
	}
}

// finalize collapses raw violations to the shallowest set: duplicates on the
// same path keep the strongest kind, and any violation with a strict
// ancestor also in the set is dropped. The result is sorted by depth, then
// lexicographically.
func finalize(vs []violation) []Violation {
	byPath := map[string]violation{}
	for _, v := range vs {
		k := v.p.key()
		if cur, ok := byPath[k]; !ok || kindPriority(v.kind) < kindPriority(cur.kind) {
			byPath[k] = v
		}
	}
	kept := make([]violation, 0, len(byPath))
	for _, v := range byPath {
		covered := false
		for _, u := range byPath {
			if u.p.key() != v.p.key() && isPrefixOf(u.p, v.p) {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, v)
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		if len(kept[i].p) != len(kept[j].p) {
			return len(kept[i].p) < len(kept[j].p)
		}
		return kept[i].p.String() < kept[j].p.String()
	})
	if len(kept) == 0 {
		return nil
	}
	out := make([]Violation, len(kept))
	for i, v := range kept {
		out[i] = Violation{Path: v.p.String(), Kind: v.kind, Old: v.oldV, New: v.newV}
	}
	return out
}
