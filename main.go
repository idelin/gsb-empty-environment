package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

func main() {
	var (
		data []byte
		err  error
	)
	if len(os.Args) > 1 {
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
	plan, err := Solve(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid input:", err)
		os.Exit(1)
	}
	fmt.Print(Render(in, plan))
	if !plan.Found {
		os.Exit(2)
	}
}
