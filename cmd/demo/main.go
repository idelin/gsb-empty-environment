// Command demo simulates two collaborators editing a shared JSON document
// concurrently and merging their replicas.
package main

import (
	"fmt"

	"crdt"
)

func main() {
	alice, bob := crdt.New("alice"), crdt.New("bob")

	// 1. Alice drafts a document; Bob syncs it.
	alice.Root().Set("list", []any{
		map[string]any{"title": "old", "tags": []any{"json"}},
	})
	bob.Merge(alice)
	fmt.Println("共同祖先状态:")
	fmt.Println("  alice:", alice.Root())
	fmt.Println("  bob:  ", bob.Root())

	// 2. Offline concurrent edits on the SAME list item, different fields.
	itemA, _ := alice.Root().Get("list")
	title, _ := itemA.Index(0).Get("title")
	title.SetValue("协同编辑") // alice edits root.list[0].title

	itemB, _ := bob.Root().Get("list")
	tags, _ := itemB.Index(0).Get("tags")
	tags.Push("go") // bob runs root.list[0].tags.push("go")

	// 3. Merge. The list item node is updated in place; neither edit is lost.
	alice.Merge(bob)
	bob.Merge(alice)

	fmt.Println("\n合并后 (三向合并: 共同祖先 + 两个并发分支):")
	fmt.Println("  alice:", alice.Root())
	fmt.Println("  bob:  ", bob.Root())

	item, _ := alice.Root().Get("list")
	fmt.Println("\nlist[0] 节点 id 合并前后保持稳定:", item.Index(0).ID())
}
