// Command demo walks through the acceptance scenario of the timetravel
// controller: three user modifications, two undos, verification of the
// restored state, and one redo — printing every step.
package main

import (
	"fmt"

	"timetravel"
)

func main() {
	ctrl := timetravel.NewController()

	// Two independent signals, as if owned by different components.
	count := timetravel.NewSignal(ctrl, 0)
	name := timetravel.NewSignal(ctrl, "init")

	// Derived values, recomputed automatically on every version change.
	doubled := timetravel.NewComputed(ctrl, func() int { return count.Get() * 2 })
	label := timetravel.NewComputed(ctrl, func() string {
		return fmt.Sprintf("%s:%d", name.Get(), count.Get())
	})

	// Downstream "UI": re-rendered on every committed version,
	// including versions reached by time travel.
	ctrl.OnChange(func(v int) {
		fmt.Printf("  [render] version=%d  count=%d name=%q doubled=%d label=%q\n",
			v, count.Get(), name.Get(), doubled.Get(), label.Get())
	})

	show := func(step string) {
		fmt.Printf("%-22s -> version=%d count=%d name=%q doubled=%d label=%q\n",
			step, ctrl.Version(), count.Get(), name.Get(), doubled.Get(), label.Get())
	}

	fmt.Println("== 3 consecutive user modifications ==")
	count.Set(1)
	show("modify #1: count=1")
	name.Set("alice")
	show("modify #2: name=alice")
	count.Set(2)
	show("modify #3: count=2")

	fmt.Println("\n== Undo x2 (back to modification #1) ==")
	ctrl.Undo()
	show("after undo #1")
	ctrl.Undo()
	show("after undo #2")

	fmt.Println("\n== verify: state matches modification #1 exactly ==")
	ok := count.Get() == 1 && name.Get() == "init" &&
		doubled.Get() == 2 && label.Get() == "init:1"
	fmt.Printf("count==1 && name==\"init\" && doubled==2 && label==\"init:1\"  =>  %v\n", ok)

	fmt.Println("\n== Redo x1 (forward to modification #2) ==")
	ctrl.Redo()
	show("after redo #1")

	ok = count.Get() == 1 && name.Get() == "alice" &&
		doubled.Get() == 2 && label.Get() == "alice:1"
	fmt.Printf("no drift: count==1 && name==\"alice\" && doubled==2 && label==\"alice:1\"  =>  %v\n", ok)

	fmt.Printf("\nhistory: %d versions, current=v%d, CanUndo=%v CanRedo=%v\n",
		ctrl.Len(), ctrl.Version(), ctrl.CanUndo(), ctrl.CanRedo())
}
