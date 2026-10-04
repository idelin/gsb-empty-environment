// Command replay runs a Multi-Paxos simulation script and prints the full
// message trace, chosen-value evidence, and final node states.
//
//	usage: replay <script>
//
// See paxos.RunScript for the script language.
package main

import (
	"fmt"
	"os"

	"multipaxos/paxos"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: replay <script>\n")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "replay: %v\n", err)
		os.Exit(2)
	}
	sim := paxos.NewSim()
	runErr := paxos.RunScript(sim, string(data))
	for _, l := range sim.Log() {
		fmt.Println(l)
	}
	fmt.Println(sim.Report())
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "replay failed: %v\n", runErr)
		os.Exit(1)
	}
}
