// Command docexec executes the runnable examples in a markdown-like document
// and prints a report mapping every output back to its source line.
package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"docexec"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: docexec <document.md>")
		os.Exit(2)
	}
	src, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	doc, err := docexec.Parse(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse error:", err)
		os.Exit(1)
	}
	rep, err := docexec.Execute(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	failed := 0
	for _, r := range rep.Results {
		b := r.Block
		fmt.Printf("[%s] block %q (lines %d-%d)\n", r.Status, b.ID, b.StartLine, b.EndLine)
		for _, o := range r.Outputs {
			fmt.Printf("  line %d | %s\n", o.Line, o.Text)
		}
		if r.Status == docexec.Failed {
			fmt.Printf("  error at line %d: %s\n", r.ErrLine, r.Err)
			failed++
		}
		if r.Status == docexec.Skipped {
			fmt.Printf("  skipped: %s\n", r.SkipReason)
		}
	}
	fmt.Println("final namespace:")
	if len(rep.Namespace) == 0 {
		fmt.Println("  (empty)")
	}
	for _, r := range rep.Results {
		for _, name := range r.Block.Defines {
			if v, ok := rep.Namespace[name]; ok {
				fmt.Printf("  %s = %s\n", name, v)
			}
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}
