// Command spaneval evaluates span telemetry against an expected structure.
// All telemetry is synthesized locally or replayed from fixtures; there is
// no monitoring-platform integration and no display dashboard.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"spaneval"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "eval":
		err = cmdEval(os.Args[2:])
	case "synth":
		err = cmdSynth(os.Args[2:])
	case "revise":
		err = cmdRevise(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  spaneval eval   -rules rules.json -events events.json [-now N] [-deadline N]
  spaneval synth  -rules rules.json -attrs k=v,... [-seed N] [-drop a,b] [-dup a] [-open a]
  spaneval revise -rules rules.json -base base.json -late late.json [-now N] [-deadline N]`)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func maxTS(events []spaneval.Event) int64 {
	var max int64
	for _, ev := range events {
		if ev.TS > max {
			max = ev.TS
		}
	}
	return max
}

func cmdEval(args []string) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	rulesPath := fs.String("rules", "", "rule graph JSON")
	eventsPath := fs.String("events", "", "event log JSON")
	now := fs.Int64("now", 0, "evaluation time (default: max event ts)")
	deadline := fs.Int64("deadline", 0, "budget deadline (default: now)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := spaneval.LoadRules(*rulesPath)
	if err != nil {
		return err
	}
	events, err := spaneval.LoadEvents(*eventsPath)
	if err != nil {
		return err
	}
	if *now == 0 {
		*now = maxTS(events)
	}
	if *deadline == 0 {
		*deadline = *now
	}
	rep, err := spaneval.Evaluate(root, events, *now, *deadline)
	if err != nil {
		return err
	}
	return printJSON(rep)
}

func cmdSynth(args []string) error {
	fs := flag.NewFlagSet("synth", flag.ContinueOnError)
	rulesPath := fs.String("rules", "", "rule graph JSON")
	attrStr := fs.String("attrs", "", "trace attrs k=v,k2=v2")
	traceID := fs.String("trace", "t1", "trace id")
	seed := fs.Int64("seed", 1, "random seed")
	drop := fs.String("drop", "", "comma-separated span names to drop")
	dup := fs.String("dup", "", "comma-separated span names to duplicate")
	open := fs.String("open", "", "comma-separated span names to leave unfinished")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := spaneval.LoadRules(*rulesPath)
	if err != nil {
		return err
	}
	attrs := map[string]string{}
	for _, kv := range strings.Split(*attrStr, ",") {
		if i := strings.Index(kv, "="); i > 0 {
			attrs[kv[:i]] = kv[i+1:]
		}
	}
	opts := spaneval.SynthOptions{
		Seed:      *seed,
		Drop:      nameSet(*drop),
		Duplicate: nameSet(*dup),
		LeaveOpen: nameSet(*open),
	}
	return printJSON(spaneval.Synth(root, *traceID, attrs, opts))
}

func cmdRevise(args []string) error {
	fs := flag.NewFlagSet("revise", flag.ContinueOnError)
	rulesPath := fs.String("rules", "", "rule graph JSON")
	basePath := fs.String("base", "", "base event log JSON")
	latePath := fs.String("late", "", "late-arriving events JSON")
	now := fs.Int64("now", 0, "evaluation time (default: max event ts)")
	deadline := fs.Int64("deadline", 0, "budget deadline (default: now)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := spaneval.LoadRules(*rulesPath)
	if err != nil {
		return err
	}
	base, err := spaneval.LoadEvents(*basePath)
	if err != nil {
		return err
	}
	late, err := spaneval.LoadEvents(*latePath)
	if err != nil {
		return err
	}
	if *now == 0 {
		if m := maxTS(append(base, late...)); m > *now {
			*now = m
		}
	}
	if *deadline == 0 {
		*deadline = *now
	}
	rev, err := spaneval.Revise(root, base, late, *now, *deadline)
	if err != nil {
		return err
	}
	return printJSON(rev)
}

func nameSet(csv string) map[string]bool {
	set := map[string]bool{}
	for _, n := range strings.Split(csv, ",") {
		if n != "" {
			set[n] = true
		}
	}
	return set
}
