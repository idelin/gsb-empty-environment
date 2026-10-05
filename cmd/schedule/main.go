// Command schedule is a JSON-lines CLI over the schedule library.
// It reads one JSON command per line from stdin and writes one JSON
// response per line to stdout.
package main

import (
	"fmt"
	"os"

	"schedule"
)

func main() {
	if err := schedule.RunCLI(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "schedule:", err)
		os.Exit(1)
	}
}
