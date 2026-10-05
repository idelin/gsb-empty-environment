// Command macroexp expands macros in a text file.
//
// Usage: macroexp [-budget N] [file]   (reads stdin when no file is given)
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"macroexp"
)

func main() {
	budget := flag.Int("budget", macroexp.DefaultBudget, "macro expansion step budget")
	flag.Parse()

	var src []byte
	var err error
	file := "stdin"
	if flag.NArg() > 0 {
		file = flag.Arg(0)
		src, err = os.ReadFile(file)
	} else {
		src, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	exp := macroexp.New(macroexp.Options{Budget: *budget})
	res, errs := exp.Expand(file, string(src))
	fmt.Print(res.Text)
	for _, err := range errs {
		fmt.Fprintln(os.Stderr, err)
	}
	if len(errs) > 0 {
		os.Exit(1)
	}
}
