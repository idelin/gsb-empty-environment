// Command demo runs a small work-stealing workload: one root task spawns a
// tree of subtasks, some of which are injected to fail or sleep.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"worksteal"
)

func main() {
	rng := rand.New(rand.NewSource(42))
	s := worksteal.New(4)
	s.Start()

	var spawn func(sc *worksteal.Scope, depth int)
	spawn = func(sc *worksteal.Scope, depth int) {
		if depth == 0 {
			time.Sleep(time.Duration(rng.Intn(3)) * time.Millisecond) // injected timing
			return
		}
		for i := 0; i < 3; i++ {
			d := depth - 1
			sc.Submit(func(c *worksteal.Scope) error {
				spawn(c, d)
				if rng.Intn(10) == 0 {
					return worksteal.ErrInjected // injected failure
				}
				return nil
			})
		}
	}

	start := time.Now()
	s.Submit(func(sc *worksteal.Scope) error { spawn(sc, 6); return nil })
	s.Shutdown()

	st := s.Stats()
	fmt.Printf("workers=%d elapsed=%s\n", s.Workers(), time.Since(start).Round(time.Millisecond))
	fmt.Printf("submitted=%d completed=%d failed=%d conserved=%v\n",
		st.Submitted, st.Completed, st.Failed, st.Conserved())
}
