// Command demo shows a full round trip of a graph with shared references
// and a self-loop, plus rejection of a malformed document.
package main

import (
	"fmt"

	"graphser"
)

func main() {
	shared := graphser.NewObject()
	shared.Fields["name"] = "shared"
	shared.Fields["n"] = 1.5

	loop := graphser.NewObject()
	loop.Fields["self"] = loop
	loop.Fields["peer"] = shared

	root := []any{shared, shared, loop, "text", 3.25, nil, true}

	data, err := graphser.Marshal(root)
	if err != nil {
		panic(err)
	}
	fmt.Println("serialized:")
	fmt.Println(" ", string(data))

	back, err := graphser.Unmarshal(data)
	if err != nil {
		panic(err)
	}
	list := back.([]any)
	gotShared0 := list[0].(*graphser.Object)
	gotShared1 := list[1].(*graphser.Object)
	gotLoop := list[2].(*graphser.Object)

	fmt.Println("after decode:")
	fmt.Println("  root[0] and root[1] same identity:", gotShared0 == gotShared1)
	fmt.Println("  self-loop preserved:              ", gotLoop.Fields["self"].(*graphser.Object) == gotLoop)
	fmt.Println("  loop.peer is the shared object:   ", gotLoop.Fields["peer"].(*graphser.Object) == gotShared0)
	fmt.Println("  value-equal to original graph:    ", graphser.ValueEqual(root, back))

	bad := []byte(`{"version":1,"count":0,"objects":[],"root":{"ref":7}}`)
	_, err = graphser.Unmarshal(bad)
	fmt.Println("dangling ref rejected:", err)

	dup := []byte(`{"version":1,"count":2,"objects":[{"id":1,"fields":{}},{"id":1,"fields":{}}],"root":{"ref":1}}`)
	_, err = graphser.Unmarshal(dup)
	fmt.Println("duplicate id rejected:", err)
}
