// Command updater reads an update problem as JSON and prints the optimal
// new lock file. Usage:
//
//	updater [problem.json]   (reads stdin when no file is given)
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"updater"
)

func main() {
	var data []byte
	var err error
	if len(os.Args) > 1 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal(err)
	}
	var problem updater.Problem
	if err := json.Unmarshal(data, &problem); err != nil {
		fatal(err)
	}
	solution := problem.Solve()
	if solution == nil {
		fatal(fmt.Errorf("no feasible update: the security blocklist and constraints cannot all be satisfied"))
	}
	out, err := json.MarshalIndent(solution, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "updater:", err)
	os.Exit(1)
}
