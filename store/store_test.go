package store

import (
	"fmt"
	"sort"
	"testing"
)

// ---- targeted scenario tests ----

func TestABA(t *testing.T) {
	s := New()
	if !s.CompareAndSwap("k", 0, 1) { // create at version 1
		t.Fatal("create failed")
	}
	if !s.CompareAndSwap("k", 1, 2) { // update to version 2
		t.Fatal("update failed")
	}
	if !s.CompareAndDelete("k", 2) { // delete bumps to version 3
		t.Fatal("delete failed")
	}
	if _, v, exists := s.Get("k"); exists || v != 3 {
		t.Fatalf("missing version after delete = %v, exists=%v; want 3, false", v, exists)
	}
	if !s.CompareAndSwap("k", 3, 1) { // recreate with same value as before
		t.Fatal("recreate failed")
	}
	// The stale condition {k, 2} described the same value before the
	// delete; it must never hold again.
	if s.Txn([]Condition{{Key: "k", Version: 2}}, nil) {
		t.Fatal("stale pre-delete condition held after delete+recreate (ABA)")
	}
	if s.CompareAndSwap("k", 1, 9) {
		t.Fatal("stale create-time condition held after delete+recreate (ABA)")
	}
}

func TestEmptyBatch(t *testing.T) {
	s := New()
	if !s.Txn(nil, nil) {
		t.Fatal("empty txn should succeed")
	}
	if !s.Txn(nil, []Write{{Key: "a", Value: 7}}) {
		t.Fatal("condition-free txn should always succeed")
	}
	if got, v, ok := s.Get("a"); !ok || got != 7 || v != 1 {
		t.Fatalf("got (%v,%v,%v)", got, v, ok)
	}
}

func TestSameKeyConflictingConditions(t *testing.T) {
	s := New()
	s.CompareAndSwap("k", 0, 1) // k at version 1
	ok := s.Txn(
		[]Condition{{Key: "k", Version: 1}, {Key: "k", Version: 2}},
		[]Write{{Key: "other", Value: 5}},
	)
	if ok {
		t.Fatal("conflicting same-key conditions must not both hold")
	}
	if _, _, exists := s.Get("other"); exists {
		t.Fatal("failed batch must not write anything")
	}
}

func TestMissingVersionIndependence(t *testing.T) {
	s := New()
	s.CompareAndSwap("a", 0, 1)
	s.CompareAndDelete("a", 1)
	if v := s.VersionOf("b"); v != 0 {
		t.Fatalf("key b missing version = %v, want 0 (must be independent of a)", v)
	}
	if v := s.VersionOf("a"); v != 2 {
		t.Fatalf("key a missing version = %v, want 2", v)
	}
	// Absence can be asserted via the missing version.
	if !s.Txn([]Condition{{Key: "a", Version: 2}}, []Write{{Key: "b", Value: 1}}) {
		t.Fatal("condition on missing version should hold")
	}
}

func TestBatchAtomicity(t *testing.T) {
	s := New()
	s.CompareAndSwap("a", 0, 1) // a at version 1
	ok := s.Txn(
		[]Condition{{Key: "a", Version: 1}, {Key: "b", Version: 9}},
		[]Write{{Key: "a", Value: 2}, {Key: "c", Value: 3}},
	)
	if ok {
		t.Fatal("batch with a failing condition must fail")
	}
	if got, _, _ := s.Get("a"); got != 1 {
		t.Fatal("failed batch mutated a")
	}
	if _, _, exists := s.Get("c"); exists {
		t.Fatal("failed batch created c")
	}
}

// ---- exhaustive small-state verification ----

var (
	exKeys   = []string{"a", "b"}
	exValues = []int{0, 1}
)

type op struct {
	conds  []Condition
	writes []Write
}

// alphabet is the full set of operations explored: unconditional writes,
// multi-write batches (including delete+recreate in one batch), pure
// condition checks, conditional writes, empty txns, and conflicting
// same-key condition pairs.
func alphabet() []op {
	var ops []op
	ops = append(ops, op{}) // empty txn
	for _, k := range exKeys {
		for _, v := range exValues {
			ops = append(ops, op{writes: []Write{{Key: k, Value: v}}})
		}
		ops = append(ops, op{writes: []Write{{Key: k, Del: true}}})
	}
	ops = append(ops,
		op{writes: []Write{{Key: "a", Value: 1}, {Key: "b", Value: 1}}},
		op{writes: []Write{{Key: "a", Del: true}, {Key: "a", Value: 1}}},
		op{writes: []Write{{Key: "a", Value: 1}, {Key: "a", Del: true}}},
	)
	condVersions := []Version{0, 1, 2}
	var condSets [][]Condition
	condSets = append(condSets, nil)
	for _, k := range exKeys {
		for _, v := range condVersions {
			condSets = append(condSets, []Condition{{Key: k, Version: v}})
		}
	}
	for _, v1 := range condVersions {
		for _, v2 := range condVersions {
			condSets = append(condSets,
				[]Condition{{Key: "a", Version: v1}, {Key: "b", Version: v2}})
		}
	}
	condSets = append(condSets,
		[]Condition{{Key: "a", Version: 0}, {Key: "a", Version: 1}},
		[]Condition{{Key: "a", Version: 1}, {Key: "a", Version: 2}},
	)
	for _, cs := range condSets {
		ops = append(ops, op{conds: cs})
		for _, k := range exKeys {
			ops = append(ops,
				op{conds: cs, writes: []Write{{Key: k, Value: 1}}},
				op{conds: cs, writes: []Write{{Key: k, Del: true}}},
			)
		}
	}
	return ops
}

