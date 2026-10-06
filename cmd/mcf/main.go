// Command mcf solves a small min-cost-flow instance described as JSON
// on stdin (or a file argument) and prints a JSON report with the
// per-edge flow and total cost.
//
// Input schema:
//   {
//     "nodes": 4,
//     "edges": [{"from":0,"to":1,"lo":0,"cap":5,"cost":2,
//                "executed":1,"canceled":false}],
//     "supply": [3,0,0,-3],          // or "source","sink","value"
//     "verify": true,                // cross-check by enumeration
//     "staged": true,                // re-optimize around executed flow
//     "budget": 100000,              // staged enumeration budget
//     "frontier": "frontier.json",   // resumable frontier file
//     "resume": false
//   }
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"mcf"
)

type edgeJSON struct {
	From     int  `json:"from"`
	To       int  `json:"to"`
	Lo       int  `json:"lo"`
	Cap      int  `json:"cap"`
	Cost     int  `json:"cost"`
	Executed int  `json:"executed"`
	Canceled bool `json:"canceled"`
}

type instance struct {
	Nodes    int        `json:"nodes"`
	Edges    []edgeJSON `json:"edges"`
	Supply   []int      `json:"supply"`
	Source   *int       `json:"source"`
	Sink     *int       `json:"sink"`
	Value    *int       `json:"value"`
	Verify   bool       `json:"verify"`
	Staged   bool       `json:"staged"`
	Budget   int64      `json:"budget"`
	Frontier string     `json:"frontier"`
	Resume   bool       `json:"resume"`
}

type report struct {
	Status   string  `json:"status"`
	Flow     []int   `json:"flow,omitempty"`
	Cost     *int    `json:"cost,omitempty"`
	Verified *bool   `json:"verified,omitempty"`
	Staged   *staged `json:"staged,omitempty"`
}

type staged struct {
	Status          string   `json:"status"`
	Flow            []int    `json:"flow,omitempty"`
	TotalCost       *int     `json:"totalCost,omitempty"`
	IncrementalCost *int     `json:"incrementalCost,omitempty"`
	Lower           *int     `json:"lower,omitempty"`
	Upper           *int     `json:"upper,omitempty"`
	Cut             *cutJSON `json:"cut,omitempty"`
	Explored        int64    `json:"explored"`
	FrontierSaved   bool     `json:"frontierSaved"`
}

type cutJSON struct {
	Nodes      []int `json:"nodes"`
	NetDemand  int   `json:"netDemand"`
	CapacityIn int   `json:"capacityIn"`
	Deficit    int   `json:"deficit"`
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "mcf: "+format+"\n", args...)
	os.Exit(2)
}

func main() {
	var data []byte
	var err error
	if len(os.Args) > 1 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fail("reading input: %v", err)
	}
	var inst instance
	if err := json.Unmarshal(data, &inst); err != nil {
		fail("parsing input: %v", err)
	}
	if inst.Nodes < 1 || inst.Nodes > 8 {
		fail("nodes must be in [1,8], got %d", inst.Nodes)
	}
	if len(inst.Edges) > 12 {
		fail("at most 12 edges supported, got %d", len(inst.Edges))
	}
	supply := inst.Supply
	if supply == nil && inst.Source != nil && inst.Sink != nil && inst.Value != nil {
		supply = make([]int, inst.Nodes)
		supply[*inst.Source] = *inst.Value
		supply[*inst.Sink] = -*inst.Value
	}
	if len(supply) != inst.Nodes {
		fail("supply must have one entry per node")
	}

	g := mcf.Graph{N: inst.Nodes}
	for _, e := range inst.Edges {
		g.Edges = append(g.Edges, mcf.Edge{From: e.From, To: e.To, Lo: e.Lo, Cap: e.Cap, Cost: e.Cost})
	}

	rep := report{}
	res := mcf.Solve(g, supply)
	rep.Status = res.Status.String()
	if res.Status == mcf.Optimal {
		rep.Flow = res.Flow
		rep.Cost = &res.Cost
	}
	if inst.Verify {
		ex := mcf.Exhaustive(g, supply, 1<<28)
		ok := ex.Status == res.Status && (res.Status != mcf.Optimal || ex.Cost == res.Cost)
		rep.Verified = &ok
		if !ok {
			fmt.Fprintf(os.Stderr, "mcf: verification mismatch: solve=%v/%d exhaustive=%v/%d\n",
				res.Status, res.Cost, ex.Status, ex.Cost)
		}
	}

	if inst.Staged {
		si := mcf.StagedInstance{
			N:            inst.Nodes,
			Supply:       supply,
			Budget:       inst.Budget,
			FrontierPath: inst.Frontier,
			Resume:       inst.Resume,
		}
		for _, e := range inst.Edges {
			si.Edges = append(si.Edges, mcf.StagedEdge{
				Edge:     mcf.Edge{From: e.From, To: e.To, Lo: e.Lo, Cap: e.Cap, Cost: e.Cost},
				Executed: e.Executed,
				Canceled: e.Canceled,
			})
		}
		sr := mcf.SolveStaged(si)
		s := &staged{
			Status:        sr.Status.String(),
			Flow:          sr.Flow,
			Explored:      sr.Explored,
			FrontierSaved: sr.FrontierSaved,
		}
		if sr.Status == mcf.Optimal {
			s.TotalCost = &sr.TotalCost
			s.IncrementalCost = &sr.IncrementalCost
		}
		if sr.Status == mcf.BudgetExhausted {
			lo, hi := sr.Lower, sr.Upper
			s.Lower = &lo
			if sr.HasIncumbent {
				s.Upper = &hi
			}
		}
		if sr.Cut != nil {
			s.Cut = &cutJSON{
				Nodes:      sr.Cut.Nodes,
				NetDemand:  sr.Cut.NetDemand,
				CapacityIn: sr.Cut.CapacityIn,
				Deficit:    sr.Cut.Deficit,
			}
		}
		rep.Staged = s
	}

	out, err := json.MarshalIndent(&rep, "", "  ")
	if err != nil {
		fail("encoding report: %v", err)
	}
	fmt.Println(string(out))
}
