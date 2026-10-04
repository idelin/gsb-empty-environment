package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "reconcile: %v\n", err)
		os.Exit(1)
	}
}

// run parses the log, reconciles it, and prints the report as JSON.
// Exit code is 0 for determined/ambiguous, 2 for contradiction (the report
// is still printed), and 1 for usage or input errors.
func run(args []string, stdin io.Reader, stdout io.Writer) error {
	var data []byte
	var err error
	switch {
	case len(args) == 0 || args[0] == "-":
		data, err = io.ReadAll(stdin)
	case len(args) == 1:
		data, err = os.ReadFile(args[0])
	default:
		return fmt.Errorf("usage: reconcile [events.json|-]")
	}
	if err != nil {
		return err
	}

	events, err := parseEvents(data)
	if err != nil {
		return err
	}
	report, err := Reconcile(events)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, string(out))
	if report.Status == "contradiction" {
		os.Exit(2)
	}
	return nil
}

// parseEvents accepts either a JSON array of events or an object with an
// "events" array.
func parseEvents(data []byte) ([]Event, error) {
	var events []Event
	if err := json.Unmarshal(data, &events); err == nil && events != nil {
		return events, nil
	}
	var wrapper struct {
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Events != nil {
		return wrapper.Events, nil
	}
	return nil, fmt.Errorf("input must be a JSON array of events or an object with an \"events\" array")
}
