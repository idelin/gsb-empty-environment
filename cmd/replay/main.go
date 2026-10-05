// Command replay runs a Multi-Paxos simulation script and prints the full
// event trace, chosen evidence, applied prefixes and node states.
//
// Usage:
//
//	replay [script-file]        (reads stdin when no file is given)
//
// Script commands (one per line, '#' starts a comment):
//
//	tick <n>            set the integer logical clock
//	leader <node>       node starts phase 1 with a fresh ballot
//	propose <node> <v>  submit client value v to node
//	deliver <id>        deliver pending message id
//	deliverto <node>    deliver all pending messages addressed to node
//	deliverall          deliver every pending message (FIFO, fair)
//	drop <id>           drop pending message id
//	dropto <node>       drop all pending messages addressed to node
//	dropall             drop every pending message
//	dup <id>            duplicate pending message id
//	crash <node>        crash node (volatile state lost)
//	restart <node>      restart node (durable state reloaded)
//	state               print chosen evidence, applied prefixes, node states
//	check               run the independent safety checker
package main

import (
	"fmt"
	"io"
	"os"

	paxos "paxossim"
)

func main() {
	var r io.Reader = os.Stdin
	if len(os.Args) > 1 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		defer f.Close()
		r = f
	}
	sim, _ := paxos.NewSimMem(5)
	if err := paxos.RunScript(sim, r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "replay error:", err)
		os.Exit(1)
	}
}
