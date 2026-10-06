package versionedkv

import (
	"fmt"
	"testing"
)

// This test exhaustively enumerates every action sequence up to exDepth
// over a tiny state space (keys x and y) and, after every single action,
// verifies:
//
//   - ABA safety: a version that stopped being current never becomes
//     current again (tracked via a per-key retired-version set), for both
//     present and missing versions.
//   - Batch semantics: a batch applies iff all conditions held beforehand;
//     a rejected batch leaves values and versions untouched.
//   - Oracle consistency: Get always agrees with a model map.
//
// The action alphabet covers unconditional puts/deletes, CAS with current
// and stale (initial) versions, multi-key batches with one stale condition,
// conflicting duplicate conditions on the same key, empty batches, and
// delete+recreate cycles.
const exDepth = 4

var exKeys = []string{"x", "y"}

type exEnv struct {
	s       *Store
	oracle  map[string]string
	cur     map[string]Version
	retired map[string]map[Version]bool
	init    map[string]Version
}

func newExEnv() *exEnv {
	e := &exEnv{
		s:       New(),
		oracle:  map[string]string{},
		cur:     map[string]Version{},
		retired: map[string]map[Version]bool{},
		init:    map[string]Version{},
	}
	for _, k := range exKeys {
		e.cur[k] = e.s.VersionOf(k)
		e.init[k] = e.s.VersionOf(k)
		e.retired[k] = map[Version]bool{}
	}
	return e
}

func (e *exEnv) clone() *exEnv {
	c := &exEnv{
		s: &Store{
			rev:   e.s.rev,
			items: make(map[string]entry, len(e.s.items)),
			tombs: make(map[string]uint64, len(e.s.tombs)),
		},
		oracle:  make(map[string]string, len(e.oracle)),
		cur:     make(map[string]Version, len(e.cur)),
		retired: make(map[string]map[Version]bool, len(e.retired)),
		init:    make(map[string]Version, len(e.init)),
	}
	for k, v := range e.s.items {
		c.s.items[k] = entry{value: append([]byte(nil), v.value...), rev: v.rev}
	}
	for k, v := range e.s.tombs {
		c.s.tombs[k] = v
	}
	for k, v := range e.oracle {
		c.oracle[k] = v
	}
	for k, v := range e.cur {
		c.cur[k] = v
	}
	for k, v := range e.init {
		c.init[k] = v
	}
	for k, set := range e.retired {
		ns := make(map[Version]bool, len(set))
		for v := range set {
			ns[v] = true
		}
		c.retired[k] = ns
	}
	return c
}

// check verifies the global invariants after an action.
func (e *exEnv) check(t *testing.T, path string) {
	t.Helper()
	for _, k := range exKeys {
		val, ver := e.s.Get(k)
		want, ok := e.oracle[k]
		if ok != ver.Exists || (ok && string(val) != want) {
			t.Errorf("%s: oracle mismatch for %q: got (%q, %v), want (%q, %v)",
				path, k, val, ver.Exists, want, ok)
		}
		if e.retired[k][ver] {
			t.Errorf("%s: ABA violation: version %+v of %q became current again",
				path, ver, k)
		}
		if ver != e.cur[k] {
			e.retired[k][e.cur[k]] = true
			e.cur[k] = ver
		}
	}
}

type exSnap struct {
	val string
	ok  bool
	ver Version
}

func (e *exEnv) snapshot() map[string]exSnap {
	snap := map[string]exSnap{}
	for _, k := range exKeys {
		val, ver := e.s.Get(k)
		snap[k] = exSnap{val: string(val), ok: ver.Exists, ver: ver}
	}
	return snap
}

// apply runs one batch, checks the result against the model prediction,
// checks atomicity on rejection, updates the oracle on success, and finally
// checks the global invariants.
func (e *exEnv) apply(t *testing.T, path string, conds []Condition, writes []Write, wantOK bool) {
	t.Helper()
	snap := e.snapshot()
	gotOK := e.s.Batch(conds, writes)
	if gotOK != wantOK {
		t.Errorf("%s: Batch returned %v, model predicted %v", path, gotOK, wantOK)
	}
	if !gotOK {
		after := e.snapshot()
		for _, k := range exKeys {
			if after[k] != snap[k] {
				t.Errorf("%s: rejected batch mutated %q: %+v -> %+v",
					path, k, snap[k], after[k])
			}
		}
	} else {
		for _, w := range writes {
			if w.delete {
				delete(e.oracle, w.key)
			} else {
				e.oracle[w.key] = string(w.value)
			}
		}
	}
	e.check(t, path)
}

