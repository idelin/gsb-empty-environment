// Command domain analyzes the exact domain of a rational-power expression.
//
// Usage:
//
//	domain [-lo-open] [-hi-open] <expr> <var> <lo> <hi>
//
// Example:
//
//	domain "(x-1)^(1/2) + 1/(x-5)" x 0 10
package main

import (
	"flag"
	"fmt"
	"os"

	"domain"
)

func main() {
	loOpen := flag.Bool("lo-open", false, "left endpoint is open")
	hiOpen := flag.Bool("hi-open", false, "right endpoint is open")
	flag.Parse()
	if flag.NArg() != 4 {
		fmt.Fprintln(os.Stderr, "usage: domain [-lo-open] [-hi-open] <expr> <var> <lo> <hi>")
		os.Exit(2)
	}
	res, err := domain.AnalyzeString(flag.Arg(0), flag.Arg(1), flag.Arg(2), flag.Arg(3), *loOpen, *hiOpen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(res)
}
