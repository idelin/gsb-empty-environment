// Command capgate is the Go build-host entry point for the capability
// isolation acceptance gate. It reads a Job as JSON (a file path argument or
// stdin when "-" is passed) and prints the acceptance Report as JSON.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	capgate "formal.local/w28-372608570c"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "capgate:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var raw []byte
	var err error
	switch {
	case len(args) == 0 || args[0] == "-":
		raw, err = readAll(os.Stdin)
	case len(args) == 1:
		raw, err = os.ReadFile(args[0])
	default:
		return fmt.Errorf("usage: capgate [job.json | -]")
	}
	if err != nil {
		return err
	}

	var job capgate.Job
	dec := json.NewDecoder(newReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&job); err != nil {
		return fmt.Errorf("invalid job json: %w", err)
	}
	report, err := capgate.Analyze(&job)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	_, err = os.Stdout.Write(out)
	return err
}

func readAll(r io.Reader) ([]byte, error) { return io.ReadAll(r) }

func newReader(b []byte) io.Reader { return strings.NewReader(string(b)) }
