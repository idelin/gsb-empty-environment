// Command sampler replays a telemetry fixture through the tail sampler
// and prints every decision. Telemetry is synthesized locally; nothing
// is exported to a monitoring platform.
package main

import (
	"fmt"
	"os"

	"tailsampler/sampler"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: sampler <fixture.json>")
		os.Exit(2)
	}
	f, err := sampler.LoadFixture(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	recs, err := sampler.Replay(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	kept, dropped := 0, 0
	for _, r := range recs {
		d := r.Decision
		verb := "drop"
		if d.Keep {
			verb = "keep"
			kept++
		} else {
			dropped++
		}
		late := ""
		if d.Late {
			late = " (late span)"
		}
		fmt.Printf("t=%s trace=%-6s shard=%d decision=%s reason=%-7s spans=%d%s\n",
			r.At.Format("15:04:05.000"), d.TraceID,
			sampler.ShardKey(d.TraceID, f.Config.NumShards),
			verb, d.Reason, d.Spans, late)
	}
	fmt.Printf("summary: kept=%d dropped=%d total=%d\n", kept, dropped, kept+dropped)
}
