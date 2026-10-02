// Command demo runs the acceptance scenario of the distributed undo tree:
// user A types "hello", user B inserts "!", A undoes "hello" (B's "!"
// survives), then A redoes and the document is back to "hello!".
package main

import (
	"fmt"

	"undocrdt"
)

func main() {
	a := undocrdt.NewDoc("A")
	b := undocrdt.NewDoc("B")
	sync := func(ops ...undocrdt.Op) {
		for _, op := range ops {
			switch op.ID.Site {
			case "A":
				b.Apply(op)
			default:
				a.Apply(op)
			}
		}
		fmt.Printf("  A sees %q | B sees %q\n", a.String(), b.String())
	}

	fmt.Println("1. A types \"hello\"")
	sync(a.LocalInsert(undocrdt.RootID, "hello"))

	fmt.Println("2. B inserts \"!\" at the end")
	sync(b.LocalInsert(b.LastVisible(), "!"))

	fmt.Println("3. A undoes \"hello\" (compensation node, B's '!' survives)")
	undo, _ := a.Undo()
	sync(undo)

	fmt.Println("4. A redoes (reverse compensation restores \"hello\")")
	redo, _ := a.Redo()
	sync(redo)

	fmt.Println("\nUndo tree on A's replica:")
	fmt.Print(a.UndoTree())
}
