// Command graphdemo demonstrates graphser: shared references, self-cycles,
// stable object IDs, and rejection of malformed documents.
package main

import (
	"fmt"
	"reflect"

	"graphser"
)

type Node struct {
	Name string
	Next *Node
}

type Graph struct {
	First  *Node
	Second *Node
	Loop   *Node
	Index  map[string]*Node
}

func main() {
	shared := &Node{Name: "shared"}
	loop := &Node{Name: "loop"}
	loop.Next = loop // self-cycle

	in := &Graph{
		First:  shared,
		Second: shared, // same object as First
		Loop:   loop,
		Index:  map[string]*Node{"again": shared},
	}

	data, err := graphser.Marshal(in)
	must(err)
	fmt.Println("=== wire format (note: object 1 appears once, referenced 3x) ===")
	fmt.Print(string(data))

	var out *Graph
	must(graphser.Unmarshal(data, &out))

	fmt.Println("=== identity after decode ===")
	fmt.Println("First == Second:        ", out.First == out.Second)
	fmt.Println("First == Index[again]:  ", out.First == out.Index["again"])
	fmt.Println("Loop.Next == Loop:      ", out.Loop.Next == out.Loop)
	fmt.Println("First != Loop:          ", out.First != out.Loop)

	equal := &Node{Name: "shared"}
	pair, err := graphser.Marshal(&Graph{First: shared, Second: equal})
	must(err)
	var pout *Graph
	must(graphser.Unmarshal(pair, &pout))
	fmt.Println("=== value equality vs identity ===")
	fmt.Println("DeepEqual(*First, *Second):", reflect.DeepEqual(*pout.First, *pout.Second))
	fmt.Println("First == Second:            ", pout.First == pout.Second)

	fmt.Println("=== malformed documents are rejected ===")
	bad := []string{
		`{"version":1,"root":{"kind":"ref","ref":9},"objects":[]}`,
		`{"version":1,"root":{"kind":"ref","ref":1},"objects":[` +
			`{"id":1,"type":"main.Node","value":{"kind":"struct","type":"main.Node"}},` +
			`{"id":1,"type":"main.Node","value":{"kind":"struct","type":"main.Node"}}]}`,
	}
	for _, doc := range bad {
		var v *Graph
		fmt.Println("rejected:", graphser.Unmarshal([]byte(doc), &v))
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
