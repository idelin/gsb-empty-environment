package matcher

import "fmt"

// CoverJSON is the JSON form of the minimum vertex cover.
type CoverJSON struct {
	Tasks     []string `json:"tasks"`
	Employees []string `json:"employees"`
}

// DualsJSON is the JSON form of the dual potentials, keyed by ID so
// independent verifiers need no index convention.
type DualsJSON struct {
	Source    int64            `json:"source"`
	Sink      int64            `json:"sink"`
	Tasks     map[string]int64 `json:"tasks"`
	Employees map[string]int64 `json:"employees"`
}

// ResultJSON is the JSON form of a Result. Assignment is parallel to
// Tasks; a null entry means the task is unassigned (the sentinel,
// which compares greater than every employee ID).
type ResultJSON struct {
	Cardinality    int       `json:"cardinality"`
	TotalProfit    int64     `json:"total_profit"`
	Tasks          []string  `json:"tasks"`
	Employees      []string  `json:"employees"`
	Assignment     []*string `json:"assignment"`
	Matches        []Edge    `json:"matches"`
	MinVertexCover CoverJSON `json:"min_vertex_cover"`
	Potentials     DualsJSON `json:"potentials"`
}

// ToJSON converts a Result to its JSON form.
func ToJSON(res *Result) *ResultJSON {
	out := &ResultJSON{
		Cardinality: res.Cardinality,
		TotalProfit: res.TotalProfit,
		Tasks:       orEmpty(res.Tasks),
		Employees:   orEmpty(res.Employees),
		Assignment:  make([]*string, len(res.Assignment)),
		Matches:     orEmptyEdges(res.Matches),
		MinVertexCover: CoverJSON{
			Tasks:     orEmpty(res.CoverTasks),
			Employees: orEmpty(res.CoverEmps),
		},
		Potentials: DualsJSON{
			Source:    res.Duals.Source,
			Sink:      res.Duals.Sink,
			Tasks:     map[string]int64{},
			Employees: map[string]int64{},
		},
	}
	for i, a := range res.Assignment {
		if a != "" {
			s := a
			out.Assignment[i] = &s
		}
	}
	for i, id := range res.Tasks {
		out.Potentials.Tasks[id] = res.Duals.Tasks[i]
	}
	for j, id := range res.Employees {
		out.Potentials.Employees[id] = res.Duals.Employees[j]
	}
	return out
}

// ToResult parses a JSON-form result back for verification.
func (rj *ResultJSON) ToResult() (*Result, error) {
	if len(rj.Assignment) != len(rj.Tasks) {
		return nil, fmt.Errorf("assignment has %d entries, tasks has %d",
			len(rj.Assignment), len(rj.Tasks))
	}
	res := &Result{
		Cardinality: rj.Cardinality,
		TotalProfit: rj.TotalProfit,
		Tasks:       rj.Tasks,
		Employees:   rj.Employees,
		Assignment:  make([]string, len(rj.Tasks)),
		Matches:     rj.Matches,
		CoverTasks:  rj.MinVertexCover.Tasks,
		CoverEmps:   rj.MinVertexCover.Employees,
	}
	for i, a := range rj.Assignment {
		if a != nil {
			res.Assignment[i] = *a
		}
	}
	if rj.Potentials.Tasks == nil {
		rj.Potentials.Tasks = map[string]int64{}
	}
	if rj.Potentials.Employees == nil {
		rj.Potentials.Employees = map[string]int64{}
	}
	res.Duals.Source = rj.Potentials.Source
	res.Duals.Sink = rj.Potentials.Sink
	res.Duals.Tasks = make([]int64, len(rj.Tasks))
	for i, id := range rj.Tasks {
		v, ok := rj.Potentials.Tasks[id]
		if !ok {
			return nil, fmt.Errorf("missing dual potential for task %q", id)
		}
		res.Duals.Tasks[i] = v
	}
	res.Duals.Employees = make([]int64, len(rj.Employees))
	for j, id := range rj.Employees {
		v, ok := rj.Potentials.Employees[id]
		if !ok {
			return nil, fmt.Errorf("missing dual potential for employee %q", id)
		}
		res.Duals.Employees[j] = v
	}
	return res, nil
}

func orEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

func orEmptyEdges(xs []Edge) []Edge {
	if xs == nil {
		return []Edge{}
	}
	return xs
}
