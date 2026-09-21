package worksteal_test

import (
	"fmt"

	"worksteal"
)

// Example schedules a root task that spawns subtasks onto its worker's local
// deque; idle peers steal them. Shutdown drains everything and the counters
// conserve.
func Example() {
	s := worksteal.New(4)
	s.Start()

	s.Submit(func(sc *worksteal.Scope) error {
		for i := 0; i < 10; i++ {
			sc.Submit(func(*worksteal.Scope) error { return nil })
		}
		return nil
	})
	s.Shutdown()

	st := s.Stats()
	fmt.Println(st.Submitted, st.Completed, st.Failed, st.Conserved())
	// Output: 11 11 0 true
}
