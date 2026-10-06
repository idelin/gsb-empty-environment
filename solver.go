package main

import (
	"errors"
	"fmt"
	"sort"
)

// Idle marks a slot with no job scheduled.
const Idle = -1

// MaxSlots is the horizon limit: at most twenty time slots.
const MaxSlots = 20

const inf = int64(1) << 60

// Job is a preemptable task with integer processing requirement.
// Slot t covers [t, t+1); a job may run in slot t only if t >= Release.
type Job struct {
	ID         int `json:"id"`
	Release    int `json:"release"`
	Processing int `json:"processing"`
	Deadline   int `json:"deadline"`
	Weight     int `json:"weight"`
}

// Instance is a scheduling problem over Slots time slots (0..Slots-1).
type Instance struct {
	Slots int   `json:"slots"`
	Jobs  []Job `json:"jobs"`
}

// Result is an optimal schedule: Schedule[t] is the job id processed in
// slot t (or Idle). Completion[i] is the right end of the last slot in
// which job i runs; a zero-processing job completes at its release time.
type Result struct {
	Cost       int64   `json:"cost"`
	Schedule   []int   `json:"schedule"`
	Completion []int   `json:"completion"`
	Tardiness  []int64 `json:"tardiness"`
}

// Validate checks structural constraints of the instance.
func (inst *Instance) Validate() error {
	if inst.Slots < 0 || inst.Slots > MaxSlots {
		return fmt.Errorf("slots must be in [0, %d], got %d", MaxSlots, inst.Slots)
	}
	seen := map[int]bool{}
	for _, j := range inst.Jobs {
		if seen[j.ID] {
			return fmt.Errorf("duplicate job id %d", j.ID)
		}
		seen[j.ID] = true
		if j.Release < 0 {
			return fmt.Errorf("job %d: negative release", j.ID)
		}
		if j.Processing < 0 {
			return fmt.Errorf("job %d: negative processing", j.ID)
		}
		if j.Weight < 0 {
			return fmt.Errorf("job %d: negative weight", j.ID)
		}
		if j.Processing > 0 && j.Release+j.Processing > inst.Slots {
			return fmt.Errorf("job %d: infeasible (release %d + processing %d > slots %d)",
				j.ID, j.Release, j.Processing, inst.Slots)
		}
	}
	total := 0
	for _, j := range inst.Jobs {
		total += j.Processing
	}
	if total > inst.Slots {
		return errors.New("infeasible: total processing exceeds horizon")
	}
	return nil
}

// dpEntry memoizes, for a (time, remaining-work) state, the optimal
// tail cost and the action achieving it (index into sorted jobs, or Idle).
type dpEntry struct {
	cost   int64
	action int
}

// solver is an exact branch-and-bound over per-slot states with
// memoization. Ties are broken by the lexicographically smallest
// per-slot job id sequence, where a real job id sorts before Idle.
type solver struct {
	jobs  []Job // sorted by ID
	slots int
	radix []uint64
	memo  map[uint64]dpEntry
	rem   []int // scratch remaining-work vector
}

// Solve returns an optimal schedule, or an error if none exists.
func Solve(inst *Instance) (*Result, error) {
	if err := inst.Validate(); err != nil {
		return nil, err
	}
	jobs := make([]Job, len(inst.Jobs))
	copy(jobs, inst.Jobs)
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].ID < jobs[b].ID })

	s := &solver{jobs: jobs, slots: inst.Slots, memo: map[uint64]dpEntry{}, rem: make([]int, len(jobs))}
	s.radix = make([]uint64, len(jobs))
	base := uint64(1)
	for i, j := range jobs {
		s.radix[i] = base
		base *= uint64(j.Processing) + 1
		s.rem[i] = j.Processing
	}
	if base > (uint64(1)<<63)/uint64(inst.Slots+2) {
		return nil, errors.New("state space too large")
	}

	dpCost, _ := s.dp(0)
	if dpCost >= inf {
		return nil, errors.New("infeasible instance")
	}

	res := &Result{
		Schedule:   make([]int, inst.Slots),
		Completion: make([]int, len(jobs)),
		Tardiness:  make([]int64, len(jobs)),
	}
	for i, j := range jobs {
		s.rem[i] = j.Processing
		res.Completion[i] = j.Release // zero-processing jobs complete at release
	}
	for t := 0; t < inst.Slots; t++ {
		_, action := s.dp(t)
		if action == Idle {
			res.Schedule[t] = Idle
			continue
		}
		s.rem[action]--
		res.Schedule[t] = jobs[action].ID
		if s.rem[action] == 0 {
			res.Completion[action] = t + 1
		}
	}
	for i, j := range jobs {
		if c := res.Completion[i]; c > j.Deadline {
			res.Tardiness[i] = int64(j.Weight) * int64(c-j.Deadline)
		}
		res.Cost += res.Tardiness[i]
	}
	return res, nil
}

// key packs the remaining-work vector and the time into one integer.
func (s *solver) key(t int) uint64 {
	k := uint64(0)
	for i := range s.rem {
		k += uint64(s.rem[i]) * s.radix[i]
	}
	return k*uint64(s.slots+1) + uint64(t)
}

// dp returns the minimum total weighted tardiness achievable for slots
// t..slots-1 given s.rem, plus the optimal action for slot t. Among
// equal-cost options it prefers the smallest job id, and Idle last,
// which yields the lexicographically smallest per-slot id sequence.
func (s *solver) dp(t int) (int64, int) {
	if t == s.slots {
		for _, r := range s.rem {
			if r > 0 {
				return inf, Idle
			}
		}
		return 0, Idle
	}
	k := s.key(t)
	if e, ok := s.memo[k]; ok {
		return e.cost, e.action
	}
	best, bestAction := inf, Idle
	for i, j := range s.jobs {
		if s.rem[i] == 0 || j.Release > t {
			continue
		}
		s.rem[i]--
		tail, _ := s.dp(t + 1)
		s.rem[i]++
		if tail >= inf {
			continue
		}
		c := tail
		if s.rem[i] == 1 && t+1 > j.Deadline { // last unit finishes at t+1
			c += int64(j.Weight) * int64(t+1-j.Deadline)
		}
		if c < best {
			best, bestAction = c, i
		}
	}
	if tail, _ := s.dp(t + 1); tail < best {
		best, bestAction = tail, Idle
	}
	s.memo[k] = dpEntry{cost: best, action: bestAction}
	return best, bestAction
}
