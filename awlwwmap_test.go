package awlwwmap

import (
	"reflect"
	"sync"
	"testing"
)

// syncAll merges every replica with every other replica until fixpoint.
func syncAll(replicas ...*Map) {
	for i := 0; i < len(replicas); i++ {
		for j := 0; j < len(replicas); j++ {
			if i != j {
				replicas[i].Merge(replicas[j])
			}
		}
	}
}

func mustGet(t *testing.T, m *Map, key string) map[string]interface{} {
	t.Helper()
	got, ok := m.Get(key)
	if !ok {
		t.Fatalf("expected key %q to be present", key)
	}
	return got
}

// Acceptance boundary: node A removes root key "user_profile" while node
// B concurrently sets user_profile.age=25. The causal merge must prefer
// the update (add-wins): user_profile survives with the age field.
func TestAcceptance_RemoveKeyVsConcurrentFieldUpdate(t *testing.T) {
	a := New("A")
	b := New("B")

	// Both replicas start in sync with an existing profile.
	a.SetAt("user_profile", "name", "alice", 100)
	a.SetAt("user_profile", "city", "shanghai", 101)
	b.Merge(a)

	// Concurrent, causally-unaware operations.
	a.RemoveKey("user_profile")
	b.SetAt("user_profile", "age", 25, 200)

	// Before merge: A sees the key deleted, B sees its update.
	if _, ok := a.Get("user_profile"); ok {
		t.Fatalf("A should see user_profile removed before merge")
	}
	if v, ok := b.GetField("user_profile", "age"); !ok || v != 25 {
		t.Fatalf("B should see age=25 before merge, got %v", v)
	}

	// Causal merge in both directions; replicas must converge.
	syncAll(a, b)

	for _, r := range []*Map{a, b} {
		got := mustGet(t, r, "user_profile")
		if v := got["age"]; v != 25 {
			t.Fatalf("add-wins violated: age=%v, want 25", v)
		}
		// Fields causally observed by the remove stay removed.
		if _, ok := got["name"]; ok {
			t.Fatalf("name was observed by the remove and must stay deleted")
		}
		if _, ok := got["city"]; ok {
			t.Fatalf("city was observed by the remove and must stay deleted")
		}
	}

	// Both replicas converged to identical state.
	ga, _ := a.Get("user_profile")
	gb, _ := b.Get("user_profile")
	if !reflect.DeepEqual(ga, gb) {
		t.Fatalf("replicas diverged: A=%v B=%v", ga, gb)
	}
}

// A remove that causally follows an update still wins (no resurrection).
func TestRemoveWinsWhenCausallyAfterUpdate(t *testing.T) {
	a := New("A")
	b := New("B")

	a.SetAt("user_profile", "age", 25, 100)
	b.Merge(a) // B observes the update
	b.RemoveKey("user_profile")
	a.Merge(b)

	if _, ok := a.Get("user_profile"); ok {
		t.Fatalf("remove causally after update must win")
	}
	if keys := a.Keys(); len(keys) != 0 {
		t.Fatalf("expected no live keys, got %v", keys)
	}
}

// Concurrent writes to the same field resolve by LWW timestamp.
func TestConcurrentFieldUpdateLWW(t *testing.T) {
	a := New("A")
	b := New("B")

	a.SetAt("user_profile", "age", 25, 100)
	b.SetAt("user_profile", "age", 30, 200)

	syncAll(a, b)

	for _, r := range []*Map{a, b} {
		if v, _ := r.GetField("user_profile", "age"); v != 30 {
			t.Fatalf("LWW violated: age=%v, want 30", v)
		}
	}
}

// Equal timestamps break ties deterministically by node ID.
func TestConcurrentFieldUpdateLWWTieBreak(t *testing.T) {
	a := New("A")
	b := New("B")

	a.SetAt("user_profile", "age", 25, 100)
	b.SetAt("user_profile", "age", 30, 100)

	syncAll(a, b)

	// Node "B" > "A", so B's value wins on both replicas.
	for _, r := range []*Map{a, b} {
		if v, _ := r.GetField("user_profile", "age"); v != 30 {
			t.Fatalf("tie-break violated: age=%v, want 30 (node B)", v)
		}
	}
}

