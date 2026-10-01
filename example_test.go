package crdt_test

import (
	"fmt"

	"jsoncrdt"
)

// Two replicas concurrently edit different levels of the same list item,
// then merge. Both edits survive.
func Example() {
	base := crdt.NewDoc("base")
	item := base.Root().PutArray("list").PushMap()
	item.PutRegister("title", "old title")
	item.PutArray("tags")

	a := base.Fork("A")
	b := base.Fork("B")

	a.Root().Get("list").(*crdt.Array).At(0).(*crdt.Map).
		Get("title").(*crdt.Register).Set("new title")
	b.Root().Get("list").(*crdt.Array).At(0).(*crdt.Map).
		Get("tags").(*crdt.Array).PushRegister("go")

	a.Merge(b)
	b.Merge(a)

	out, _ := a.ToJSON()
	fmt.Println(string(out))
	// Output:
	// {
	//   "list": [
	//     {
	//       "tags": [
	//         "go"
	//       ],
	//       "title": "new title"
	//     }
	//   ]
	// }
}
