package crdt_test

import (
	"fmt"

	"crdt"
)

// Two replicas concurrently edit different fields of the same list item,
// then merge. Both edits survive and all replicas converge.
func Example() {
	alice, bob := crdt.New("alice"), crdt.New("bob")

	// Shared starting state, synced to both replicas.
	alice.Root().Set("list", []any{
		map[string]any{"title": "old", "tags": []any{"json"}},
	})
	bob.Merge(alice)

	// Concurrent edits without syncing in between.
	itemAlice, _ := alice.Root().Get("list")
	title, _ := itemAlice.Index(0).Get("title")
	title.SetValue("协同编辑") // alice: root.list[0].title = "协同编辑"

	itemBob, _ := bob.Root().Get("list")
	tags, _ := itemBob.Index(0).Get("tags")
	tags.Push("go") // bob: root.list[0].tags.push("go")

	// Merge both ways: replicas converge, both edits preserved.
	alice.Merge(bob)
	bob.Merge(alice)

	fmt.Println("alice:", alice.Root())
	fmt.Println("bob:  ", bob.Root())

	// Output:
	// alice: {"list":[{"tags":["json","go"],"title":"协同编辑"}]}
	// bob:   {"list":[{"tags":["json","go"],"title":"协同编辑"}]}
}
