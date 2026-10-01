// Command alertseg replays telemetry fixtures through the alert segment
// engine and prints the resulting segments and notifications.
//
// Usage: alertseg fixture.json [fixture.json ...]
package main

import (
	"flag"
	"fmt"
	"os"

	"alertseg/alertseg"
)

func main() {
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: alertseg fixture.json [fixture.json ...]")
		os.Exit(2)
	}
	failed := false
	for _, path := range flag.Args() {
		if err := replay(path); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func replay(path string) error {
	f, err := alertseg.LoadFixture(path)
	if err != nil {
		return err
	}
	eng, err := f.Replay()
	if err != nil {
		return err
	}
	fmt.Printf("=== %s (%s)\n", f.Name, path)
	fmt.Println("segments:")
	for _, s := range eng.Segments() {
		fmt.Printf("  %s\n", s)
	}
	fmt.Println("notifications:")
	ns := eng.Notifications()
	if len(ns) == 0 {
		fmt.Println("  (none)")
	}
	for _, n := range ns {
		fmt.Printf("  %s\n", n)
	}
	fmt.Println()
	return nil
}
