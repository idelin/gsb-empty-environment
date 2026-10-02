// Command demo runs the acceptance scenario: 3 fibers alternately yielding,
// printing the deterministic scheduler tick log.
package main

import (
	"fmt"

	"fibersched/fiber"
)

func main() {
	s := fiber.New()
	for _, name := range []string{"A", "B", "C"} {
		s.Spawn(name, func(f *fiber.Fiber) {
			for i := 0; i < 3; i++ {
				f.Yield()
			}
		})
	}
	s.Run()
	fmt.Print(s.LogString())
	fmt.Printf("total ticks: %d\n", s.Tick())
}
