package main

import (
	"fmt"
	"os"
	"sort"
)

func printOutputs(title string, e *Engine) {
	fmt.Println("== " + title)
	outs := e.Outputs()
	keys := make([]string, 0, len(outs))
	for k := range outs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		o := outs[k]
		var contrib []string
		for ik, v := range o.Contributions {
			contrib = append(contrib, fmt.Sprintf("%s=%g", PrettySeriesKey(ik), v))
		}
		sort.Strings(contrib)
		fmt.Printf("  %s value=%g contributions=%v\n", o.Labels, o.Value(), contrib)
	}
	fmt.Println()
}

func main() {
	reserved := []string{"__name__", "tenant"}

	// 1. Strict engine: rename + delete + synth, conflict on collision.
	strict := NewEngine(false, reserved, nil)
	must(strict.UpdateRules([]Rule{
		RenameLabel{RuleID: "r1", From: "job", To: "service"},
		SynthLabel{RuleID: "r2", Label: "site", Template: "{dc}-{env}"},
	}))
	must(strict.AddSeries(Series{Name: "http_requests_total",
		Labels: LabelSet{"job": "api", "instance": "a", "dc": "eu", "env": "prod"}, Value: 10}))
	printOutputs("rename job->service, synth site={dc}-{env}", strict)

	// 2. Deleting the distinguishing label must not overwrite: conflict.
	must(strict.UpdateRules([]Rule{
		RenameLabel{RuleID: "r1", From: "job", To: "service"},
		DeleteLabel{RuleID: "r3", Label: "instance"},
	}))
	err := strict.AddSeries(Series{Name: "http_requests_total",
		Labels: LabelSet{"job": "api", "instance": "b", "dc": "eu", "env": "prod"}, Value: 20})
	fmt.Printf("== delete distinguishing label, second series rejected:\n  %v\n\n", err)
	printOutputs("original series untouched (no overwrite)", strict)

	// 3. Reserved labels cannot be rewritten by normal rules.
	err = strict.UpdateRules([]Rule{DeleteLabel{RuleID: "bad", Label: "tenant"}})
	fmt.Printf("== rule targeting reserved label rejected:\n  %v\n\n", err)

	// 4. Aggregation engine: explicit merge, then retract -> split back.
	agg := NewEngine(true, reserved, nil)
	must(agg.UpdateRules([]Rule{DeleteLabel{RuleID: "r3", Label: "instance"}}))
	must(agg.AddSeries(Series{Name: "http_requests_total",
		Labels: LabelSet{"job": "api", "instance": "a"}, Value: 10}))
	must(agg.AddSeries(Series{Name: "http_requests_total",
		Labels: LabelSet{"job": "api", "instance": "b"}, Value: 20}))
	printOutputs("explicit aggregation merges contributions", agg)
	must(agg.RetractRule("r3"))
	printOutputs("after retracting delete rule: series split, contributions restored", agg)

	// 5. Atomic batch update with minimal conflicting pair.
	strict2 := NewEngine(false, reserved, nil)
	must(strict2.AddSeries(Series{Name: "m", Labels: LabelSet{"pod": "p1", "app": "x"}, Value: 1}))
	must(strict2.AddSeries(Series{Name: "m", Labels: LabelSet{"pod": "p2", "app": "x"}, Value: 2}))
	err = strict2.UpdateRules([]Rule{DeleteLabel{RuleID: "r9", Label: "pod"}})
	fmt.Printf("== atomic batch rejected, version stays %d:\n  %v\n\n", strict2.Version(), err)

	// 6. Small-domain enumeration verifies the full conflict scope.
	domain := map[string][]string{"pod": {"p1", "p2"}, "app": {"x", "y"}}
	all := EnumerateDomain("m", domain)
	graph := BuildConflictGraph(all, []Rule{DeleteLabel{RuleID: "r9", Label: "pod"}})
	fmt.Printf("== enumerated %d series, conflict nodes: %d\n", len(all), len(graph.Nodes))
	if pair, ok := graph.MinimalPair(); ok {
		fmt.Printf("  minimal conflicting pair: %s vs %s\n", PrettySeriesKey(pair[0]), PrettySeriesKey(pair[1]))
	}
	graph.Retract("r9")
	fmt.Printf("  after retracting r9: conflicts=%v\n\n", graph.HasConflicts())

	// 7. Telemetry: dump local events to a fixture and replay them.
	tele := NewTelemetry()
	demo := NewEngine(true, reserved, tele)
	must(demo.UpdateRules([]Rule{DeleteLabel{RuleID: "r3", Label: "instance"}}))
	must(demo.AddSeries(Series{Name: "m", Labels: LabelSet{"instance": "a"}, Value: 1}))
	must(demo.AddSeries(Series{Name: "m", Labels: LabelSet{"instance": "b"}, Value: 2}))
	must(demo.RetractRule("r3"))
	f, err := os.Create("telemetry_fixture.jsonl")
	must(err)
	must(tele.EncodeJSONL(f))
	f.Close()
	rf, err := os.Open("telemetry_fixture.jsonl")
	must(err)
	events, err := DecodeEvents(rf)
	must(err)
	rf.Close()
	p := Replay(events)
	fmt.Printf("== telemetry replayed from telemetry_fixture.jsonl:\n  added=%d merged=%d split=%d published=%d retracted=%d cache(store=%d hit=%d)\n",
		p.Added, p.Merged, p.Split, p.Published, p.Retracted, p.CacheStore, p.CacheHit)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
