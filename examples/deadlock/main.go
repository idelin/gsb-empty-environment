// Command deadlock demonstrates the reactive loop-deadlock guard: an
// Effect reads Signal A and writes Signal A, and the guard blocks the
// resulting infinite reactive loop at the 50th iteration, printing the
// circular causal-chain report.
package main

import (
	"fmt"

	"reactiveguard"
)

func main() {
	guard := reactiveguard.NewGuard(50)
	a := reactiveguard.NewSignal(guard, "A", 0)

	err, caught := reactiveguard.CatchCircular(func() {
		guard.Effect("self-increment", func() {
			v := a.Get().(int) // read dependency A
			fmt.Printf("effect run: A = %d\n", v)
			a.Set(v + 1) // malicious: write own dependency
		})
	})

	if caught {
		fmt.Println("---- guard intercepted the deadlock ----")
		fmt.Print(err.Error())
	} else {
		fmt.Println("no loop detected (unexpected)")
	}
}
