package awlwwmap

import (
	"reflect"
	"testing"
)

// newPair returns two replicas that share a common ancestor state:
// user_profile{name: "delin", age: 30} written by A and replicated to B.
func newPair() (a, b *Map) {
	a = New("A")
	a.Set("delin", "user_profile", "name")
	a.Set(30, "user_profile", "age")
	b = New("B")
	b.Merge(a)
	return a, b
}

func TestSetGetAndSnapshot(t *testing.T) {
	m := New("A")
	m.Set("delin", "user_profile", "name")
	m.Set(30, "user_profile", "age")
	m.Set("dark", "settings", "theme")

	if got, ok := m.Get("user_profile", "age"); !ok || got != 30 {
		t.Fatalf("Get(age) = %v, %v; want 30, true", got, ok)
	}
	want := map[string]interface{}{
		"user_profile": map[string]interface{}{"name": "delin", "age": 30},
		"settings":     map[string]interface{}{"theme": "dark"},
	}
	if got := m.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot() = %v; want %v", got, want)
	}
}

func TestOverwriteIsLastWriterWins(t *testing.T) {
	m := New("A")
	m.Set(30, "user_profile", "age")
	m.Set(31, "user_profile", "age")
	if got, _ := m.Get("user_profile", "age"); got != 31 {
		t.Fatalf("Get(age) = %v; want 31", got)
	}
}

func TestRemoveRootKey(t *testing.T) {
	m := New("A")
	m.Set("delin", "user_profile", "name")
	m.Remove("user_profile")
	if m.Has("user_profile") {
		t.Fatal("user_profile should be gone after Remove")
	}
	if _, ok := m.Get("user_profile", "name"); ok {
		t.Fatal("Get on removed key should fail")
	}
	if got := m.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot() = %v; want empty", got)
	}
}

func TestRemoveSubField(t *testing.T) {
	m := New("A")
	m.Set("delin", "user_profile", "name")
	m.Set(30, "user_profile", "age")
	m.Remove("user_profile", "age")
	if m.Has("user_profile", "age") {
		t.Fatal("age should be removed")
	}
	if !m.Has("user_profile", "name") {
		t.Fatal("name should survive removing the sibling field")
	}
}

func TestMergePropagatesRemove(t *testing.T) {
	a, b := newPair()
	a.Remove("user_profile")
	b.Merge(a)
	if b.Has("user_profile") {
		t.Fatal("remove should propagate to B")
	}
}

// TestConcurrentRemoveVsNestedUpdate_UpdateWins is the acceptance boundary:
// node A removes root key "user_profile" while, concurrently, node B sets
// "user_profile.age = 25". After merging, the update must win: the key
// survives with the new age and the previously replicated name.
func TestConcurrentRemoveVsNestedUpdate_UpdateWins(t *testing.T) {
	a, b := newPair()

	a.Remove("user_profile")         // A deletes the root key
	b.Set(25, "user_profile", "age") // B concurrently updates a nested field

	a.Merge(b)
	b.Merge(a)

	for name, m := range map[string]*Map{"A": a, "B": b} {
		if !m.Has("user_profile") {
			t.Fatalf("%s: user_profile should survive (update wins over concurrent remove)", name)
		}
		if got, ok := m.Get("user_profile", "age"); !ok || got != 25 {
			t.Fatalf("%s: age = %v, %v; want 25, true", name, got, ok)
		}
		if got, ok := m.Get("user_profile", "name"); !ok || got != "delin" {
			t.Fatalf("%s: name = %v, %v; want delin, true", name, got, ok)
		}
	}
	if !reflect.DeepEqual(a.Snapshot(), b.Snapshot()) {
		t.Fatalf("replicas diverged:\nA=%v\nB=%v", a.Snapshot(), b.Snapshot())
	}
}

// TestConcurrentRemoveVsUpdateTieAddWins forces the remove and the update to
// carry the same Lamport time so the add-wins tie-break decides.
func TestConcurrentRemoveVsUpdateTieAddWins(t *testing.T) {
	a, b := newPair()
	// Both clocks are at 2; the next op on each node gets time 3.
	a.Remove("user_profile")         // remove @ (3,"A")
	b.Set(25, "user_profile", "age") // add @ (3,"B") — same Lamport time

	a.Merge(b)
	if !a.Has("user_profile") {
		t.Fatal("add-wins tie-break: key should survive")
	}
	if got, _ := a.Get("user_profile", "age"); got != 25 {
		t.Fatalf("age = %v; want 25", got)
	}
}

// TestCausalRemoveBeatsEarlierUpdate: a remove that happens *after* observing
// the update must win — add-wins only applies to concurrent writes.
func TestCausalRemoveBeatsEarlierUpdate(t *testing.T) {
	a, b := newPair()
	b.Set(25, "user_profile", "age")
	a.Merge(b)               // A observes the update
	a.Remove("user_profile") // then removes causally later
	b.Merge(a)

	if b.Has("user_profile") {
		t.Fatal("causally-later remove should delete the key")
	}
}

func TestReAddAfterRemove(t *testing.T) {
	a, b := newPair()
	a.Remove("user_profile")
	b.Merge(a)                       // B observes the remove
	b.Set(25, "user_profile", "age") // then re-creates the key
	a.Merge(b)

	if !a.Has("user_profile") {
		t.Fatal("re-add after observed remove should resurrect the key")
	}
	if got, _ := a.Get("user_profile", "age"); got != 25 {
		t.Fatalf("age = %v; want 25", got)
	}
}

func TestConcurrentLeafWritesConverge(t *testing.T) {
	a, b := newPair()
	a.Set("alice", "user_profile", "name")
	b.Set("bob", "user_profile", "name")
	a.Merge(b)
	b.Merge(a)

	if !reflect.DeepEqual(a.Snapshot(), b.Snapshot()) {
		t.Fatalf("replicas diverged:\nA=%v\nB=%v", a.Snapshot(), b.Snapshot())
	}
	// Same Lamport time on both; node ID "B" > "A" decides deterministically.
	if got, _ := a.Get("user_profile", "name"); got != "bob" {
		t.Fatalf("name = %v; want bob (highest (time,node) wins)", got)
	}
}

func TestMergeIsCommutativeAndIdempotent(t *testing.T) {
	a, b := newPair()
	a.Remove("user_profile", "age")
	a.Set("blue", "settings", "theme")
	b.Set(25, "user_profile", "age")

	left := a.Clone()
	left.Merge(b)
	right := b.Clone()
	right.Merge(a)

	if !reflect.DeepEqual(left.Snapshot(), right.Snapshot()) {
		t.Fatalf("merge not commutative:\n%v\n%v", left.Snapshot(), right.Snapshot())
	}
	once := left.Snapshot()
	left.Merge(b)
	left.Merge(b.Clone())
	if !reflect.DeepEqual(once, left.Snapshot()) {
		t.Fatal("merge not idempotent")
	}
}

func TestNestedRemoveMergesWithSiblingUpdate(t *testing.T) {
	a, b := newPair()
	a.Remove("user_profile", "age")         // A deletes just the age field
	b.Set("delin2", "user_profile", "name") // B concurrently renames
	a.Merge(b)
	b.Merge(a)

	if a.Has("user_profile", "age") || b.Has("user_profile", "age") {
		t.Fatal("age should stay removed")
	}
	if got, _ := a.Get("user_profile", "name"); got != "delin2" {
		t.Fatalf("name = %v; want delin2", got)
	}
}
