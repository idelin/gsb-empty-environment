// Command promlite evaluates restricted PromQL-style expressions against
// locally synthesized, fixture-replayable telemetry events.
//
//	promlite synth -out fixtures/demo.json
//	promlite run -fixture fixtures/demo.json -query 'rate(http_requests_total[2m])' [-at RFC3339]
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"promlite"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "synth":
		fs := flag.NewFlagSet("synth", flag.ExitOnError)
		out := fs.String("out", "fixtures/demo.json", "fixture output path")
		steps := fs.Int("steps", 20, "number of 30s steps")
		fs.Parse(os.Args[2:])
		store := promlite.Synth(*steps, 30*time.Second)
		if err := promlite.SaveFixture(*out, store); err != nil {
			fatal(err)
		}
		fmt.Printf("wrote %d events to %s\n", len(store.Samples()), *out)
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		fixture := fs.String("fixture", "fixtures/demo.json", "fixture input path")
		query := fs.String("query", "", "restricted expression to evaluate (required)")
		at := fs.String("at", "", "evaluation instant (RFC3339); default: newest event")
		fs.Parse(os.Args[2:])
		if *query == "" {
			fatal(fmt.Errorf("-query is required"))
		}
		store, err := promlite.LoadFixture(*fixture)
		if err != nil {
			fatal(err)
		}
		t, ok := store.MaxTime()
		if *at != "" {
			t, err = time.Parse(time.RFC3339, *at)
			if err != nil {
				fatal(fmt.Errorf("bad -at: %w", err))
			}
		} else if !ok {
			fatal(fmt.Errorf("fixture is empty"))
		}
		expr, err := promlite.Parse(*query)
		if err != nil {
			fatal(fmt.Errorf("parse error: %w", err))
		}
		val, trace, err := promlite.NewEvaluator(store).Eval(expr, t)
		if err != nil {
			fatal(fmt.Errorf("evaluation error: %w", err))
		}
		fmt.Printf("query:   %s\n", promlite.Describe(expr))
		fmt.Printf("eval at: %s\n\n", t.UTC().Format(time.RFC3339))
		printValue(val)
		fmt.Printf("\ndependency trace (per-step consumed sample ranges):\n%s", trace.Format())
	default:
		usage()
		os.Exit(2)
	}
}

func printValue(v promlite.Value) {
	if v.IsScalar {
		fmt.Printf("result: scalar %g\n", v.Scalar)
		return
	}
	fmt.Println("result:")
	vec := append([]promlite.Element(nil), v.Vector...)
	sort.Slice(vec, func(i, j int) bool { return vec[i].Labels.Key() < vec[j].Labels.Key() })
	if len(vec) == 0 {
		fmt.Println("  (empty vector)")
	}
	for _, el := range vec {
		fmt.Printf("  %s => %g\n", el.Labels.Key(), el.V)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: promlite <synth|run> [flags]")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
