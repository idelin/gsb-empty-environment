package dagexec

import "fmt"

// A fails, B is cancelled: C (all-success) and D (any-success) are skipped
// immediately, E (all-terminal) still launches, and the reason chain
// records the failed and cancelled parents separately.
func ExampleScheduler() {
	s := New()
	s.Add("A", AllSuccess)
	s.Add("B", AllSuccess)
	s.Add("C", AllSuccess, "A", "B")
	s.Add("D", AnySuccess, "A", "B")
	s.Add("E", AllTerminal, "C", "D")

	launched, _ := s.Start()
	fmt.Println("launched:", launched)

	launched, _ = s.Complete("A", Failed)
	fmt.Println("launched:", launched, "| C:", s.State("C"))

	launched, _ = s.Complete("B", Cancelled)
	fmt.Println("launched:", launched, "| D:", s.State("D"))

	fmt.Print(s.Explain("E"))

	// Output:
	// launched: [A B]
	// launched: [] | C: skipped
	// launched: [E] | D: skipped
	// E: running (launch [all-terminal]: C=skipped, D=skipped)
	//   C: skipped (skip [all-success]: A=failed, B=cancelled)
	//     A: failed
	//     B: cancelled
	//   D: skipped (skip [any-success]: A=failed, B=cancelled)
	//     A: failed
	//     B: cancelled
}