type exAction struct {
	name string
	fn   func(t *testing.T, e *exEnv, path string)
}

func exActions() []exAction {
	acts := []exAction{}
	for _, k := range exKeys {
		k := k
		for _, v := range []string{"1", "2"} {
			v := v
			acts = append(acts, exAction{
				name: fmt.Sprintf("put(%s=%s)", k, v),
				fn: func(t *testing.T, e *exEnv, p string) {
					e.apply(t, p, nil, []Write{Put(k, []byte(v))}, true)
				},
			})
		}
		acts = append(acts, exAction{
			name: "del(" + k + ")",
			fn: func(t *testing.T, e *exEnv, p string) {
				e.apply(t, p, nil, []Write{Delete(k)}, true)
			},
		})
		acts = append(acts, exAction{
			name: "casCur(" + k + ")",
			fn: func(t *testing.T, e *exEnv, p string) {
				e.apply(t, p,
					[]Condition{IfVersion(e.s.VersionOf(k))},
					[]Write{Put(k, []byte("c"))}, true)
			},
		})
		acts = append(acts, exAction{
			name: "casInit(" + k + ")",
			fn: func(t *testing.T, e *exEnv, p string) {
				ok := e.init[k] == e.s.VersionOf(k)
				e.apply(t, p,
					[]Condition{IfVersion(e.init[k])},
					[]Write{Put(k, []byte("s"))}, ok)
			},
		})
		acts = append(acts, exAction{
			name: "conflict(" + k + ")",
			fn: func(t *testing.T, e *exEnv, p string) {
				cur := e.s.VersionOf(k)
				ok := e.init[k] == cur
				e.apply(t, p,
					[]Condition{IfVersion(cur), IfVersion(e.init[k])},
					[]Write{Put(k, []byte("cf"))}, ok)
			},
		})
		acts = append(acts, exAction{
			name: "delRecreate(" + k + ")",
			fn: func(t *testing.T, e *exEnv, p string) {
				e.apply(t, p, nil,
					[]Write{Delete(k), Put(k, []byte("r"))}, true)
			},
		})
	}
	acts = append(acts, exAction{
		name: "batchBothCur",
		fn: func(t *testing.T, e *exEnv, p string) {
			e.apply(t, p,
				[]Condition{IfVersion(e.s.VersionOf("x")), IfVersion(e.s.VersionOf("y"))},
				[]Write{Put("x", []byte("bx")), Put("y", []byte("by"))}, true)
		},
	})
	acts = append(acts, exAction{
		name: "batchCurX+initY",
		fn: func(t *testing.T, e *exEnv, p string) {
			ok := e.init["y"] == e.s.VersionOf("y")
			e.apply(t, p,
				[]Condition{IfVersion(e.s.VersionOf("x")), IfVersion(e.init["y"])},
				[]Write{Put("x", []byte("m")), Put("y", []byte("m"))}, ok)
		},
	})
	acts = append(acts, exAction{
		name: "batchInitX+curY",
		fn: func(t *testing.T, e *exEnv, p string) {
			ok := e.init["x"] == e.s.VersionOf("x")
			e.apply(t, p,
				[]Condition{IfVersion(e.init["x"]), IfVersion(e.s.VersionOf("y"))},
				[]Write{Put("x", []byte("m")), Put("y", []byte("m"))}, ok)
		},
	})
	acts = append(acts, exAction{
		name: "emptyBatch",
		fn: func(t *testing.T, e *exEnv, p string) {
			e.apply(t, p, nil, nil, true)
		},
	})
	return acts
}

func TestExhaustiveSmallState(t *testing.T) {
	acts := exActions()
	var walk func(e *exEnv, path string, depth int)
	walk = func(e *exEnv, path string, depth int) {
		if depth == 0 {
			return
		}
		for _, a := range acts {
			child := e.clone()
			p := path + "/" + a.name
			a.fn(t, child, p)
			walk(child, p, depth-1)
		}
	}
	walk(newExEnv(), "", exDepth)
}
