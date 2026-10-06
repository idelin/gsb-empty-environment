package main

import "errors"

// bruteForce enumerates every valid per-slot assignment (each slot idle
// or running one released job with remaining work) and scores the final
// schedule directly. It is exponential and meant only to cross-check
// Solve on small instances, including ones with idle slots and
// zero-processing jobs.
func bruteForce(inst *Instance) (*Result, error) {
	if err := inst.Validate(); err != nil {
		return nil, err
	}
	jobs := make([]Job, len(inst.Jobs))
	copy(jobs, inst.Jobs)
	// Jobs are already required to have unique ids; sort by id so the
	// DFS visits actions in tie-break preference order (id asc, idle last).
	for i := 1; i < len(jobs); i++ {
		for k := i; k > 0 && jobs[k].ID < jobs[k-1].ID; k-- {
			jobs[k], jobs[k-1] = jobs[k-1], jobs[k]
		}
	}

	b := &bruter{
		jobs:     jobs,
		slots:    inst.Slots,
		rem:      make([]int, len(jobs)),
		schedule: make([]int, inst.Slots),
		best:     make([]int, inst.Slots),
		bestCost: inf,
	}
	for i, j := range jobs {
		b.rem[i] = j.Processing
	}
	b.dfs(0)
	if b.bestCost >= inf {
		return nil, errors.New("infeasible instance")
	}

	res := &Result{
		Cost:       b.bestCost,
		Schedule:   make([]int, inst.Slots),
		Completion: make([]int, len(jobs)),
		Tardiness:  make([]int64, len(jobs)),
	}
	for t, jobIdx := range b.best {
		if jobIdx == Idle {
			res.Schedule[t] = Idle
		} else {
			res.Schedule[t] = jobs[jobIdx].ID
		}
	}
	for i, j := range jobs {
		res.Completion[i] = j.Release
	}
	for t, id := range res.Schedule {
		if id == Idle {
			continue
		}
		for i := range jobs {
			if jobs[i].ID == id {
				res.Completion[i] = t + 1
			}
		}
	}
	for i, j := range jobs {
		if c := res.Completion[i]; c > j.Deadline {
			res.Tardiness[i] = int64(j.Weight) * int64(c-j.Deadline)
		}
	}
	return res, nil
}

type bruter struct {
	jobs     []Job
	slots    int
	rem      []int
	schedule []int // job index per slot, Idle for idle
	best     []int
	bestCost int64
}

func (b *bruter) dfs(t int) {
	if t == b.slots {
		for _, r := range b.rem {
			if r > 0 {
				return
			}
		}
		cost := int64(0)
		for i, j := range b.jobs {
			c := j.Release
			if j.Processing > 0 {
				c = 0
				for s := b.slots - 1; s >= 0; s-- {
					if b.schedule[s] == i {
						c = s + 1
						break
					}
				}
			}
			if c > j.Deadline {
				cost += int64(j.Weight) * int64(c-j.Deadline)
			}
		}
		if cost < b.bestCost || (cost == b.bestCost && b.less(b.schedule, b.best)) {
			b.bestCost = cost
			copy(b.best, b.schedule)
		}
		return
	}
	for i, j := range b.jobs {
		if b.rem[i] == 0 || j.Release > t {
			continue
		}
		b.rem[i]--
		b.schedule[t] = i
		b.dfs(t + 1)
		b.rem[i]++
	}
	b.schedule[t] = Idle
	b.dfs(t + 1)
	b.schedule[t] = Idle
}

// less compares two per-slot schedules lexicographically by job id,
// with Idle sorting after every real job id.
func (b *bruter) less(a, c []int) bool {
	for t := range a {
		ka, kc := b.slotKey(a[t]), b.slotKey(c[t])
		if ka != kc {
			return ka < kc
		}
	}
	return false
}

func (b *bruter) slotKey(jobIdx int) int {
	if jobIdx == Idle {
		return 1 << 30
	}
	return b.jobs[jobIdx].ID
}
