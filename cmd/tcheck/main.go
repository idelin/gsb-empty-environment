// Command tcheck checks temporal properties of a proposition-labelled
// finite state machine described in JSON.
//
// Usage:
//
//	tcheck [-request req] [-response resp] [-danger danger] [-auth authorized] model.json
//
// Exit status is 0 when both properties hold, 1 when either fails.
package main

import (
	"flag"
	"fmt"
	"os"

	"tcheck/tcheck"
)

func main() {
	request := flag.String("request", "request", "request proposition")
	response := flag.String("response", "response", "response proposition")
	danger := flag.String("danger", "danger", "danger proposition")
	auth := flag.String("auth", "authorized", "authorization proposition")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: tcheck [flags] model.json")
		os.Exit(2)
	}

	m, err := tcheck.LoadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	safety := m.CheckSafety(*danger, *auth)
	liveness := m.CheckLiveness(*request, *response)

	fmt.Println(safety)
	fmt.Println(liveness)

	if !safety.OK || !liveness.OK {
		os.Exit(1)
	}
}
