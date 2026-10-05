// Package matcher solves exact weighted bipartite matching with a
// lexicographic objective order:
//
//  1. maximize the number of matched tasks (cardinality),
//  2. among those, maximize the total (signed) profit,
//  3. among those, take the lexicographically smallest employee ID list
//     in ascending task-ID order, where an unassigned task counts as a
//     sentinel greater than every employee ID.
//
// Negative-profit edges are never dropped: objective 1 outranks profit.
//
// The package only uses the Go standard library.
package matcher

import (
	"fmt"
	"sort"
)

// Edge is an allowed task/employee pair with a signed integer profit.
type Edge struct {
	Task     string `json:"task"`
	Employee string `json:"employee"`
	Profit   int64  `json:"profit"`
}

// Instance is the JSON-decodable problem input.
type Instance struct {
	Tasks     []string `json:"tasks"`
	Employees []string `json:"employees"`
	Edges     []Edge   `json:"edges"`
}

// problem is the validated, canonically ordered internal form.
// Employee indices are ordered by ID, so the unassigned sentinel
// value len(emps) is greater than every employee index and a plain
// integer comparison implements the required lexicographic order.
type problem struct {
	tasks  []string         // sorted ascending
	emps   []string         // sorted ascending
	adj    [][]int          // adj[t] = employee indices, ascending
	profit map[[2]int]int64 // profit per allowed (task, employee)
}

// Validate checks the instance and returns the normalized problem.
// Identical duplicate edges are merged; conflicting duplicates
// (same pair, different profit) are rejected.
func Validate(inst *Instance) (*problem, error) {
	seen := map[string]bool{}
	for _, id := range inst.Tasks {
		if id == "" {
			return nil, fmt.Errorf("empty task ID")
		}
		if seen["T:"+id] {
			return nil, fmt.Errorf("duplicate task ID %q", id)
		}
		seen["T:"+id] = true
	}
	for _, id := range inst.Employees {
		if id == "" {
			return nil, fmt.Errorf("empty employee ID")
		}
		if seen["E:"+id] {
			return nil, fmt.Errorf("duplicate employee ID %q", id)
		}
		seen["E:"+id] = true
	}
	tasks := append([]string(nil), inst.Tasks...)
	emps := append([]string(nil), inst.Employees...)
	sort.Strings(tasks)
	sort.Strings(emps)
	tidx := map[string]int{}
	for i, id := range tasks {
		tidx[id] = i
	}
	eidx := map[string]int{}
	for j, id := range emps {
		eidx[id] = j
	}
	profits := map[[2]int]int64{}
	for _, e := range inst.Edges {
		ti, ok := tidx[e.Task]
		if !ok {
			return nil, fmt.Errorf("edge references unknown task %q", e.Task)
		}
		ej, ok := eidx[e.Employee]
		if !ok {
			return nil, fmt.Errorf("edge references unknown employee %q", e.Employee)
		}
		key := [2]int{ti, ej}
		if old, dup := profits[key]; dup {
			if old != e.Profit {
				return nil, fmt.Errorf("conflicting duplicate edge %q/%q: profits %d and %d",
					e.Task, e.Employee, old, e.Profit)
			}
			continue
		}
		profits[key] = e.Profit
	}
	adj := make([][]int, len(tasks))
	for k := range profits {
		adj[k[0]] = append(adj[k[0]], k[1])
	}
	for i := range adj {
		sort.Ints(adj[i])
	}
	return &problem{tasks: tasks, emps: emps, adj: adj, profit: profits}, nil
}

// AddEdge inserts one edge into the instance. If the pair already
// exists its profit is replaced (an identical re-add is a no-op).
func AddEdge(inst *Instance, e Edge) error {
	if !contains(inst.Tasks, e.Task) {
		return fmt.Errorf("unknown task %q", e.Task)
	}
	if !contains(inst.Employees, e.Employee) {
		return fmt.Errorf("unknown employee %q", e.Employee)
	}
	for i := range inst.Edges {
		if inst.Edges[i].Task == e.Task && inst.Edges[i].Employee == e.Employee {
			inst.Edges[i].Profit = e.Profit
			return nil
		}
	}
	inst.Edges = append(inst.Edges, e)
	return nil
}

// RemoveEdge deletes exactly one edge; it is an error if absent.
func RemoveEdge(inst *Instance, task, emp string) error {
	for i := range inst.Edges {
		if inst.Edges[i].Task == task && inst.Edges[i].Employee == emp {
			inst.Edges = append(inst.Edges[:i], inst.Edges[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("no edge %q/%q to remove", task, emp)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
