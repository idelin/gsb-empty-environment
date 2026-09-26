package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "synth":
		err = cmdSynth(os.Args[2:])
	case "replay":
		err = cmdReplay(os.Args[2:])
	case "merge":
		err = cmdMerge(os.Args[2:])
	case "limits":
		err = cmdLimits(os.Args[2:])
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
  histmerge synth  [-n N] [-seed S] [-out file]   synthesize telemetry events (JSON lines)
  histmerge replay <fixture.jsonl>                replay events and merge per metric
  histmerge merge  <a.json> <b.json>              merge two histograms (exact only)
  histmerge limits <a.json> <b.json>              print merged [min,max] per bound`)
}

func cmdSynth(args []string) error {
	fs := flag.NewFlagSet("synth", flag.ContinueOnError)
	n := fs.Int("n", 6, "number of events")
	seed := fs.Int64("seed", 1, "random seed (deterministic)")
	out := fs.String("out", "", "output file (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	events := Synthesize(*seed, *n)
	if *out == "" {
		return WriteEvents(os.Stdout, events)
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()
	return WriteEvents(f, events)
}

func cmdReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("replay requires exactly one fixture file")
	}
	events, err := LoadFixture(fs.Arg(0))
	if err != nil {
		return err
	}
	res := Replay(events)
	printReplay(res)
	if len(res.Errors) > 0 {
		return fmt.Errorf("%d metric(s) could not be merged exactly", len(res.Errors))
	}
	return nil
}

func printReplay(res ReplayResult) {
	for name, h := range res.Merged {
		fmt.Printf("metric %s merged:\n", name)
		printHistogram(h)
	}
	for name, r := range res.Ranges {
		fmt.Printf("metric %s not exactly mergeable; limits per bound:\n", name)
		for i, b := range r.Bounds {
			fmt.Printf("  <= %v: [%d, %d]\n", b, r.Min[i], r.Max[i])
		}
	}
	for name, err := range res.Errors {
		fmt.Printf("metric %s: %v\n", name, err)
	}
}

func printHistogram(h Histogram) {
	for i, b := range h.Bounds {
		fmt.Printf("  <= %v: %d\n", b, h.Counts[i])
	}
}

func loadHistogram(path string) (Histogram, error) {
	f, err := os.Open(path)
	if err != nil {
		return Histogram{}, err
	}
	defer f.Close()
	var h Histogram
	if err := json.NewDecoder(f).Decode(&h); err != nil {
		return Histogram{}, fmt.Errorf("%s: %w", path, err)
	}
	return h, h.Validate()
}

func cmdMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("merge requires two histogram JSON files")
	}
	a, err := loadHistogram(fs.Arg(0))
	if err != nil {
		return err
	}
	b, err := loadHistogram(fs.Arg(1))
	if err != nil {
		return err
	}
	merged, err := Merge(a, b)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(merged)
}

func cmdLimits(args []string) error {
	fs := flag.NewFlagSet("limits", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("limits requires two histogram JSON files")
	}
	a, err := loadHistogram(fs.Arg(0))
	if err != nil {
		return err
	}
	b, err := loadHistogram(fs.Arg(1))
	if err != nil {
		return err
	}
	r, err := Limits(a, b, unionBounds(a.Bounds, b.Bounds))
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
