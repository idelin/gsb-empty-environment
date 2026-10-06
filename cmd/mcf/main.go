// Command mcf solves small integer min-cost flow problems with phased
// flow commitments. It reads one JSON document from stdin (or from the
// file named by -in) and writes a JSON result to stdout.
//
// Input schema:
//   {
//     "num_nodes": 4, "source": 0, "sink": 3, "demand": 5,
//     "edges": [{"from":0,"to":1,"cap":4,"cost":2,"executed":1,"cancelled":false}],
//     "verify": true,
//     "budget": {"max_nodes": 100000, "max_millis": 1000},
//     "frontier_in": "frontier.json", "frontier_out": "frontier.json"
//   }
//
// The exact solver (SSP + negative-cycle cancelling) always runs. The
// branch-and-bound search runs when "verify" or a budget/frontier is
// given; under a budget it may stop early, saving a resumable frontier
// and reporting [lower_bound, upper_bound] instead of an optimum.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"mcf/flow"
)

type edgeJSON struct {
	From      int   `json:"from"`
	To        int   `json:"to"`
	Cap       int64 `json:"cap"`
	Cost      int64 `json:"cost"`
	Executed  int64 `json:"executed"`
	Cancelled bool  `json:"cancelled"`
}

type inputJSON struct {
	NumNodes int        `json:"num_nodes"`
	Source   int        `json:"source"`
	Sink     int        `json:"sink"`
	Demand   int64      `json:"demand"`
	Edges    []edgeJSON `json:"edges"`

	Verify      bool         `json:"verify"`
	Budget      *flow.Budget `json:"budget"`
	FrontierIn  string       `json:"frontier_in"`
	FrontierOut string       `json:"frontier_out"`
}

type flowJSON struct {
	Edge     int   `json:"edge"`
	From     int   `json:"from"`
	To       int   `json:"to"`
	Flow     int64 `json:"flow"`
	Executed int64 `json:"executed"`
	NewFlow  int64 `json:"new_flow"` // flow beyond the executed commitment
	UnitCost int64 `json:"unit_cost"`
	Cost     int64 `json:"cost"`
}

type searchJSON struct {
	Status        string  `json:"status"`
	LowerBound    int64   `json:"lower_bound"`
	UpperBound    *int64  `json:"upper_bound"`
	NodesExplored int64   `json:"nodes_explored"`
	Flows         []int64 `json:"flows,omitempty"`
	FrontierSaved string  `json:"frontier_saved_to,omitempty"`
}

type verifyJSON struct {
	Performed bool   `json:"performed"`
	Match     bool   `json:"match"`
	Detail    string `json:"detail"`
}

type outputJSON struct {
	Status    string            `json:"status"` // optimal | infeasible | budget_exceeded | error
	Error     string            `json:"error,omitempty"`
	Flows     []flowJSON        `json:"flows,omitempty"`
	TotalCost int64             `json:"total_cost,omitempty"`
	Cut       *flow.CutEvidence `json:"cut,omitempty"`
	Search    *searchJSON       `json:"search,omitempty"`
	Verify    *verifyJSON       `json:"verification,omitempty"`
}

func fail(err error) {
	json.NewEncoder(os.Stdout).Encode(outputJSON{Status: "error", Error: err.Error()})
	os.Exit(1)
}

func main() {
	inFile := flag.String("in", "", "input JSON file (default: stdin)")
	flag.Parse()

	var raw []byte
	var err error
	if *inFile != "" {
		raw, err = ioutil.ReadFile(*inFile)
	} else {
		raw, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fail(err)
	}
	var in inputJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		fail(fmt.Errorf("bad input JSON: %v", err))
	}

	edges := make([]flow.Edge, len(in.Edges))
	for i, e := range in.Edges {
		edges[i] = flow.Edge{From: e.From, To: e.To, Cap: e.Cap, Cost: e.Cost, Lower: e.Executed, Cancelled: e.Cancelled}
	}
	p := flow.NewTransportation(in.NumNodes, in.Source, in.Sink, in.Demand, edges)

	sol, err := flow.Solve(p)
	if err != nil {
		fail(err)
	}

	out := outputJSON{}
	if !sol.Feasible {
		out.Status = "infeasible"
		out.Cut = sol.Cut
	} else {
		out.Status = "optimal"
		out.TotalCost = sol.Cost
		for i, e := range in.Edges {
			out.Flows = append(out.Flows, flowJSON{
				Edge: i, From: e.From, To: e.To,
				Flow: sol.Flows[i], Executed: e.Executed,
				NewFlow:  sol.Flows[i] - e.Executed,
				UnitCost: e.Cost, Cost: sol.Flows[i] * e.Cost,
			})
		}
	}

	// Branch-and-bound search: verification, budget, frontier.
	runSearch := in.Verify || in.Budget != nil || in.FrontierIn != "" || in.FrontierOut != ""
	if runSearch {
		var frontier *flow.Frontier
		if in.FrontierIn != "" {
			raw, err := ioutil.ReadFile(in.FrontierIn)
			if err != nil {
				fail(fmt.Errorf("read frontier: %v", err))
			}
			frontier = &flow.Frontier{}
			if err := json.Unmarshal(raw, frontier); err != nil {
				fail(fmt.Errorf("bad frontier JSON: %v", err))
			}
		}
		var budget flow.Budget
		if in.Budget != nil {
			budget = *in.Budget
		}
		res, err := flow.ResumeSearch(p, frontier, budget)
		if err != nil {
			fail(err)
		}
		sj := &searchJSON{
			Status: res.Status, LowerBound: res.LowerBound,
			NodesExplored: res.NodesExplored, Flows: res.Flows,
		}
		if res.HasUpperBound {
			ub := res.UpperBound
			sj.UpperBound = &ub
		}
		if res.Status == "budget_exceeded" {
			out.Status = "budget_exceeded"
			if in.FrontierOut != "" {
				raw, _ := json.MarshalIndent(res.Frontier, "", "  ")
				if err := ioutil.WriteFile(in.FrontierOut, raw, 0644); err != nil {
					fail(fmt.Errorf("save frontier: %v", err))
				}
				sj.FrontierSaved = in.FrontierOut
			}
		}
		out.Search = sj
		if res.Status == "optimal" || res.Status == "infeasible" {
			match := (res.Status == "optimal") == sol.Feasible
			detail := "feasibility agrees"
			if match && sol.Feasible {
				match = res.Cost == sol.Cost
				detail = fmt.Sprintf("exhaustive cost %d vs solver cost %d", res.Cost, sol.Cost)
			}
			out.Verify = &verifyJSON{Performed: true, Match: match, Detail: detail}
			if !match {
				out.Status = "error"
				out.Error = "exhaustive verification disagrees with solver: " + detail
			}
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
}
