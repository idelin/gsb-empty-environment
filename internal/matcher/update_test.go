package matcher

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestDeleteMatchedEdge(t *testing.T) {
	inst := &Instance{
		Tasks:     []string{"a", "b"},
		Employees: []string{"x", "y"},
		Edges: []Edge{
			{"a", "x", 5}, {"a", "y", 1}, {"b", "y", 4},
		},
	}
	res := checkAgainstBrute(t, inst)
	if res.Cardinality != 2 || res.TotalProfit != 9 {
		t.Fatalf("before delete: got (%d,%d), want (2,9)", res.Cardinality, res.TotalProfit)
	}
	// Delete an edge that is currently in the matching.
	if err := RemoveEdge(inst, "a", "x"); err != nil {
		t.Fatal(err)
	}
	res = checkAgainstBrute(t, inst)
	if res.Cardinality != 1 || res.TotalProfit != 4 {
		t.Fatalf("after delete: got (%d,%d), want (1,4)", res.Cardinality, res.TotalProfit)
	}
	if !reflect.DeepEqual(res.Assignment, []string{"", "y"}) {
		t.Fatalf("after delete: assignment %v", res.Assignment)
	}
}

func TestUpdateConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 200; trial++ {
		inst := randInstance(rng, 4, 4)
		if len(inst.Tasks) == 0 || len(inst.Employees) == 0 {
			continue
		}
		switch rng.Intn(4) {
		case 0: // add a brand-new edge
			ti := rng.Intn(len(inst.Tasks))
			ej := rng.Intn(len(inst.Employees))
			if err := AddEdge(inst, Edge{
				Task: inst.Tasks[ti], Employee: inst.Employees[ej],
				Profit: int64(rng.Intn(21) - 10),
			}); err != nil {
				t.Fatal(err)
			}
		case 1: // re-price an existing edge
			if len(inst.Edges) == 0 {
				continue
			}
			e := inst.Edges[rng.Intn(len(inst.Edges))]
			e.Profit = int64(rng.Intn(21) - 10)
			if err := AddEdge(inst, e); err != nil {
				t.Fatal(err)
			}
		case 2: // remove an existing edge
			if len(inst.Edges) == 0 {
				continue
			}
			e := inst.Edges[rng.Intn(len(inst.Edges))]
			if err := RemoveEdge(inst, e.Task, e.Employee); err != nil {
				t.Fatal(err)
			}
		case 3: // remove an edge that is currently matched, if any
			p := mustProblem(t, inst)
			res := p.solve()
			if len(res.Matches) == 0 {
				continue
			}
			m := res.Matches[rng.Intn(len(res.Matches))]
			if err := RemoveEdge(inst, m.Task, m.Employee); err != nil {
				t.Fatal(err)
			}
		}
		// The updated instance must agree with the exhaustive reference.
		checkAgainstBrute(t, inst)
	}
}

func TestUpdateEdgeSemantics(t *testing.T) {
	inst := &Instance{
		Tasks:     []string{"a"},
		Employees: []string{"x"},
		Edges:     []Edge{{"a", "x", 1}},
	}
	// Re-add identical: no-op.
	if err := AddEdge(inst, Edge{"a", "x", 1}); err != nil {
		t.Fatal(err)
	}
	if len(inst.Edges) != 1 {
		t.Fatalf("identical re-add duplicated the edge: %+v", inst.Edges)
	}
	// Re-add with a new profit: replace.
	if err := AddEdge(inst, Edge{"a", "x", 9}); err != nil {
		t.Fatal(err)
	}
	if len(inst.Edges) != 1 || inst.Edges[0].Profit != 9 {
		t.Fatalf("re-price failed: %+v", inst.Edges)
	}
	// Unknown endpoints are rejected.
	if err := AddEdge(inst, Edge{"b", "x", 1}); err == nil {
		t.Fatal("expected error for unknown task")
	}
	if err := AddEdge(inst, Edge{"a", "y", 1}); err == nil {
		t.Fatal("expected error for unknown employee")
	}
	// Removing a missing edge is an error.
	if err := RemoveEdge(inst, "a", "y"); err == nil {
		t.Fatal("expected error for missing edge")
	}
	if err := RemoveEdge(inst, "a", "x"); err != nil {
		t.Fatal(err)
	}
	if len(inst.Edges) != 0 {
		t.Fatalf("edge not removed: %+v", inst.Edges)
	}
}
