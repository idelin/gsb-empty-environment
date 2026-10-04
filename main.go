// Command invcheck verifies a candidate loop invariant for an integer affine
// loop over a finite domain. The program is read as JSON from a file or
// stdin and the verdict is printed as JSON.
//
// Exit codes: 0 = valid, 1 = invalid (concrete counterexample reported),
// 2 = unknown (out-of-bounds transitions could not be verified),
// 3 = usage or input error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"invcheck/checker"
)

func main() {
	maxStates := flag.Uint64("max-states", 5000000, "maximum number of states to enumerate")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: invcheck [-max-states N] [input.json]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	var data []byte
	var err error
	if flag.NArg() > 0 {
		data, err = ioutil.ReadFile(flag.Arg(0))
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "invcheck: read input:", err)
		os.Exit(3)
	}

	var prog checker.Program
	if err := json.Unmarshal(data, &prog); err != nil {
		fmt.Fprintln(os.Stderr, "invcheck: parse input:", err)
		os.Exit(3)
	}
	if err := prog.Validate(*maxStates); err != nil {
		fmt.Fprintln(os.Stderr, "invcheck: invalid program:", err)
		os.Exit(3)
	}

	rep := prog.Check()
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "invcheck: encode report:", err)
		os.Exit(3)
	}
	fmt.Println(string(out))

	switch rep.Verdict {
	case checker.VerdictValid:
		os.Exit(0)
	case checker.VerdictInvalid:
		os.Exit(1)
	default:
		os.Exit(2)
	}
}
