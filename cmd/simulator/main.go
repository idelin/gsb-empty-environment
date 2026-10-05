// Command simulator runs the coverage settlement simulator over a rule
// file and an event file (events are submitted in file order, i.e.
// arrival order) and prints the ledger plus a summary as JSON.
//
// Usage:
//
//	simulator -rules rules.json -events events.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"settlement-sim/settlement"
)

func load(path string, v interface{}) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func main() {
	rulesPath := flag.String("rules", "rules.json", "path to rule JSON")
	eventsPath := flag.String("events", "events.json", "path to events JSON (arrival order)")
	flag.Parse()

	var rule settlement.Rule
	if err := load(*rulesPath, &rule); err != nil {
		fmt.Fprintf(os.Stderr, "load rules: %v\n", err)
		os.Exit(1)
	}
	var events []settlement.ClaimEvent
	if err := load(*eventsPath, &events); err != nil {
		fmt.Fprintf(os.Stderr, "load events: %v\n", err)
		os.Exit(1)
	}

	engine, err := settlement.NewEngine(rule)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid rule: %v\n", err)
		os.Exit(1)
	}
	for _, ev := range events {
		if err := engine.Submit(ev); err != nil {
			fmt.Fprintf(os.Stderr, "submit event %q: %v\n", ev.ID, err)
			os.Exit(1)
		}
	}
	if err := engine.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invariant violation: %v\n", err)
		os.Exit(1)
	}

	out := struct {
		Ledger  []settlement.Record `json:"ledger"`
		Summary settlement.Summary  `json:"summary"`
	}{Ledger: engine.Ledger(), Summary: engine.Summarize()}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "encode output: %v\n", err)
		os.Exit(1)
	}
}
