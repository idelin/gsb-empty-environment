package matcher

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustProblem(t *testing.T, inst *Instance) *problem {
	t.Helper()
	p, err := Validate(inst)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return p
}

// matchStrings converts an internal match array to ID strings.
func (p *problem) matchStrings(match []int) []string {
	out := make([]string, len(match))
	for i, j := range match {
		if j < len(p.emps) {
			out[i] = p.emps[j]
		}
	}
	return out
}

// checkAgainstBrute solves inst and compares cardinality, profit and
// the full assignment against the exhaustive reference; it also runs
// the independent verifier on the result.
func checkAgainstBrute(t *testing.T, inst *Instance) *Result {
	t.Helper()
	p := mustProblem(t, inst)
	res := p.solve()
	bc, bp, bm := p.bruteForce()
	if res.Cardinality != bc || res.TotalProfit != bp {
		t.Fatalf("inst %+v: solver got (%d,%d), brute force got (%d,%d)",
			inst, res.Cardinality, res.TotalProfit, bc, bp)
	}
	if got, want := res.Assignment, p.matchStrings(bm); !reflect.DeepEqual(got, want) {
		t.Fatalf("inst %+v: solver assignment %v, brute force %v", inst, got, want)
	}
	if _, err := p.verify(res); err != nil {
		t.Fatalf("inst %+v: verify rejected solver output: %v", inst, err)
	}
	return res
}

