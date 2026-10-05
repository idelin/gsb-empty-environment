package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

// usage: deletion-planner [input.json]
// Reads the planning request from the file argument, or stdin when no
// argument is given, and writes the plan as indented JSON to stdout.
func main() {
	var data []byte
	var err error
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: deletion-planner [input.json]")
		os.Exit(2)
	}
	if len(os.Args) == 2 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "read input:", err)
		os.Exit(1)
	}

	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintln(os.Stderr, "parse input:", err)
		os.Exit(1)
	}

	plan, err := PlanDeletion(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plan:", err)
		os.Exit(1)
	}

	out, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode output:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
