// Command sched solves a sample single-machine scheduling problem and
// prints the optimal timetable.
package main

import (
	"fmt"
	"log"

	"example.com/jobshop/sched"
)

func main() {
	p := sched.Problem{
		// Types: 0 = standard, 1 = express, 2 = fragile.
		Orders: []sched.Order{
			{ID: "A", Type: 0, Duration: 4, Deadline: 12},
			{ID: "B", Type: 1, Duration: 2, Deadline: 6},
			{ID: "C", Type: 2, Duration: 3, Deadline: 15},
			{ID: "D", Type: 1, Duration: 5, Deadline: 20},
			{ID: "E", Type: 0, Duration: 3, Deadline: 9},
			{ID: "F", Type: 2, Duration: 2, Deadline: 11},
		},
		// Asymmetric changeover times between types.
		Setup: [][]int{
			{0, 2, 5},
			{3, 0, 4},
			{1, 6, 0},
		},
		InitialType: 0,
	}
	sol, err := sched.Solve(p)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(sol.String())
}
