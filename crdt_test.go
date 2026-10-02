package crdt

import (
	"reflect"
	"testing"
)

// buildSharedList creates the common ancestor state on a and syncs it to b:
//
//	root.list = [ { title: "old", tags: ["json"] } ]
func buildSharedList(a, b *Doc) {
	a.Root().Set("list", []any{
		map[string]any{
			"title": "old",
			"tags":  []any{"json"},
		},
	})
	b.Merge(a)
}

func listItem(t *testing.T, d *Doc) Ref {
	t.Helper()
	list := mustGet(t, d.Root(), "list")
	if list.Len() != 1 {
		t.Fatalf("root.list len = %d, want 1", list.Len())
	}
	return list.Index(0)
}

func mustGet(t *testing.T, r Ref, key string) Ref {
	t.Helper()
	child, ok := r.Get(key)
	if !ok {
		t.Fatalf("key %q missing on node %s", key, r.ID())
	}
	return child
}

// Acceptance boundary: replica A edits root.list[0].title while replica B
// concurrently runs root.list[0].tags.push("go"). After merging, both
// updates must be present and the list item's in-memory node must not have
// been replaced.
func TestConcurrentListItemFields(t *testing.T) {
	a, b := New("A"), New("B")
	buildSharedList(a, b)

	itemA := listItem(t, a)
	itemB := listItem(t, b)
	if itemA.ID() != itemB.ID() {
		t.Fatalf("item ids diverge before edits: %s vs %s", itemA.ID(), itemB.ID())
	}
	nodeBefore := a.nodes[itemA.ID()] // capture in-memory object reference

	// --- concurrent edits, no sync in between ---
	titleA, ok := itemA.Get("title")
	if !ok {
		t.Fatal("title missing on A")
	}
	titleA.SetValue("A 标题") // A: root.list[0].title = "A 标题"

	tagsB, ok := itemB.Get("tags")
	if !ok {
		t.Fatal("tags missing on B")
	}
	tagsB.Push("go") // B: root.list[0].tags.push("go")

	// --- merge both ways ---
	a.Merge(b)
	b.Merge(a)

	// 1. convergence
	if !reflect.DeepEqual(a.ToJSON(), b.ToJSON()) {
		t.Fatalf("replicas diverged:\nA=%v\nB=%v", a.ToJSON(), b.ToJSON())
	}

	// 2. both concurrent updates survived
	want := map[string]any{
		"list": []any{
			map[string]any{"title": "A 标题", "tags": []any{"json", "go"}},
		},
	}
	if !reflect.DeepEqual(a.ToJSON(), want) {
		t.Fatalf("merged state = %v, want %v", a.ToJSON(), want)
	}

	// 3. the list item node was merged in place, not overwritten
	if got := a.nodes[itemA.ID()]; got != nodeBefore {
		t.Fatal("list item node object was replaced during merge")
	}
	itemAfter := listItem(t, a)
	if itemAfter.ID() != itemA.ID() {
		t.Fatalf("list item id changed: %s -> %s", itemA.ID(), itemAfter.ID())
	}
	title, _ := itemA.Get("title") // resolve through the pre-merge handle
	if title.Value() != "A 标题" {
		t.Fatalf("title via stale handle = %v", title.Value())
	}
	tags, _ := itemA.Get("tags")
	if tags.Len() != 2 || tags.Index(1).Value() != "go" {
		t.Fatalf("tags via stale handle = %v", tags.ToJSON())
	}
}

// Three-way merge: B and C both fork from A's state, diverge, and all
// three replicas converge with every edit preserved.
func TestThreeWayMerge(t *testing.T) {
	a, b, c := New("A"), New("B"), New("C")
	buildSharedList(a, b)
	c.Merge(a)

	// B edits a leaf deep in the tree.
	titleB, _ := listItem(t, b).Get("title")
	titleB.SetValue("B 标题")

	// C edits a sibling field of the same list item and a root-level field.
	tagsC, _ := listItem(t, c).Get("tags")
	tagsC.Push("go")
	c.Root().Set("meta", map[string]any{"version": 2})

	// Merge in different orders; everyone must converge.
	a.Merge(b)
	a.Merge(c)
	b.Merge(c)
	b.Merge(a)
	c.Merge(a)
	c.Merge(b)

	want := map[string]any{
		"list": []any{
			map[string]any{"title": "B 标题", "tags": []any{"json", "go"}},
		},
		"meta": map[string]any{"version": 2},
	}
	for name, d := range map[string]*Doc{"A": a, "B": b, "C": c} {
		if !reflect.DeepEqual(d.ToJSON(), want) {
			t.Fatalf("replica %s = %v, want %v", name, d.ToJSON(), want)
		}
	}
}

// Concurrent pushes to the same array from two replicas both survive,
// in a deterministic order on every replica.
func TestConcurrentArrayPush(t *testing.T) {
	a, b := New("A"), New("B")
	a.Root().Set("items", []any{"base"})
	b.Merge(a)

	itemsA, _ := a.Root().Get("items")
	itemsB, _ := b.Root().Get("items")
	itemsA.Push("from-a")
	itemsB.Push("from-b")

	a.Merge(b)
	b.Merge(a)

	if !reflect.DeepEqual(a.ToJSON(), b.ToJSON()) {
		t.Fatalf("diverged: A=%v B=%v", a.ToJSON(), b.ToJSON())
	}
	got := itemsA.ToJSON().([]any)
	if len(got) != 3 || got[0] != "base" {
		t.Fatalf("items = %v", got)
	}
	set := map[any]bool{got[1]: true, got[2]: true}
	if !set["from-a"] || !set["from-b"] {
		t.Fatalf("concurrent pushes lost: %v", got)
	}
}

