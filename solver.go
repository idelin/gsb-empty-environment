package main

// Single-machine preemptive scheduling solver.
//
// Model:
//   - Time is slotted 0..horizon-1; at most one task runs per slot.
//   - Task j is available in slots t >= Release and needs Processing slots
//     (integer), not necessarily consecutive (preemption allowed).
//   - Completion time C_j is the right end of the last slot in which the
//     task is processed, i.e. (last slot index) + 1.
//   - Tardiness T_j = max(0, C_j - Deadline); objective minimizes the
//     weighted sum Weight_j * T_j over all tasks.
//   - A task with Processing == 0 needs no slot; it is considered complete
//     at its release time, contributing Weight * max(0, Release - Deadline).
//   - Idle slots (Idle = -1) are always allowed.
//   - A task still incomplete at the horizon is penalized by
//     incompletePenalty plus Weight * max(0, horizon - Deadline), so any
//     completable task is always finished when feasible.
//
// Tie-breaking: among minimum-cost schedules, the lexicographically
// smallest per-slot task-id sequence wins, with Idle (-1) ordering before
// any task id.
//
// The solver is an exhaustive state-space search (DFS over slot index and
// remaining-processing vector) with memoization; correctness for idle
// slots and zero-processing tasks is cross-checked against a brute-force
// enumerator in solver_test.go.

const Idle = -1

const incompletePenalty = int64(1) << 40

const maxHorizon = 20

// Task describes one job.
type Task struct {
	ID         int `json:"id"`
	Release    int `json:"release"`
	Processing int `json:"processing"`
	Deadline   int `json:"deadline"`
	Weight     int `json:"weight"`
}

// dpResult is the optimal outcome from a state to the end of the horizon.
type dpResult struct {
	cost int64
	seq  []int // per-slot choice (task id or Idle) from the state's slot on
}

type solver struct {
	tasks   []Task // only tasks with Processing > 0
	horizon int
	memo    map[string]dpResult
}

// Solve returns the minimum total weighted tardiness and an optimal
// per-slot schedule (length horizon; Idle where no task runs).
func Solve(tasks []Task, horizon int) (int64, []int) {
	var constCost int64
	active := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		if t.Processing <= 0 {
			// Zero-processing tasks complete at their release time.
			constCost += int64(t.Weight) * max64(0, int64(t.Release-t.Deadline))
			continue
		}
		active = append(active, t)
	}
	s := &solver{tasks: active, horizon: horizon, memo: make(map[string]dpResult)}
	rem := make([]int, len(active))
	for i, t := range active {
		rem[i] = t.Processing
	}
	res := s.search(0, rem)
	return constCost + res.cost, res.seq
}

// search returns the optimal (cost, sequence) from slot t given remaining
// processing amounts rem.
func (s *solver) search(t int, rem []int) dpResult {
	if key, ok := s.memoKey(t, rem); ok {
		if res, hit := s.memo[key]; hit {
			return res
		}
		var res dpResult
		res = s.compute(t, rem)
		s.memo[key] = res
		return res
	}
	return s.compute(t, rem)
}

func (s *solver) memoKey(t int, rem []int) (string, bool) {
	if t >= s.horizon {
		return "", false // terminal states are trivial; skip allocation
	}
	buf := make([]byte, 1+len(rem))
	buf[0] = byte(t)
	for i, r := range rem {
		buf[1+i] = byte(r)
	}
	return string(buf), true
}

func (s *solver) compute(t int, rem []int) dpResult {
	remaining := 0
	for _, r := range rem {
		remaining += r
	}
	if remaining == 0 {
		// Everything finished: idle out the rest of the horizon.
		return dpResult{cost: 0, seq: idleSeq(s.horizon - t)}
	}
	if t >= s.horizon {
		// Horizon exhausted with work left: penalize incomplete tasks.
		var cost int64
		for i, task := range s.tasks {
			if rem[i] > 0 {
				cost += incompletePenalty +
					int64(task.Weight)*max64(0, int64(s.horizon-task.Deadline))
			}
		}
		return dpResult{cost: cost, seq: nil}
	}

	best := dpResult{cost: int64(1)<<62 - 1}

	// Choice 1: leave this slot idle.
	best = better(best, s.step(t, rem, -1))

	// Choice 2: run any released, unfinished task.
	for i, task := range s.tasks {
		if rem[i] == 0 || task.Release > t {
			continue
		}
		best = better(best, s.step(t, rem, i))
	}
	return best
}

// step evaluates running task index idx (or Idle when idx < 0) in slot t.
func (s *solver) step(t int, rem []int, idx int) dpResult {
	next := make([]int, len(rem))
	copy(next, rem)
	var cost int64
	choice := Idle
	if idx >= 0 {
		next[idx]--
		choice = s.tasks[idx].ID
		if next[idx] == 0 {
			// Task completes at the right end of this slot.
			task := s.tasks[idx]
			cost = int64(task.Weight) * max64(0, int64(t+1-task.Deadline))
		}
	}
	sub := s.search(t+1, next)
	seq := make([]int, 1+len(sub.seq))
	seq[0] = choice
	copy(seq[1:], sub.seq)
	return dpResult{cost: cost + sub.cost, seq: seq}
}

// better returns the preferred of two results: lower cost, then the
// lexicographically smaller per-slot id sequence (Idle sorts first).
func better(a, b dpResult) dpResult {
	if b.cost != a.cost {
		if b.cost < a.cost {
			return b
		}
		return a
	}
	if lexLess(b.seq, a.seq) {
		return b
	}
	return a
}

func lexLess(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func idleSeq(n int) []int {
	seq := make([]int, n)
	for i := range seq {
		seq[i] = Idle
	}
	return seq
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
