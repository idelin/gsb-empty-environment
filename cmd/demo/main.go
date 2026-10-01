// Command demo runs the constant-folding engine over sample WHERE clauses.
package main

import (
	"fmt"

	"constfold"
)

func main() {
	samples := []string{
		"1 + 2 * 3",
		"age > 10 + 5",
		"u.created_at > NOW() - INTERVAL 1 DAY AND 1 = 1 AND u.score * 0 = 0",
		"price * (1 + 1) > 100 AND NOT FALSE",
		"1 + 2 * 3 = 7 AND NOT (2 > 5)",
	}
	for _, s := range samples {
		out, err := constfold.FoldString(s)
		if err != nil {
			fmt.Printf("input : %s\nerror : %v\n\n", s, err)
			continue
		}
		fmt.Printf("input : %s\nfolded: %s\n\n", s, out)
	}
}
