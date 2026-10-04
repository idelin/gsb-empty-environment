// Command macroexp expands a source file and prints every output token
// with its provenance, followed by the rendered text and diagnostics.
package main

import (
	"fmt"
	"os"

	"macroexp"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: macroexp <file>")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	res := macroexp.Expand(os.Args[1], string(data), 0)
	for _, t := range res.Tokens {
		call := "-"
		if !t.CallPos.IsZero() {
			call = t.CallPos.String()
		}
		fmt.Printf("%-14s def=%-8s call=%-8s chain=%s\n",
			t.Text, t.DefPos, call, macroexp.FormatChain(t.Chain))
	}
	fmt.Println("---")
	fmt.Println(res.String())
	for _, d := range res.Diags {
		fmt.Fprintln(os.Stderr, d)
	}
	if len(res.Diags) > 0 {
		os.Exit(1)
	}
}