func TestCrafted(t *testing.T) {
	cases := []struct {
		name       string
		inst       *Instance
		card       int
		profit     int64
		assignment []string
	}{
		{
			name: "isolated vertices",
			inst: &Instance{
				Tasks:     []string{"a", "b", "c"},
				Employees: []string{"x", "y"},
				Edges:     []Edge{{"a", "x", 1}},
			},
			card:       1,
			profit:     1,
			assignment: []string{"x", "", ""},
		},
		{
			name: "all negative profits still maximize cardinality",
			inst: &Instance{
				Tasks:     []string{"a", "b"},
				Employees: []string{"x", "y"},
				Edges: []Edge{
					{"a", "x", -3}, {"a", "y", -1},
					{"b", "x", -2}, {"b", "y", -4},
				},
			},
			card:       2,
			profit:     -3, // a-y(-1) + b-x(-2)
			assignment: []string{"y", "x"},
		},
		{
			name: "negative edge forced by cardinality",
			inst: &Instance{
				Tasks:     []string{"a", "b"},
				Employees: []string{"x", "y"},
				Edges:     []Edge{{"a", "x", -5}, {"b", "y", 2}},
			},
			card:       2,
			profit:     -3,
			assignment: []string{"x", "y"},
		},
		{
			name: "negative edge avoided when cardinality allows",
			inst: &Instance{
				Tasks:     []string{"a", "b"},
				Employees: []string{"x"},
				Edges:     []Edge{{"a", "x", -5}, {"b", "x", -2}},
			},
			card:       1,
			profit:     -2,
			assignment: []string{"", "x"},
		},
		{
			name: "multiple equivalent optima pick lexicographically smallest",
			inst: &Instance{
				Tasks:     []string{"a", "b"},
				Employees: []string{"x", "y"},
				Edges: []Edge{
					{"a", "x", 0}, {"a", "y", 0},
					{"b", "x", 0}, {"b", "y", 0},
				},
			},
			card:       2,
			profit:     0,
			assignment: []string{"x", "y"},
		},
		{
			name: "unassigned sentinel is the last resort",
			inst: &Instance{
				Tasks:     []string{"a", "b"},
				Employees: []string{"z"},
				Edges:     []Edge{{"a", "z", 0}, {"b", "z", 0}},
			},
			card:       1,
			profit:     0,
			assignment: []string{"z", ""}, // "z" beats the sentinel at task a
		},
		{
			name: "identical duplicate edges are merged",
			inst: &Instance{
				Tasks:     []string{"a"},
				Employees: []string{"x"},
				Edges:     []Edge{{"a", "x", 7}, {"a", "x", 7}},
			},
			card:       1,
			profit:     7,
			assignment: []string{"x"},
		},
		{
			name:       "empty instance",
			inst:       &Instance{},
			card:       0,
			profit:     0,
			assignment: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := checkAgainstBrute(t, tc.inst)
			if res.Cardinality != tc.card || res.TotalProfit != tc.profit {
				t.Fatalf("got (%d,%d), want (%d,%d)",
					res.Cardinality, res.TotalProfit, tc.card, tc.profit)
			}
			if !reflect.DeepEqual(res.Assignment, tc.assignment) {
				t.Fatalf("assignment %v, want %v", res.Assignment, tc.assignment)
			}
		})
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		inst *Instance
	}{
		{"conflicting duplicate edges", &Instance{
			Tasks: []string{"a"}, Employees: []string{"x"},
			Edges: []Edge{{"a", "x", 1}, {"a", "x", 2}},
		}},
		{"unknown task", &Instance{
			Tasks: []string{"a"}, Employees: []string{"x"},
			Edges: []Edge{{"b", "x", 1}},
		}},
		{"unknown employee", &Instance{
			Tasks: []string{"a"}, Employees: []string{"x"},
			Edges: []Edge{{"a", "y", 1}},
		}},
		{"duplicate task ID", &Instance{Tasks: []string{"a", "a"}}},
		{"duplicate employee ID", &Instance{Employees: []string{"x", "x"}}},
		{"empty task ID", &Instance{Tasks: []string{""}}},
		{"empty employee ID", &Instance{Employees: []string{""}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Validate(tc.inst); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func randInstance(rng *rand.Rand, maxT, maxE int) *Instance {
	T := rng.Intn(maxT + 1)
	E := rng.Intn(maxE + 1)
	inst := &Instance{}
	for i := 0; i < T; i++ {
		inst.Tasks = append(inst.Tasks, "t"+string(rune('0'+i)))
	}
	for j := 0; j < E; j++ {
		inst.Employees = append(inst.Employees, "e"+string(rune('0'+j)))
	}
	prob := []float64{0.2, 0.5, 0.8}[rng.Intn(3)]
	for i := 0; i < T; i++ {
		for j := 0; j < E; j++ {
			if rng.Float64() < prob {
				inst.Edges = append(inst.Edges, Edge{
					Task:     inst.Tasks[i],
					Employee: inst.Employees[j],
					Profit:   int64(rng.Intn(19) - 9),
				})
			}
		}
	}
	return inst
}

func TestRandomVsBrute(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 400; trial++ {
		inst := randInstance(rng, 4, 4)
		checkAgainstBrute(t, inst)
	}
}

func TestRandom7x7VsBrute(t *testing.T) {
	for seed := int64(10); seed < 13; seed++ {
		rng := rand.New(rand.NewSource(seed))
		inst := &Instance{}
		for i := 0; i < 7; i++ {
			inst.Tasks = append(inst.Tasks, "t"+string(rune('0'+i)))
			inst.Employees = append(inst.Employees, "e"+string(rune('0'+i)))
		}
		for i := 0; i < 7; i++ {
			for j := 0; j < 7; j++ {
				if rng.Float64() < 0.7 {
					inst.Edges = append(inst.Edges, Edge{
						Task:     inst.Tasks[i],
						Employee: inst.Employees[j],
						Profit:   int64(rng.Intn(41) - 20),
					})
				}
			}
		}
		checkAgainstBrute(t, inst)
	}
}

func TestDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		inst := randInstance(rng, 4, 4)
		p := mustProblem(t, inst)
		if a, b := p.solve(), p.solve(); !reflect.DeepEqual(a, b) {
			t.Fatalf("non-deterministic result: %+v vs %+v", a, b)
		}
	}
}
