// Demo: reactive signals + computed + time-travel controller.
package main

import (
	"fmt"

	"timetravel"
)

func main() {
	ctl := timetravel.NewController()

	count := timetravel.NewSignal(ctl, 0)
	name := timetravel.NewSignal(ctl, "init")
	double := timetravel.NewComputed(ctl, func() int { return count.Get() * 2 })

	// Downstream "UI" re-renders on every state transition.
	ctl.Subscribe(func() {
		fmt.Printf("  [render] count=%d name=%q double=%d (snapshot #%d)\n",
			count.Get(), name.Get(), double.Get(), ctl.Current().ID)
	})

	fmt.Println("== 3 user mutations ==")
	count.Set(1)
	name.Set("alice")
	count.Set(3)

	fmt.Println("== Undo(2) -> back to step 1 ==")
	ctl.Undo(2)

	fmt.Println("== Redo(1) -> forward to step 2 ==")
	ctl.Redo(1)

	fmt.Println("== Undo to root, then Redo to tip ==")
	ctl.Undo(2)
	ctl.Redo(3)
}
