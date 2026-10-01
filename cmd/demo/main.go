// Demo of the acceptance scenario: two collaborators, a per-user undo that
// preserves the peer's input, and a redo that restores the document.
package main

import (
	"fmt"

	"undotree"
)

func show(step string, docs ...*undotree.Doc) {
	fmt.Printf("%-34s", step)
	for i, d := range docs {
		fmt.Printf(" replica%c=%q", 'A'+i, d.Text())
	}
	fmt.Println()
}

func main() {
	a := undotree.NewDoc("A")
	b := undotree.NewDoc("B")

	show("init", a, b)

	hello := a.Insert(undotree.CharID{}, "hello")
	b.Apply(hello)
	show(`A inserts "hello"`, a, b)

	bang := b.Insert(undotree.LastChar(hello), "!")
	a.Apply(bang)
	show(`B inserts "!"`, a, b)

	undo, _ := a.Undo()
	b.Apply(undo)
	show("A undoes (compensation node)", a, b)

	redo, _ := a.Redo()
	b.Apply(redo)
	show("A redoes (compensation node)", a, b)

	fmt.Println("\noperation graph on replica A (append-only, causal order):")
	for _, op := range a.Ops() {
		fmt.Println("  " + op.String())
	}
}
