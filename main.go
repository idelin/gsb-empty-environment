// Command signal-recover decides whether partial linear observations of a
// bounded integer signal determine it uniquely. It reads a JSON instance
// from a file argument or stdin and writes a JSON verdict to stdout.
//
// Input:
//
//	{
//	  "matrix": [[1, 1], [1, -1]],   // m observation rows, n entries each
//	  "observations": [5, 1],        // m observed values
//	  "lower": [0, 0],               // n lower bounds (inclusive)
//	  "upper": [10, 10]              // n upper bounds (inclusive)
//	}
//
// Numbers may be JSON integers, quoted integers, or quoted fractions like
// "3/2"; all arithmetic is exact. The signal dimension is at most 12.
//
// Output is one of:
//
//	{"status": "unique", "signal": [...]}
//	{"status": "multiple", "signals": [[...], [...]]}
//	{"status": "infeasible", "conflict": {"observations": [...], "explanation": "..."}}
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func main() {
	var data []byte
	var err error
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: signal-recover [instance.json]  (reads stdin when no file is given)")
		os.Exit(2)
	}
	if len(os.Args) == 2 {
		data, err = os.ReadFile(os.Args[1])
	} else {
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal(err)
	}
	prob, err := parseProblem(data)
	if err != nil {
		fatal(err)
	}
	res := Solve(prob)
	out, err := json.MarshalIndent(toJSON(res), "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))
}

func fatal(err error) {
	msg, _ := json.Marshal(err.Error())
	fmt.Fprintf(os.Stderr, `{"status":"error","error":%s}`+"\n", msg)
	os.Exit(1)
}
