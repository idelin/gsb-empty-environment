// Command docexec runs the runnable example blocks of a Markdown document
// in dependency order and prints either a report or an annotated document.
//
// Usage:
//
//	docexec [-annotate] <file.md>
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"docexec/docexec"
)

func main() {
	annotate := flag.Bool("annotate", false, "print the document with block outputs inserted")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: docexec [-annotate] <file.md>")
		os.Exit(2)
	}
	data, err := ioutil.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "docexec:", err)
		os.Exit(1)
	}
	doc := docexec.Parse(string(data))
	res := docexec.Run(doc)
	if *annotate {
		fmt.Print(docexec.Annotate(doc, res))
	} else {
		fmt.Print(docexec.Report(res))
	}
	for _, r := range res.Results {
		if r.Status == docexec.StatusFailed {
			os.Exit(1)
		}
	}
}
