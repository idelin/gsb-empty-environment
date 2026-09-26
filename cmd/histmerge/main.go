// Command histmerge replays a fixture of locally synthesized telemetry
// events and prints the resulting snapshots. It exits non-zero when the
// replay rejects its input (invalid counts, incompatible bounds, overflow).
package main

import (
	"flag"
	"fmt"
	"os"

	"histmerge"
)

func main() {
	fixture := flag.String("fixture", "", "path to a JSONL event fixture (required)")
	gen := flag.Int("gen", 0, "ignore the fixture and synthesize N observations locally instead")
	seed := flag.Int64("seed", 42, "random seed used with -gen")
	flag.Parse()

	var events []histmerge.Event
	if *gen > 0 {
		events = histmerge.SynthesizeEvents(*seed, *gen)
		fmt.Printf("synthesized %d events locally (seed %d)\n", len(events), *seed)
	} else {
		if *fixture == "" {
			fmt.Fprintln(os.Stderr, "usage: histmerge -fixture events.jsonl | -gen N [-seed S]")
			os.Exit(2)
		}
		f, err := os.Open(*fixture)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		defer f.Close()
		events, err = histmerge.LoadEvents(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error loading fixture:", err)
			os.Exit(2)
		}
	}

	res, err := histmerge.Replay(events)
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay rejected:", err)
		os.Exit(1)
	}
	for _, s := range res.Snapshots {
		fmt.Printf("snapshot %s\n  bounds: %v\n  counts: %v\n", s.Name, s.Bounds, s.Counts)
	}
	for name, n := range res.Dropped {
		fmt.Printf("dropped %d observation(s) above last bound of %s\n", n, name)
	}
}
