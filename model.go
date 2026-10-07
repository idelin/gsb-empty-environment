package main

import (
	"encoding/json"
	"fmt"
)

// Resource is a renewable resource with a per-time-unit capacity.
type Resource struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

// Job is a non-preemptive activity. Duration may be zero; zero-duration
// jobs consume no resources but still impose precedence constraints.
type Job struct {
	ID           int   `json:"id"`
	Duration     int   `json:"duration"`
	Demands      []int `json:"demands"`      // per-resource demand while active
	Predecessors []int `json:"predecessors"` // indices of jobs that must finish first
}

// Instance is a full problem definition over a discrete time horizon
// [0, Horizon). Every job i must satisfy start_i + duration_i <= Horizon.
type Instance struct {
	Horizon   int        `json:"horizon"`
	Resources []Resource `json:"resources"`
	Jobs      []Job      `json:"jobs"`
}

// TimeEvidence reports, for one time slot, the resource usage and the
// set of jobs occupying that slot.
type TimeEvidence struct {
	Time  int   `json:"time"`
	Usage []int `json:"usage"`
	Jobs  []int `json:"jobs"`
}

// Result is the solver output.
type Result struct {
	Status   string         `json:"status"` // "optimal" or "infeasible"
	Reason   string         `json:"reason,omitempty"`
	Makespan int            `json:"makespan,omitempty"`
	Starts   []int          `json:"starts,omitempty"`
	Timeline []TimeEvidence `json:"timeline,omitempty"`
}

// validate normalizes and sanity-checks the instance.
func validate(inst *Instance) error {
	if inst.Horizon < 0 {
		return fmt.Errorf("horizon must be >= 0")
	}
	k := len(inst.Resources)
	for r, res := range inst.Resources {
		if res.Capacity < 0 {
			return fmt.Errorf("resource %d (%q): negative capacity", r, res.Name)
		}
	}
	n := len(inst.Jobs)
	if n > 12 {
		return fmt.Errorf("at most 12 jobs supported, got %d", n)
	}
	for i := range inst.Jobs {
		j := &inst.Jobs[i]
		j.ID = i
		if j.Duration < 0 {
			return fmt.Errorf("job %d: negative duration", i)
		}
		if len(j.Demands) == 0 {
			j.Demands = make([]int, k)
		}
		if len(j.Demands) != k {
			return fmt.Errorf("job %d: demands length %d != %d resources", i, len(j.Demands), k)
		}
		for r, d := range j.Demands {
			if d < 0 {
				return fmt.Errorf("job %d: negative demand on resource %d", i, r)
			}
		}
		seen := map[int]bool{}
		for _, p := range j.Predecessors {
			if p < 0 || p >= n {
				return fmt.Errorf("job %d: predecessor %d out of range", i, p)
			}
			if p == i {
				return fmt.Errorf("job %d: self precedence", i)
			}
			if seen[p] {
				return fmt.Errorf("job %d: duplicate predecessor %d", i, p)
			}
			seen[p] = true
		}
	}
	return nil
}

// buildTimeline derives per-time resource evidence from a start vector.
func buildTimeline(inst *Instance, starts []int, makespan int) []TimeEvidence {
	k := len(inst.Resources)
	tl := make([]TimeEvidence, makespan)
	for t := 0; t < makespan; t++ {
		tl[t] = TimeEvidence{Time: t, Usage: make([]int, k), Jobs: []int{}}
	}
	for i, job := range inst.Jobs {
		for t := starts[i]; t < starts[i]+job.Duration; t++ {
			for r := 0; r < k; r++ {
				tl[t].Usage[r] += job.Demands[r]
			}
			tl[t].Jobs = append(tl[t].Jobs, i)
		}
	}
	return tl
}

func mustJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}
