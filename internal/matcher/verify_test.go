package matcher

import (
	"strings"
	"testing"
)

func tamperBase(t *testing.T) (*problem, *Result) {
	t.Helper()
	inst := &Instance{
		Tasks:     []string{"a", "b"},
		Employees: []string{"x", "y"},
		Edges: []Edge{
			{"a", "x", 5}, {"a", "y", 1},
			{"b", "x", 3}, {"b", "y", 4},
		},
	}
	p := mustProblem(t, inst)
	res := p.solve()
	if _, err := p.verify(res); err != nil {
		t.Fatalf("solver output failed verification: %v", err)
	}
	return p, res
}

func clone(res *Result) *Result {
	c := *res
	c.Assignment = append([]string(nil), res.Assignment...)
	c.Matches = append([]Edge(nil), res.Matches...)
	c.CoverTasks = append([]string(nil), res.CoverTasks...)
	c.CoverEmps = append([]string(nil), res.CoverEmps...)
	c.Duals.Tasks = append([]int64(nil), res.Duals.Tasks...)
	c.Duals.Employees = append([]int64(nil), res.Duals.Employees...)
	return &c
}

func TestVerifyRejectsTampering(t *testing.T) {
	p, good := tamperBase(t)
	cases := []struct {
		name   string
		mutate func(*Result)
		want   string
	}{
		{"wrong cardinality", func(r *Result) { r.Cardinality++ }, "cardinality"},
		{"wrong profit", func(r *Result) { r.TotalProfit++ }, "profit"},
		{"unknown employee", func(r *Result) { r.Assignment[0] = "q" }, "unknown employee"},
		{"non-edge assignment", func(r *Result) {
			r.Assignment[0] = ""
			r.Assignment[1] = "y"
			r.Matches = r.Matches[1:]
			r.Cardinality = 1
			r.TotalProfit = 4
		}, "vertex cover"}, // cardinality 1 is infeasible; cover size 2 != 1
		{"shrunk cover", func(r *Result) { r.CoverTasks = nil; r.CoverEmps = nil }, "vertex cover"},
		{"corrupted duals", func(r *Result) { r.Duals.Tasks[0] += 1 << 40 }, "dual violation"},
		{"match contradicts assignment", func(r *Result) {
			r.Matches[0].Profit++
		}, "profit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := clone(good)
			tc.mutate(res)
			_, err := p.verify(res)
			if err == nil {
				t.Fatal("tampered result passed verification")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestVerifyJSONRoundTrip(t *testing.T) {
	inst := &Instance{
		Tasks:     []string{"a", "b"},
		Employees: []string{"x", "y"},
		Edges: []Edge{
			{"a", "x", 5}, {"a", "y", 1},
			{"b", "x", 3}, {"b", "y", 4},
		},
	}
	res, err := SolveInstance(inst)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ToJSON(res).ToResult()
	if err != nil {
		t.Fatal(err)
	}
	checks, err := VerifyInstance(inst, back)
	if err != nil {
		t.Fatalf("round-tripped result failed verification: %v", err)
	}
	joined := strings.Join(checks, ",")
	for _, want := range []string{"structure", "min-vertex-cover", "dual-potentials", "exhaustive"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing check %q in %v", want, checks)
		}
	}
}
