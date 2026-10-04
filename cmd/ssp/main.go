// Command ssp computes shortest paths from one or more sources in a
// directed graph that may contain negative edges and negative cycles.
//
// Input is JSON on stdin or via -graph:
//
//	{
//	  "edges":   [{"from": "a", "to": "b", "weight": 2.5}, ...],
//	  "sources": ["s1", "s2"],
//	  "targets": ["t1", "t2"]        // optional: defaults to all vertices
//	}
//
// For every (source, target) pair the tool reports a finite distance with a
// shortest path, "unreachable", or "negative-infinity" together with a
// witness: a simple negative cycle reachable from the source that can reach
// the target, plus the two connecting paths.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"

	"ssp"
)

type edgeJSON struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Weight float64 `json:"weight"`
}

type inputJSON struct {
	Edges   []edgeJSON `json:"edges"`
	Sources []string   `json:"sources"`
	Targets []string   `json:"targets"`
}

type targetOut struct {
	Target   string   `json:"target"`
	Status   string   `json:"status"`
	Distance *float64 `json:"distance,omitempty"`
	Path     []string `json:"path,omitempty"`
	Evidence *evidOut `json:"evidence,omitempty"`
}

type evidOut struct {
	Cycle          []string `json:"cycle"`
	CycleWeight    float64  `json:"cycleWeight"`
	PathFromSource []string `json:"pathFromSource"`
	PathToTarget   []string `json:"pathToTarget"`
}

type sourceOut struct {
	Source  string      `json:"source"`
	Targets []targetOut `json:"targets"`
}

func names(g *ssp.Graph, ids []int) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = g.Names[id]
	}
	return out
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "ssp: "+format+"\n", args...)
	os.Exit(2)
}

func main() {
	graphFile := flag.String("graph", "-", "JSON graph file, or - for stdin")
	srcFlag := flag.String("sources", "", "comma-separated sources (overrides JSON)")
	tgtFlag := flag.String("targets", "", "comma-separated targets (overrides JSON)")
	format := flag.String("format", "text", "output format: text or json")
	verify := flag.Bool("verify", false, "cross-check every answer against brute-force simple-cycle enumeration")
	flag.Parse()

	var r io.Reader = os.Stdin
	if *graphFile != "-" {
		f, err := os.Open(*graphFile)
		if err != nil {
			fail("open graph: %v", err)
		}
		defer f.Close()
		r = f
	}
	data, err := ioutil.ReadAll(r)
	if err != nil {
		fail("read input: %v", err)
	}
	var in inputJSON
	if err := json.Unmarshal(data, &in); err != nil {
		fail("parse JSON: %v", err)
	}

	g := ssp.NewGraph()
	for _, e := range in.Edges {
		g.AddEdge(e.From, e.To, e.Weight)
	}

	srcNames := in.Sources
	if *srcFlag != "" {
		srcNames = strings.Split(*srcFlag, ",")
	}
	if len(srcNames) == 0 {
		fail("no sources given (JSON \"sources\" or -sources)")
	}
	tgtNames := in.Targets
	if *tgtFlag != "" {
		tgtNames = strings.Split(*tgtFlag, ",")
	}
	if len(tgtNames) == 0 {
		tgtNames = g.Names // default: every vertex
	}

	var sources []int
	for _, s := range srcNames {
		id, ok := g.Lookup(strings.TrimSpace(s))
		if !ok {
			fail("unknown source vertex %q", s)
		}
		sources = append(sources, id)
	}
	var targets []int
	for _, t := range tgtNames {
		id, ok := g.Lookup(strings.TrimSpace(t))
		if !ok {
			fail("unknown target vertex %q", t)
		}
		targets = append(targets, id)
	}

	var all []sourceOut
	verifyFailed := false
	for _, src := range sources {
		res := ssp.ShortestPaths(g, src)
		if *verify {
			for _, p := range ssp.CrossCheck(g, src, res) {
				verifyFailed = true
				fmt.Fprintf(os.Stderr, "verify: MISMATCH %s\n", p)
			}
		}
		so := sourceOut{Source: g.Names[src]}
		for _, t := range targets {
			to := targetOut{Target: g.Names[t], Status: res.Status[t].String()}
			switch res.Status[t] {
			case ssp.Finite:
				d := res.Dist[t]
				to.Distance = &d
				to.Path = names(g, res.Path(g, t))
			case ssp.NegInf:
				ev := res.Evidence(g, t)
				if ev == nil {
					fail("internal: no evidence found for %s -> %s", g.Names[src], g.Names[t])
				}
				to.Evidence = &evidOut{
					Cycle:          names(g, ev.Cycle),
					CycleWeight:    ev.CycleWeight,
					PathFromSource: names(g, ev.FromSource),
					PathToTarget:   names(g, ev.ToTarget),
				}
			}
			so.Targets = append(so.Targets, to)
		}
		all = append(all, so)
	}

	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]interface{}{"results": all}); err != nil {
			fail("encode: %v", err)
		}
	} else {
		printText(all)
	}
	if verifyFailed {
		os.Exit(1)
	}
}

func printText(all []sourceOut) {
	for _, so := range all {
		fmt.Printf("source %s\n", so.Source)
		for _, t := range so.Targets {
			switch t.Status {
			case "finite":
				fmt.Printf("  -> %s: distance %g\n", t.Target, *t.Distance)
				fmt.Printf("     path: %s\n", strings.Join(t.Path, " -> "))
			case "unreachable":
				fmt.Printf("  -> %s: unreachable\n", t.Target)
			case "negative-infinity":
				fmt.Printf("  -> %s: -inf (affected by reachable negative cycle)\n", t.Target)
				e := t.Evidence
				fmt.Printf("     cycle: %s -> %s (total weight %g)\n",
					strings.Join(e.Cycle, " -> "), e.Cycle[0], e.CycleWeight)
				fmt.Printf("     path from source: %s\n", strings.Join(e.PathFromSource, " -> "))
				fmt.Printf("     path to target:   %s\n", strings.Join(e.PathToTarget, " -> "))
			}
		}
	}
}
