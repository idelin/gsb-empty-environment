// Command demo builds and prints the logical plan for a sample query,
// before and after optimization.
package main

import (
	"fmt"

	"logplan"
)

const query = "SELECT u.name FROM users u JOIN orders o ON u.id = o.uid WHERE u.age > 18"

func main() {
	fmt.Println("SQL:")
	fmt.Println(" ", query)

	raw, err := logplan.Build(query)
	if err != nil {
		panic(err)
	}
	fmt.Println("\nLogical plan (unoptimized):")
	fmt.Print(logplan.String(raw))

	opt, err := logplan.BuildOptimized(query)
	if err != nil {
		panic(err)
	}
	fmt.Println("Logical plan (optimized: predicate pushdown + projection elimination):")
	fmt.Print(logplan.String(opt))
}
