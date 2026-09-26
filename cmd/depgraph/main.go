// Command depgraph builds dependency edges from locally synthesized or
// fixture-replayed telemetry. It prints machine-checkable text output;
// it is not a dashboard and talks to no monitoring platform.
package main

import (
	"flag"
	"fmt"
	"os"

	"depgraph/internal/fixture"
	"depgraph/internal/pairing"
	"depgraph/internal/telemetry"
)

func main() {
	fixturePath := flag.String("fixture", "", "replay spans from this JSON fixture")
	synthesizeTo := flag.String("synthesize", "", "synthesize telemetry and write it to this path")
	seed := flag.Int64("seed", 42, "seed for local synthesis")
	flag.Parse()

	if *synthesizeTo != "" {
		spans := telemetry.Synthesize(*seed)
		f, err := os.Create(*synthesizeTo)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		if err := fixture.Write(f, spans); err != nil {
			fatal(err)
		}
		fmt.Printf("synthesized %d spans (seed %d) -> %s\n", len(spans), *seed, *synthesizeTo)
		return
	}

	var spans []telemetry.Span
	if *fixturePath != "" {
		f, err := os.Open(*fixturePath)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		loaded, err := fixture.Read(f)
		if err != nil {
			fatal(err)
		}
		spans = fixture.Replay(loaded)
	} else {
		spans = fixture.Replay(telemetry.Synthesize(*seed))
	}

	res := pairing.Build(spans)

	fmt.Printf("spans: %d\n", len(spans))
	fmt.Println("edges (from -> to, success, error):")
	for _, e := range res.Edges {
		fmt.Printf("  %s -> %s  success=%d error=%d\n", e.Edge.From, e.Edge.To, e.Success, e.Error)
	}
	fmt.Printf("uncertain evidence: %d\n", len(res.Uncertain))
	for _, ev := range res.Uncertain {
		fmt.Printf("  [%s] kind=%s service=%q op=%q request=%s attempt=%d status=%s\n",
			ev.Reason, ev.Span.Kind, ev.Span.Service, ev.Span.Operation,
			ev.Span.RequestID, ev.Span.Attempt, ev.Span.Status)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
