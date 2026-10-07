package sched

import "fmt"

func ExampleSolve() {
	orders := []Order{
		{ID: "A", Type: 0, Duration: 3, Deadline: 5},
		{ID: "B", Type: 1, Duration: 2, Deadline: 4},
		{ID: "C", Type: 0, Duration: 4, Deadline: 12},
	}
	setup := [][]int{
		{0, 2},
		{3, 0},
	}
	fmt.Print(Solve(orders, setup, 0))
	// Output:
	// #   Order    Type Setup Start End   Lateness
	// 0   B        1    2     0     2     -2
	// 1   A        0    3     2     5     0
	// 2   C        0    0     5     9     -3
	// MaxLateness=0 TotalSetup=5
}
