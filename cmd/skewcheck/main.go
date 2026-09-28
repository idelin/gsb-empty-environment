// Command skewcheck derives per-node clock-offset feasible intervals
// from a locally replayed telemetry fixture and reports contradictions.
// It is a command line analyzer, not a dashboard: there is no platform
// integration and no long-running UI.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"skewcheck/fixture"
	"skewcheck/skew"
)

type jsonReport struct {
	Feasible  bool                    `json:"feasible"`
	Reference string                  `json:"reference"`
	Intervals map[string]jsonInterval `json:"intervals,omitempty"`
	MinSlack  *int64                  `json:"min_slack_ns,omitempty"`
	Corrected map[string]int64        `json:"corrected_ts_ns,omitempty"`
	Chains    []jsonChain             `json:"chains,omitempty"`
}

type jsonInterval struct {
	LoNS   int64 `json:"lo_ns"`
	HiNS   int64 `json:"hi_ns"`
	LoOpen bool  `json:"lo_open"`
	HiOpen bool  `json:"hi_open"`
}

type jsonChain struct {
	SumNS int64           `json:"sum_ns"`
	Edges []jsonChainEdge `json:"edges"`
}

type jsonChainEdge struct {
	From     string `json:"from_node"`
	To       string `json:"to_node"`
	FromTSNS int64  `json:"from_ts_ns"`
	ToTSNS   int64  `json:"to_ts_ns"`
	WeightNS int64  `json:"weight_ns"`
}

func main() {
	wideFlag := flag.Duration("wide", time.Duration(skew.DefaultWide), "absolute bound for unconstrained offsets")
	jsonFlag := flag.Bool("json", false, "emit a machine-readable JSON report")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: skewcheck [-json] [-wide 87600h] fixture.json\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	spec, err := fixture.Load(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	wide := spec.WideNS()
	if wide == 0 {
		wide = int64(*wideFlag)
	}
	res, err := spec.Graph().Analyze(spec.Reference, skew.Options{Wide: wide})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if *jsonFlag {
		emitJSON(spec, res)
	} else {
		emitText(spec, res)
	}
	if !res.Feasible {
		os.Exit(1)
	}
}

func emitJSON(spec *fixture.Spec, res *skew.Result) {
	rpt := jsonReport{Feasible: res.Feasible, Reference: res.Reference}
	if res.Feasible {
		rpt.Intervals = make(map[string]jsonInterval, len(res.Intervals))
		for n, iv := range res.Intervals {
			rpt.Intervals[n] = jsonInterval{LoNS: iv.Lo, HiNS: iv.Hi, LoOpen: iv.LoOpen, HiOpen: iv.HiOpen}
		}
		ms := res.MinSlack
		rpt.MinSlack = &ms
		rpt.Corrected = make(map[string]int64)
		for _, e := range spec.Events {
			rpt.Corrected[e.ID] = res.CorrectedTS(skew.Event{ID: e.ID, Node: e.Node, TS: e.TS.TS})
		}
	}
	for _, c := range res.Chains {
		jc := jsonChain{SumNS: c.Sum}
		for _, ce := range c.Edges {
			jc.Edges = append(jc.Edges, jsonChainEdge{
				From: ce.From, To: ce.To,
				FromTSNS: ce.FromTS, ToTSNS: ce.ToTS, WeightNS: ce.Weight,
			})
		}
		rpt.Chains = append(rpt.Chains, jc)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rpt)
}

func emitText(spec *fixture.Spec, res *skew.Result) {
	fmt.Printf("reference node: %s\n\n", res.Reference)
	if !res.Feasible {
		fmt.Printf("INFEASIBLE: %d contradiction chain(s)\n\n", len(res.Chains))
		for i, c := range res.Chains {
			fmt.Printf("chain %d: total signed duration %s (negative => contradiction)\n", i+1, fmtNS(c.Sum))
			for _, ce := range c.Edges {
				fmt.Printf("  %s@%d -> %s@%d   weight = %s\n",
					ce.From, ce.FromTS, ce.To, ce.ToTS, fmtNS(ce.Weight))
			}
			fmt.Println()
		}
		return
	}

	fmt.Println("feasible clock offset intervals (relative to reference):")
	nodeSeen := map[string]bool{}
	var nodes []string
	for _, e := range spec.Events {
		if !nodeSeen[e.Node] {
			nodeSeen[e.Node] = true
			nodes = append(nodes, e.Node)
		}
	}
	for _, n := range nodes {
		fmt.Printf("  %-8s %s\n", n, fmtInterval(res.Intervals[n]))
	}
	fmt.Printf("\nminimum corrected causal duration (slack): %s\n", fmtNS(res.MinSlack))
	fmt.Println("corrected timestamps (observed local ts + assigned offset):")
	for _, e := range spec.Events {
		ev := skew.Event{ID: e.ID, Node: e.Node, TS: e.TS.TS}
		fmt.Printf("  %-4s %s@%d -> %d\n", e.ID, e.Node, e.TS.TS, res.CorrectedTS(ev))
	}
}

func fmtInterval(iv skew.Interval) string {
	lo, hi := fmtNS(iv.Lo), fmtNS(iv.Hi)
	if iv.LoOpen {
		lo = "-infty"
	}
	if iv.HiOpen {
		hi = "+infty"
	}
	return fmt.Sprintf("[%s, %s] (ns: [%d, %d])", lo, hi, iv.Lo, iv.Hi)
}

func fmtNS(v int64) string {
	return time.Duration(v).String()
}
