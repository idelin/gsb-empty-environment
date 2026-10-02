// Command demo parses a deeply nested expression, pretty-prints it within
// an 80-column limit, re-parses the printed text and verifies that the
// resulting AST is identical to the original.
package main

import (
	"fmt"
	"os"
	"reflect"

	"example.com/pretty/pretty"
)

const src = `compute(alpha + beta * gamma - delta / (epsilon + zeta), ` +
	`transform(x1, x2, x3) * (foo(bar(1, 2), baz(3)) + quux) % 17, ` +
	`!(flag1 && flag2) || (score >= threshold && level <= max_level) == done, ` +
	`-"negated" + wrap(nested(deep(deeper(deepest(a, b), c), d), e), f) * 3.14)`

func main() {
	ast, err := pretty.Parse(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse error:", err)
		os.Exit(1)
	}

	out := pretty.Print(ast)
	fmt.Println("=== pretty-printed (80-column limit) ===")
	fmt.Println(out)

	back, err := pretty.Parse(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "re-parse error:", err)
		os.Exit(1)
	}
	fmt.Println("\n=== round-trip check ===")
	if reflect.DeepEqual(ast, back) {
		fmt.Println("OK: re-parsed AST is structurally identical to the original")
	} else {
		fmt.Println("FAIL: AST mismatch")
		os.Exit(1)
	}
}
