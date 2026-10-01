// Demo of the acceptance scenario: two replicas fork a shared document,
// concurrently edit root.list[0].title and root.list[0].tags, then merge.
package main

import (
	"fmt"

	"jsoncrdt"
)

func mustJSON(d *crdt.Doc) string {
	b, err := d.ToJSON()
	if err != nil {
		panic(err)
	}
	return string(b)
}

func main() {
	base := crdt.NewDoc("base")
	item := base.Root().PutArray("list").PushMap()
	item.PutRegister("title", "old title")
	item.PutArray("tags")
	fmt.Printf("base document:\n%s\n\n", mustJSON(base))

	a := base.Fork("A")
	b := base.Fork("B")

	// Replica A: root.list[0].title = "new title"
	listA := a.Root().Get("list").(*crdt.Array)
	itemA := listA.At(0).(*crdt.Map)
	itemA.Get("title").(*crdt.Register).Set("new title")

	// Replica B (concurrently): root.list[0].tags.push("go")
	itemB := b.Root().Get("list").(*crdt.Array).At(0).(*crdt.Map)
	itemB.Get("tags").(*crdt.Array).PushRegister("go")

	fmt.Printf("A before merge: %s\n", mustJSON(a))
	fmt.Printf("B before merge: %s\n\n", mustJSON(b))

	a.Merge(b)
	b.Merge(a)

	fmt.Printf("A after merge:  %s\n", mustJSON(a))
	fmt.Printf("B after merge:  %s\n\n", mustJSON(b))

	sameObj := listA.At(0) == itemA
	fmt.Printf("list[0] object reference preserved on A: %v\n", sameObj)
	fmt.Printf("replicas converged: %v\n", mustJSON(a) == mustJSON(b))
}
