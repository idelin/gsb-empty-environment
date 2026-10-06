package main

import (
	"fmt"
	"os"
)

const usage = `usage: cubediff 'EXPR_A' 'EXPR_B'

Compares two boolean switch-config expressions (up to 10 variables) and
prints all minimal condition cubes under which they differ. Every
assignment covered by a printed cube makes the two expressions differ,
and no literal can be removed from a cube without covering an assignment
where they agree. The result is verified against the full truth table.

operators: ! ~ not   & && and   | || or   ^ xor   ( )   constants: 0 1 true false
example:   cubediff 'a & (b | c)' 'a & b | c'
`

func run(args []string) (string, error) {
	if len(args) != 2 {
		return "", fmt.Errorf("expected exactly 2 expressions\n%s", usage)
	}
	ea, err := ParseExpr(args[0])
	if err != nil {
		return "", fmt.Errorf("expression A: %v", err)
	}
	eb, err := ParseExpr(args[1])
	if err != nil {
		return "", fmt.Errorf("expression B: %v", err)
	}
	vars := SortedVars(ea, eb)
	if len(vars) > 10 {
		return "", fmt.Errorf("too many variables: %d (max 10)", len(vars))
	}
	n := uint(len(vars))
	diff := DiffTable(ea, eb, vars)
	cubes := PrimeImplicants(diff, n)
	if err := VerifyCubes(cubes, diff, n); err != nil {
		return "", fmt.Errorf("internal verification failed: %v", err)
	}

	out := fmt.Sprintf("variables (%d): %v\n", n, vars)
	diffCount := 0
	for _, d := range diff {
		if d {
			diffCount++
		}
	}
	if diffCount == 0 {
		return out + "expressions are equivalent: no differing assignments\n", nil
	}
	out += fmt.Sprintf("differing assignments: %d of %d\n", diffCount, len(diff))
	out += fmt.Sprintf("minimal cubes (%d):\n", len(cubes))
	for _, c := range cubes {
		out += "  " + c.String(vars) + "\n"
	}
	return out, nil
}

func main() {
	out, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Print(out)
}
