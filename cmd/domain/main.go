// Command domain analyzes the domain of an expression over a rational
// interval:  domain '<expr>' <lo> <hi>
package main

import (
	"fmt"
	"math/big"
	"os"

	"domain"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr, "usage: domain '<expr>' <lo> <hi>\n")
		fmt.Fprintf(os.Stderr, "example: domain 'x^(1/2) + 1/(x-1)' -2 9/2\n")
		os.Exit(2)
	}
	e, err := domain.Parse(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse error: %v\n", err)
		os.Exit(1)
	}
	lo, ok1 := new(big.Rat).SetString(os.Args[2])
	hi, ok2 := new(big.Rat).SetString(os.Args[3])
	if !ok1 || !ok2 || lo.Cmp(hi) > 0 {
		fmt.Fprintf(os.Stderr, "invalid range [%q, %q]\n", os.Args[2], os.Args[3])
		os.Exit(1)
	}
	d := domain.DomainOf(e, domain.Interval{Lo: lo, Hi: hi})
	fmt.Printf("expr:   %s\n", e)
	fmt.Printf("range:  [%s, %s]\n", lo.RatString(), hi.RatString())
	fmt.Printf("domain: %s\n", d)
	for _, u := range d.Unsupported {
		fmt.Printf("unsupported: %s\n", u)
	}
}