// Concurrent writes to the same register resolve deterministically (LWW)
// and identically on all replicas.
func TestRegisterLWW(t *testing.T) {
	a, b := New("A"), New("B")
	a.Root().Set("name", "init")
	b.Merge(a)

	nameA, _ := a.Root().Get("name")
	nameB, _ := b.Root().Get("name")
	nameA.SetValue("from-a")
	nameB.SetValue("from-b")

	a.Merge(b)
	b.Merge(a)

	if nameA.Value() != nameB.Value() {
		t.Fatalf("register diverged: A=%v B=%v", nameA.Value(), nameB.Value())
	}
	// B's write has the higher lamport clock and must win on both replicas.
	if nameA.Value() != "from-b" {
		t.Fatalf("register = %v, want from-b", nameA.Value())
	}
}

// Concurrent Map.Set of the same key picks one winner deterministically.
func TestMapKeyLWW(t *testing.T) {
	a, b := New("A"), New("B")
	a.Root().Set("k", "init")
	b.Merge(a)

	a.Root().Set("k", "a-value")
	b.Root().Set("k", "b-value")

	a.Merge(b)
	b.Merge(a)

	if !reflect.DeepEqual(a.ToJSON(), b.ToJSON()) {
		t.Fatalf("diverged: A=%v B=%v", a.ToJSON(), b.ToJSON())
	}
	got := a.ToJSON().(map[string]any)["k"]
	if got != "a-value" && got != "b-value" {
		t.Fatalf("k = %v, want one of the concurrent writes", got)
	}
}

// Deeply nested mixed Map/Array/Register structure with concurrent edits
// at several levels of the tree.
func TestDeepNestedConcurrentEdits(t *testing.T) {
	a, b := New("A"), New("B")
	a.Root().Set("users", []any{
		map[string]any{
			"name": "alice",
			"profile": map[string]any{
				"settings": map[string]any{"theme": "dark", "fontSize": 14},
				"history":  []any{"login"},
			},
		},
	})
	b.Merge(a)

	usersA := mustGet(t, a.Root(), "users")
	profileA := usersA.Index(0).Map("profile")
	settingsA := profileA.Map("settings")
	themeA, _ := settingsA.Get("theme")
	themeA.SetValue("light")

	usersB := mustGet(t, b.Root(), "users")
	profileB := usersB.Index(0).Map("profile")
	profileB.Map("settings").Set("fontSize", 16)
	mustGet(t, profileB, "history").Push("logout")

	a.Merge(b)
	b.Merge(a)

	want := map[string]any{
		"users": []any{
			map[string]any{
				"name": "alice",
				"profile": map[string]any{
					"settings": map[string]any{"theme": "light", "fontSize": 16},
					"history":  []any{"login", "logout"},
				},
			},
		},
	}
	if !reflect.DeepEqual(a.ToJSON(), want) {
		t.Fatalf("A = %v, want %v", a.ToJSON(), want)
	}
	if !reflect.DeepEqual(a.ToJSON(), b.ToJSON()) {
		t.Fatalf("diverged: A=%v B=%v", a.ToJSON(), b.ToJSON())
	}
}

// Merging the same ops repeatedly must be a no-op (idempotence).
func TestMergeIdempotent(t *testing.T) {
	a, b := New("A"), New("B")
	buildSharedList(a, b)

	tagsB, _ := listItem(t, b).Get("tags")
	tagsB.Push("go")

	a.Merge(b)
	snapshot := a.ToJSON()
	for i := 0; i < 3; i++ {
		a.Merge(b)
	}
	if !reflect.DeepEqual(a.ToJSON(), snapshot) {
		t.Fatalf("repeated merge changed state: %v", a.ToJSON())
	}
	tagsA, _ := listItem(t, a).Get("tags")
	if tagsA.Len() != 2 {
		t.Fatalf("tags duplicated by re-merge: %v", tagsA.ToJSON())
	}
}

// Ops applied in arbitrary (here: reversed) order still converge thanks to
// causal buffering of unresolved dependencies.
func TestOutOfOrderOpDelivery(t *testing.T) {
	a := New("A")
	buildSharedList(a, New("B")) // just to populate a's log via edits
	a.Root().Set("extra", map[string]any{"deep": []any{1, 2, 3}})

	ops := a.Ops()
	c := New("C")
	for i := len(ops) - 1; i >= 0; i-- { // deliver in reverse
		c.Apply([]Op{ops[i]})
	}
	if !reflect.DeepEqual(a.ToJSON(), c.ToJSON()) {
		t.Fatalf("out-of-order delivery diverged:\nA=%v\nC=%v", a.ToJSON(), c.ToJSON())
	}
}

// A replica that only ever receives ops transitively (A -> B -> C) still
// converges, because merged ops remain in the log for gossip.
func TestTransitiveSync(t *testing.T) {
	a, b, c := New("A"), New("B"), New("C")
	a.Root().Set("x", 1)
	b.Merge(a)
	b.Root().Set("y", 2)
	c.Merge(b) // c never talks to a directly
	c.Root().Set("z", 3)
	a.Merge(c)
	b.Merge(a)

	want := map[string]any{"x": 1, "y": 2, "z": 3}
	for name, d := range map[string]*Doc{"A": a, "B": b, "C": c} {
		if !reflect.DeepEqual(d.ToJSON(), want) {
			t.Fatalf("replica %s = %v, want %v", name, d.ToJSON(), want)
		}
	}
}
