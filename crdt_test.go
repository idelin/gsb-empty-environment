package crdt

import (
	"reflect"
	"testing"
)

// buildBase creates the shared starting document:
//
//	{ "list": [ { "title": "old title", "tags": [] } ] }
func buildBase() *Doc {
	d := NewDoc("base")
	list := d.Root().PutArray("list")
	item := list.PushMap()
	item.PutRegister("title", "old title")
	item.PutArray("tags")
	return d
}

// Acceptance: concurrent edits to root.list[0].title and
// root.list[0].tags.push("go") must both survive the merge, and the
// in-memory *Map for list[0] must not be replaced.
func TestConcurrentNestedEditsMergePrecisely(t *testing.T) {
	base := buildBase()
	a := base.Fork("A")
	b := base.Fork("B")

	// Capture the in-memory identity of the list item on replica A.
	listA := a.Root().Get("list").(*Array)
	itemA := listA.At(0).(*Map)

	// A edits root.list[0].title.
	itemA.Get("title").(*Register).Set("new title")

	// Concurrently, B pushes onto root.list[0].tags.
	itemB := b.Root().Get("list").(*Array).At(0).(*Map)
	itemB.Get("tags").(*Array).PushRegister("go")

	// Sync both ways.
	a.Merge(b)
	b.Merge(a)

	// 1. The list item's in-memory object reference is preserved.
	if got := listA.At(0); got != itemA {
		t.Fatalf("list[0] object reference was replaced: got %p, want %p", got, itemA)
	}

	// 2. Both concurrent edits are present on A.
	if v := itemA.Get("title").(*Register).Value(); v != "new title" {
		t.Fatalf("title = %v, want %q", v, "new title")
	}
	tags := itemA.Get("tags").(*Array)
	if tags.Len() != 1 || tags.At(0).(*Register).Value() != "go" {
		t.Fatalf("tags = %v, want [go]", tags.value())
	}

	// 3. Replicas converge to the same value.
	if !reflect.DeepEqual(a.Value(), b.Value()) {
		t.Fatalf("replicas diverged:\nA=%v\nB=%v", a.Value(), b.Value())
	}
	want := map[string]any{
		"list": []any{
			map[string]any{"title": "new title", "tags": []any{"go"}},
		},
	}
	if !reflect.DeepEqual(a.Value(), want) {
		t.Fatalf("merged value = %v, want %v", a.Value(), want)
	}
}

func TestConcurrentArrayPushesBothSurvive(t *testing.T) {
	base := NewDoc("base")
	base.Root().PutArray("items")

	a := base.Fork("A")
	b := base.Fork("B")

	a.Root().Get("items").(*Array).PushRegister("from-a")
	b.Root().Get("items").(*Array).PushRegister("from-b")

	a.Merge(b)
	b.Merge(a)

	if a.Root().Get("items").(*Array).Len() != 2 {
		t.Fatalf("len = %d, want 2", a.Root().Get("items").(*Array).Len())
	}
	if !reflect.DeepEqual(a.Value(), b.Value()) {
		t.Fatalf("diverged: A=%v B=%v", a.Value(), b.Value())
	}
	got := a.Root().Get("items").(*Array).value()
	want := []any{"from-b", "from-a"} // RGA: same prev, higher ID (B) first
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
}

func TestRegisterLWW(t *testing.T) {
	base := NewDoc("base")
	base.Root().PutRegister("name", "base")

	a := base.Fork("A")
	b := base.Fork("B")
	a.Root().Get("name").(*Register).Set("alice")
	b.Root().Get("name").(*Register).Set("bob")

	a.Merge(b)
	b.Merge(a)

	// Equal Lamport counters: replica ID breaks the tie, "B" wins.
	for _, d := range []*Doc{a, b} {
		if v := d.Root().Get("name").(*Register).Value(); v != "bob" {
			t.Fatalf("name = %v, want bob", v)
		}
	}
}

func TestMapDeleteBeatsStaleSubtreeEdit(t *testing.T) {
	base := NewDoc("base")
	base.Root().PutMap("profile").PutRegister("age", 30)

	a := base.Fork("A")
	b := base.Fork("B")

	a.Root().Delete("profile")                                     // newer write
	b.Root().Get("profile").(*Map).PutRegister("city", "shanghai") // stale key write

	a.Merge(b)
	b.Merge(a)

	for _, d := range []*Doc{a, b} {
		if d.Root().Get("profile") != nil {
			t.Fatalf("profile should stay deleted, got %v", d.Value())
		}
	}
}

func TestArrayDeletePropagates(t *testing.T) {
	base := NewDoc("base")
	arr := base.Root().PutArray("xs")
	arr.PushRegister(1)
	arr.PushRegister(2)
	arr.PushRegister(3)

	a := base.Fork("A")
	b := base.Fork("B")

	a.Root().Get("xs").(*Array).Delete(0)
	b.Root().Get("xs").(*Array).PushRegister(4)

	a.Merge(b)
	b.Merge(a)

	want := []any{2, 3, 4}
	for _, d := range []*Doc{a, b} {
		if got := d.Root().Get("xs").(*Array).value(); !reflect.DeepEqual(got, want) {
			t.Fatalf("xs = %v, want %v", got, want)
		}
	}
}

func TestDeepMixedNesting(t *testing.T) {
	d := NewDoc("A")
	user := d.Root().PutMap("user")
	user.PutRegister("name", "delin")
	addrs := user.PutArray("addresses")
	home := addrs.PushMap()
	home.PutRegister("city", "shanghai")
	home.PutArray("tags").PushRegister("home")

	b := d.Fork("B")
	b.Root().Get("user").(*Map).Get("addresses").(*Array).
		At(0).(*Map).Get("tags").(*Array).PushRegister("work")

	d.Merge(b)

	want := map[string]any{
		"user": map[string]any{
			"name": "delin",
			"addresses": []any{
				map[string]any{"city": "shanghai", "tags": []any{"home", "work"}},
			},
		},
	}
	if !reflect.DeepEqual(d.Value(), want) {
		t.Fatalf("value = %v, want %v", d.Value(), want)
	}
}

func TestMergeIdempotentAndCommutative(t *testing.T) {
	base := buildBase()
	a := base.Fork("A")
	b := base.Fork("B")

	a.Root().Get("list").(*Array).At(0).(*Map).Get("title").(*Register).Set("t2")
	b.Root().Get("list").(*Array).At(0).(*Map).Get("tags").(*Array).PushRegister("x")

	a.Merge(b)
	snap1, _ := a.ToJSON()
	a.Merge(b) // idempotent
	a.Merge(a) // self-merge is a no-op
	snap2, _ := a.ToJSON()
	if string(snap1) != string(snap2) {
		t.Fatalf("merge not idempotent:\n%s\nvs\n%s", snap1, snap2)
	}

	// Commutativity: merging in the opposite order gives the same value.
	c, d := base.Fork("C"), base.Fork("D")
	c.Merge(a)
	c.Merge(b)
	d.Merge(b)
	d.Merge(a)
	if !reflect.DeepEqual(c.Value(), d.Value()) {
		t.Fatalf("merge not commutative: %v vs %v", c.Value(), d.Value())
	}
}

func TestToJSON(t *testing.T) {
	d := buildBase()
	data, err := d.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "list": [
    {
      "tags": [],
      "title": "old title"
    }
  ]
}`
	if string(data) != want {
		t.Fatalf("json =\n%s\nwant\n%s", data, want)
	}
}
