// Demo of the revocable session-window merger acceptance scenario.
package main

import (
	"fmt"

	"sessionwin"
)

func printChanges(step string, changes []sessionwin.Change) {
	fmt.Printf("%s\n", step)
	if len(changes) == 0 {
		fmt.Println("  (no changes)")
	}
	for _, c := range changes {
		fmt.Printf("  %-7s [%d, %d]\n", c.Kind, c.Window.Start, c.Window.End)
	}
}

func main() {
	m := sessionwin.NewMerger(5, 10)

	printChanges("Add t=0:", m.Add(0))
	printChanges("Add t=9:", m.Add(9))
	fmt.Printf("active windows: %v\n", m.Windows())

	printChanges("Late event t=4 arrives (bridges both windows):", m.Add(4))
	fmt.Printf("active windows: %v\n", m.Windows())

	printChanges("Retract t=4 (window splits again):", m.Remove(4))
	fmt.Printf("active windows: %v\n", m.Windows())

	printChanges("Advance watermark to 20 (maxLateness=10):", m.AdvanceWatermark(20))
	fmt.Printf("active windows: %v\n", m.Windows())

	printChanges("Add t=5 (too late, dropped):", m.Add(5))
	fmt.Printf("active windows: %v\n", m.Windows())
}
