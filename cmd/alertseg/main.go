package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"alertseg"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "replay":
		cmdReplay(os.Args[2:])
	case "synthesize":
		cmdSynthesize(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  alertseg replay <fixture.json>   replay a telemetry fixture
  alertseg synthesize [flags]      synthesize a fixture locally, print JSON
    flags: -seed 1 -events 6 -sources db-1,api-1 -start 2026-01-01T00:00:00Z`)
}

func cmdReplay(args []string) {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	f, err := alertseg.LoadFixture(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	m, err := alertseg.Replay(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fmt.Printf("fixture: %s (repeat interval: %s)\n\n", f.Name, orDash(f.RepeatInterval))
	fmt.Println("notifications:")
	if len(m.Notifications()) == 0 {
		fmt.Println("  (none)")
	}
	for _, n := range m.Notifications() {
		fmt.Printf("  %s  %-8s %-8s %-18s seg[%s +%s events=%d]\n",
			n.Time.Format(time.RFC3339), n.Source, n.Kind, n.Reason,
			n.Segment.State, dur(n.Segment), n.Segment.Events)
	}

	fmt.Println("\nsegments:")
	for _, s := range m.Segments() {
		end := "open"
		if !s.Open {
			end = s.End.Format(time.RFC3339)
		}
		fmt.Printf("  %-8s %-6s %s -> %s  events=%d\n",
			s.Source, s.State, s.Start.Format(time.RFC3339), end, s.Events)
	}
}

func cmdSynthesize(args []string) {
	fs := flag.NewFlagSet("synthesize", flag.ExitOnError)
	seed := fs.Int64("seed", 1, "random seed (deterministic)")
	events := fs.Int("events", 6, "state changes per source")
	sources := fs.String("sources", "db-1,api-1", "comma-separated source names")
	start := fs.String("start", "2026-01-01T00:00:00Z", "RFC3339 start time")
	fs.Parse(args)

	t, err := time.Parse(time.RFC3339, *start)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: bad -start:", err)
		os.Exit(1)
	}
	var srcs []string
	for _, s := range splitComma(*sources) {
		srcs = append(srcs, s)
	}
	f := alertseg.Synthesize(*seed, t, srcs, *events)
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	return append(out, cur)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func dur(s alertseg.Segment) string {
	end := s.End
	if s.Open {
		end = s.Start // unknown; report zero-length
	}
	return end.Sub(s.Start).Truncate(time.Minute).String()
}
