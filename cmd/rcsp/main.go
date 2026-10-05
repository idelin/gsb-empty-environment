// Command rcsp reads a graph and query as JSON and prints the
// minimum-cost risk-constrained path together with its risk ledger.
//
// Input (stdin or -in file):
//
//	{
//	  "num_nodes": 4,
//	  "edges": [{"id":1,"from":0,"to":1,"cost":2,"risk":1}, ...],
//	  "source": 0, "target": 3, "budget": 5
//	}
//
// With -verify the answer is recomputed with the independent
// (node, risk) state-expansion solver and the run fails if the two
// disagree (only practical for small graphs).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"

	"rcsp"
)

type query struct {
	NumNodes int         `json:"num_nodes"`
	Edges    []rcsp.Edge `json:"edges"`
	Source   int         `json:"source"`
	Target   int         `json:"target"`
	Budget   int         `json:"budget"`
}

type ledgerEntry struct {
	EdgeID  int `json:"edge_id"`
	From    int `json:"from"`
	To      int `json:"to"`
	Cost    int `json:"cost"`
	Risk    int `json:"risk"`
	CumCost int `json:"cum_cost"`
	CumRisk int `json:"cum_risk"`
}

type answer struct {
	Found     bool          `json:"found"`
	EdgeIDs   []int         `json:"edge_ids,omitempty"`
	Nodes     []int         `json:"nodes,omitempty"`
	Hops      int           `json:"hops"`
	TotalCost int           `json:"total_cost"`
	TotalRisk int           `json:"total_risk"`
	Ledger    []ledgerEntry `json:"ledger,omitempty"`
}

func toAnswer(r rcsp.Result) answer {
	a := answer{
		Found:     r.Found,
		EdgeIDs:   r.EdgeIDs,
		Nodes:     r.Nodes,
		Hops:      r.Hops,
		TotalCost: r.TotalCost,
		TotalRisk: r.TotalRisk,
	}
	for _, e := range r.Ledger {
		a.Ledger = append(a.Ledger, ledgerEntry{
			EdgeID: e.EdgeID, From: e.From, To: e.To,
			Cost: e.Cost, Risk: e.Risk, CumCost: e.CumCost, CumRisk: e.CumRisk,
		})
	}
	return a
}

func main() {
	in := flag.String("in", "", "input JSON file (default: stdin)")
	verify := flag.Bool("verify", false, "cross-check with the (node,risk) state-expansion solver")
	flag.Parse()

	var data []byte
	var err error
	if *in == "" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(*in)
	}
	if err != nil {
		fatal("read input: %v", err)
	}
	var q query
	if err := json.Unmarshal(data, &q); err != nil {
		fatal("parse input: %v", err)
	}
	g, err := rcsp.NewGraph(q.NumNodes, q.Edges)
	if err != nil {
		fatal("invalid graph: %v", err)
	}
	res, err := rcsp.Solve(g, q.Source, q.Target, q.Budget)
	if err != nil {
		fatal("solve: %v", err)
	}
	if *verify {
		chk, err := rcsp.SolveByStateExpansion(g, q.Source, q.Target, q.Budget)
		if err != nil {
			fatal("verify: %v", err)
		}
		if chk.Found != res.Found || chk.TotalCost != res.TotalCost ||
			chk.TotalRisk != res.TotalRisk || chk.Hops != res.Hops ||
			!reflect.DeepEqual(chk.EdgeIDs, res.EdgeIDs) {
			fatal("verification mismatch: label solver=%+v state expansion=%+v", res, chk)
		}
	}
	out, err := json.MarshalIndent(toAnswer(res), "", "  ")
	if err != nil {
		fatal("encode: %v", err)
	}
	fmt.Println(string(out))
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "rcsp: "+format+"\n", args...)
	os.Exit(1)
}
