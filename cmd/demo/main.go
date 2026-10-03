// Command demo shows the constant-folding engine in action.
package main

import (
	"fmt"

	"fold/fold"
)

func main() {
	queries := []string{
		"1 + 2 * 3",
		"age > 10 + 5",
		"price * 1 + 0",
		"u.created_at > NOW() - INTERVAL 1 DAY AND 1 = 1 AND u.score * 0 = 0",
	}
	for _, q := range queries {
		e, err := fold.Parse(q)
		if err != nil {
			fmt.Printf("parse error for %q: %v\n", q, err)
			continue
		}
		fmt.Printf("input : WHERE %s\n", q)
		fmt.Printf("output: WHERE %s\n\n", fold.String(fold.Simplify(e)))
	}
}
