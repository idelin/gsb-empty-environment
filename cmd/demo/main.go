// Command demo runs the acceptance scenario: 3 fibers alternately Yield,
// and prints the deterministic scheduling log twice to show that the
// tick sequence is bit-for-bit identical across runs.
package main

import (
	"fmt"

	"fiber"
)

func scenario() []fiber.Event {
	s := &fiber.Scheduler{} // 1 virtual microsecond per time slice
	work := func(f *fiber.Fiber) {
		for i := 0; i < 3; i++ {
			f.Yield()
		}
	}
	s.Spawn("A", work)
	s.Spawn("B", work)
	s.Spawn("C", work)
	s.Run()
	return s.Log()
}

func main() {
	log1 := scenario()
	log2 := scenario()

	fmt.Println("scheduling log (run #1):")
	for _, e := range log1 {
		fmt.Println(" ", e)
	}

	same := len(log1) == len(log2)
	for i := range log1 {
		if log1[i] != log2[i] {
			same = false
		}
	}
	fmt.Printf("\nrun #2 identical to run #1 (tick-by-tick): %v\n", same)
}
