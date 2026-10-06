package mcf

import (
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func effectiveGraph(inst StagedInstance) Graph {
	g := Graph{N: inst.N}
	for _, e := range inst.Edges {
		g.Edges = append(g.Edges, Edge{From: e.From, To: e.To, Lo: e.effLo(), Cap: e.effCap(), Cost: e.Cost})
	}
	return g
}

// randomStaged builds a staged instance whose supplies come from a
// concrete flow respecting executed commitments, hence feasible.
func randomStaged(r *rand.Rand) StagedInstance {
	n := 2 + r.Intn(4) // 2..5 nodes
	m := 1 + r.Intn(8) // 1..8 edges
	inst := StagedInstance{N: n, Supply: make([]int, n)}
	for i := 0; i < m; i++ {
		from := r.Intn(n)
		to := r.Intn(n)
		for to == from {
			to = r.Intn(n)
		}
		cap := 1 + r.Intn(3)
		executed := r.Intn(cap + 1)
		e := StagedEdge{
			Edge:     Edge{From: from, To: to, Cap: cap, Cost: r.Intn(13) - 4},
			Executed: executed,
			Canceled: r.Intn(4) == 0,
		}
		inst.Edges = append(inst.Edges, e)
		f := e.effLo()
		if e.effCap() > f {
			f += r.Intn(e.effCap() - f + 1)
		}
		inst.Supply[from] += f
		inst.Supply[to] -= f
	}
	return inst
}

func TestStagedMatchesExact(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 300; trial++ {
		inst := randomStaged(r)
		sr := SolveStaged(inst)
		if sr.Status != Optimal {
			t.Fatalf("trial %d: status %v, want optimal (feasible by construction)", trial, sr.Status)
		}
		// Commitments: never reduce executed flow; canceled edges frozen.
		for i, e := range inst.Edges {
			if sr.Flow[i] < e.Executed {
				t.Fatalf("trial %d: edge %d flow %d < executed %d", trial, i, sr.Flow[i], e.Executed)
			}
			if e.Canceled && sr.Flow[i] != e.Executed {
				t.Fatalf("trial %d: canceled edge %d flow %d != executed %d", trial, i, sr.Flow[i], e.Executed)
			}
		}
		exact := Solve(effectiveGraph(inst), inst.Supply)
		if exact.Status != Optimal || exact.Cost != sr.TotalCost {
			t.Fatalf("trial %d: staged cost %d, exact %v/%d", trial, sr.TotalCost, exact.Status, exact.Cost)
		}
		executedCost := 0
		for _, e := range inst.Edges {
			executedCost += e.Cost * e.Executed
		}
		if sr.IncrementalCost != sr.TotalCost-executedCost {
			t.Fatalf("trial %d: incremental %d != total %d - executed %d",
				trial, sr.IncrementalCost, sr.TotalCost, executedCost)
		}
	}
}

func TestStagedBudgetBoundsAndResume(t *testing.T) {
	dir, err := ioutil.TempDir("", "mcf-frontier")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	frontier := filepath.Join(dir, "frontier.json")

	r := rand.New(rand.NewSource(99))
	var inst StagedInstance
	for {
		inst = randomStaged(r)
		states := int64(1)
		for _, e := range inst.Edges {
			states *= int64(e.effCap() - e.effLo() + 1)
		}
		if states > 2000 && states < 200000 {
			break
		}
	}
	full := SolveStaged(inst)
	if full.Status != Optimal {
		t.Fatalf("full run status %v", full.Status)
	}

	limited := inst
	limited.Budget = 500
	limited.FrontierPath = frontier
	res := SolveStaged(limited)
	if res.Status != BudgetExhausted {
		t.Fatalf("status %v, want budget_exhausted", res.Status)
	}
	if res.Flow != nil {
		t.Fatalf("budget-exhausted run must not claim an optimal flow")
	}
	if res.Lower > full.TotalCost {
		t.Fatalf("lower bound %d exceeds true optimum %d", res.Lower, full.TotalCost)
	}
	if res.HasIncumbent && res.Upper < full.TotalCost {
		t.Fatalf("upper bound %d below true optimum %d", res.Upper, full.TotalCost)
	}
	if !res.FrontierSaved {
		t.Fatalf("frontier not saved")
	}
	if _, err := os.Stat(frontier); err != nil {
		t.Fatalf("frontier file missing: %v", err)
	}

	resumed := inst
	resumed.Budget = 0 // unlimited
	resumed.FrontierPath = frontier
	resumed.Resume = true
	res2 := SolveStaged(resumed)
	if res2.Status != Optimal {
		t.Fatalf("resumed status %v", res2.Status)
	}
	if res2.TotalCost != full.TotalCost {
		t.Fatalf("resumed cost %d != one-shot cost %d", res2.TotalCost, full.TotalCost)
	}
}

func TestStagedInfeasibleCut(t *testing.T) {
	// Node 2 needs 5 units but the only incoming edge is canceled
	// after 2 executed units: cut evidence must expose the deficit.
	inst := StagedInstance{
		N:      3,
		Supply: []int{5, 0, -5},
		Edges: []StagedEdge{
			{Edge: Edge{From: 0, To: 1, Cap: 5, Cost: 1}},
			{Edge: Edge{From: 1, To: 2, Cap: 4, Cost: 1}, Executed: 2, Canceled: true},
		},
	}
	res := SolveStaged(inst)
	if res.Status != Infeasible {
		t.Fatalf("status %v, want infeasible", res.Status)
	}
	if res.Cut == nil {
		t.Fatalf("missing cut evidence")
	}
	if res.Cut.Deficit <= 0 {
		t.Fatalf("cut deficit %d, want > 0", res.Cut.Deficit)
	}
	if res.Cut.Deficit != res.Cut.NetDemand-res.Cut.CapacityIn {
		t.Fatalf("inconsistent cut: %+v", res.Cut)
	}
	// The cut must isolate node 2 (the starved sink).
	found := false
	for _, v := range res.Cut.Nodes {
		if v == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("cut nodes %v do not contain sink 2", res.Cut.Nodes)
	}
}