type snapshot map[string]record

func snapOf(s *Store) snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn := make(snapshot, len(s.records))
	for k, r := range s.records {
		sn[k] = *r
	}
	return sn
}

func storeFrom(sn snapshot) *Store {
	s := New()
	for k, r := range sn {
		rec := r
		s.records[k] = &rec
	}
	return s
}

func (sn snapshot) versionOf(k string) Version {
	if r, ok := sn[k]; ok {
		return r.version
	}
	return 0
}

func (sn snapshot) key() string {
	keys := make([]string, 0, len(sn))
	for k := range sn {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for _, k := range keys {
		r := sn[k]
		out += fmt.Sprintf("%s:%d:%d:%v;", k, r.version, r.value, r.exists)
	}
	return out
}

func equalSnapshot(a, b snapshot) bool {
	if len(a) != len(b) {
		return false
	}
	for k, ra := range a {
		rb, ok := b[k]
		if !ok || ra != rb {
			return false
		}
	}
	return true
}

func applyWrites(sn snapshot, writes []Write) snapshot {
	out := make(snapshot, len(sn))
	for k, r := range sn {
		out[k] = r
	}
	for _, w := range writes {
		r := out[w.Key]
		r.version++
		if w.Del {
			r.exists = false
			r.value = 0
		} else {
			r.exists = true
			r.value = w.Value
		}
		out[w.Key] = r
	}
	return out
}

func writtenKeys(writes []Write) map[string]bool {
	m := make(map[string]bool, len(writes))
	for _, w := range writes {
		m[w.Key] = true
	}
	return m
}

// checkTransition verifies every invariant of a single Txn step.
func checkTransition(t *testing.T, pre snapshot, o op, ok bool, post snapshot) {
	t.Helper()

	condsHold := true
	for _, c := range o.conds {
		if pre.versionOf(c.Key) != c.Version {
			condsHold = false
			break
		}
	}
	if ok != condsHold {
		t.Fatalf("txn %+v: applied=%v but conditions hold=%v (pre %s)",
			o, ok, condsHold, pre.key())
	}

	if !condsHold {
		// All-or-nothing: a rejected batch changes nothing.
		if !equalSnapshot(pre, post) {
			t.Fatalf("rejected txn %+v mutated state: %s -> %s", o, pre.key(), post.key())
		}
		return
	}

	// An accepted batch applies exactly its writes, in order.
	want := applyWrites(pre, o.writes)
	if !equalSnapshot(want, post) {
		t.Fatalf("txn %+v: got %s, want %s", o, post.key(), want.key())
	}

	written := writtenKeys(o.writes)
	for _, k := range exKeys {
		pv, qv := pre.versionOf(k), post.versionOf(k)
		// Versions never decrease; written keys strictly increase, so a
		// delete+recreate can never make an old version current again.
		if qv < pv {
			t.Fatalf("txn %+v: version of %q decreased %d -> %d", o, k, pv, qv)
		}
		if written[k] && qv <= pv {
			t.Fatalf("txn %+v: version of written key %q not bumped", o, k)
		}
		// Missing versions are independent: untouched keys keep theirs.
		if !written[k] && qv != pv {
			t.Fatalf("txn %+v: untouched key %q version changed %d -> %d", o, k, pv, qv)
		}
	}

	// Empty batches are no-ops.
	if len(o.conds) == 0 && len(o.writes) == 0 && !equalSnapshot(pre, post) {
		t.Fatalf("empty txn mutated state: %s -> %s", pre.key(), post.key())
	}
}

// TestExhaustive explores every reachable state up to a bounded depth over
// a two-key, two-value universe and checks the invariants on every
// transition: atomic all-or-nothing batches, monotonic per-key versions
// (ABA safety), independent missing versions, empty-batch behavior, and
// rejection of conflicting same-key conditions.
func TestExhaustive(t *testing.T) {
	ops := alphabet()
	const maxDepth = 4

	type node struct {
		sn    snapshot
		depth int
	}
	seen := map[string]bool{}
	root := snapOf(New())
	seen[root.key()] = true
	queue := []node{{root, 0}}
	transitions := 0

	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.depth == maxDepth {
			continue
		}
		for _, o := range ops {
			s := storeFrom(n.sn)
			pre := snapOf(s)
			ok := s.Txn(o.conds, o.writes)
			post := snapOf(s)
			checkTransition(t, pre, o, ok, post)
			transitions++
			k := post.key()
			if !seen[k] {
				seen[k] = true
				queue = append(queue, node{post, n.depth + 1})
			}
		}
	}
	t.Logf("explored %d states, %d transitions, alphabet %d ops", len(seen), transitions, len(ops))
}
