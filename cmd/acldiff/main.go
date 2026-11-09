// Command acldiff diffs two IPv4 ACLs read from stdin as JSON:
//
//	{"old": [["10.0.0.0/8","deny"], ...], "new": [["10.1.0.0/16","allow"], ...]}
//
// Rules are evaluated top-down, first match wins, default deny. It prints
// a JSON array of [CIDR, oldAction, newAction] rows covering exactly the
// addresses whose effective action changed, merged into the minimal set
// of aligned CIDR blocks and sorted by start address.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"example.org/offlinecli"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "acldiff:", err)
	os.Exit(1)
}

func main() {
	var input struct {
		Old [][]string `json:"old"`
		New [][]string `json:"new"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		fatal(fmt.Errorf("reading stdin JSON: %w", err))
	}
	oldRules, err := offlinecli.ParseRules(input.Old)
	if err != nil {
		fatal(fmt.Errorf("old: %w", err))
	}
	newRules, err := offlinecli.ParseRules(input.New)
	if err != nil {
		fatal(fmt.Errorf("new: %w", err))
	}
	out, err := json.Marshal(offlinecli.DiffEntries(oldRules, newRules))
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))
}