// Concurrent field remove vs field update: update survives (add-wins).
func TestRemoveFieldVsConcurrentUpdate(t *testing.T) {
	a := New("A")
	b := New("B")

	a.SetAt("user_profile", "age", 25, 100)
	b.Merge(a)

	a.RemoveField("user_profile", "age")
	b.SetAt("user_profile", "age", 26, 200)

	syncAll(a, b)

	for _, r := range []*Map{a, b} {
		if v, ok := r.GetField("user_profile", "age"); !ok || v != 26 {
			t.Fatalf("concurrent update must survive field remove, got %v", v)
		}
	}
}

// Sub-fields can be added, changed and removed dynamically.
func TestDynamicFieldLifecycle(t *testing.T) {
	m := New("A")

	m.SetAt("user_profile", "name", "alice", 1)
	m.SetAt("user_profile", "age", 25, 2)
	m.SetAt("user_profile", "age", 26, 3) // update
	m.SetAt("user_profile", "city", "shanghai", 4)
	m.RemoveField("user_profile", "city") // delete

	got := mustGet(t, m, "user_profile")
	want := map[string]interface{}{"name": "alice", "age": 26}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Merge is idempotent and commutative.
func TestMergeIdempotentAndCommutative(t *testing.T) {
	build := func() (*Map, *Map) {
		a := New("A")
		b := New("B")
		a.SetAt("k1", "f1", "a1", 100)
		a.SetAt("k2", "f2", "a2", 101)
		b.Merge(a) // B causally observes A's writes
		b.SetAt("k1", "f1", "b1", 200)
		b.SetAt("k3", "f3", "b3", 102)
		b.RemoveKey("k2")
		return a, b
	}

	a1, b1 := build()
	a1.Merge(b1)
	b1.Merge(a1)
	a1.Merge(b1) // idempotent: merging again changes nothing

	a2, b2 := build()
	b2.Merge(a2) // opposite merge order
	a2.Merge(b2)

	for _, k := range []string{"k1", "k2", "k3"} {
		g1, ok1 := a1.Get(k)
		g2, ok2 := a2.Get(k)
		if ok1 != ok2 || !reflect.DeepEqual(g1, g2) {
			t.Fatalf("merge order changed result for %q: %v vs %v", k, g1, g2)
		}
	}
	if _, ok := a1.Get("k2"); ok {
		t.Fatalf("k2 must stay removed (remove observed the only write)")
	}
	if v, _ := a1.GetField("k1", "f1"); v != "b1" {
		t.Fatalf("k1.f1=%v, want b1 (higher ts)", v)
	}
}

// Three replicas converging through pairwise merges.
func TestThreeReplicaConvergence(t *testing.T) {
	a, b, c := New("A"), New("B"), New("C")

	a.SetAt("user_profile", "name", "alice", 100)
	syncAll(a, b, c)

	a.RemoveKey("user_profile")
	b.SetAt("user_profile", "age", 25, 200)
	c.SetAt("user_profile", "city", "shanghai", 201)

	syncAll(a, b, c)
	syncAll(a, b, c) // gossip until fixpoint

	want := map[string]interface{}{"age": 25, "city": "shanghai"}
	for _, r := range []*Map{a, b, c} {
		got := mustGet(t, r, "user_profile")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("replica did not converge: got %v, want %v", got, want)
		}
	}
}

// Concurrent goroutine access must be race-free (run with -race).
func TestConcurrentAccess(t *testing.T) {
	a := New("A")
	b := New("B")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				a.Set("user_profile", "age", j)
				a.Get("user_profile")
				a.Merge(b)
			}
		}(i)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Set("user_profile", "name", "bob")
				b.Keys()
				b.Merge(a)
			}
		}(i)
	}
	wg.Wait()

	syncAll(a, b)
	ga, _ := a.Get("user_profile")
	gb, _ := b.Get("user_profile")
	if !reflect.DeepEqual(ga, gb) {
		t.Fatalf("replicas diverged after concurrent merges: %v vs %v", ga, gb)
	}
}
