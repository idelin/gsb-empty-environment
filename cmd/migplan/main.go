// migplan reads migration steps as JSON from stdin (or a file argument)
// and prints the scheduled plan as JSON.
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"migplan/planner"
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
	var steps []planner.Step
	if err := json.Unmarshal(data, &steps); err != nil {
		fatal(fmt.Errorf("invalid steps JSON: %v", err))
	}
	plan, err := planner.Schedule(steps)
	if err != nil {
		fatal(err)
	}
	out, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "migplan: "+err.Error())
	os.Exit(1)
}
