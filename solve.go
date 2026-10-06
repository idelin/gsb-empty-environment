package flowshop

import "errors"

// Result is the outcome of Solve or Replan.
type Result struct {
	Sequence []int    // job IDs in the chosen processing order
	Schedule Schedule // schedule of the chosen sequence, blocking and downtime included
}

// Solve finds a permutation of jobs (at most MaxJobs) that minimises the
// makespan of the two-machine blocking flow shop, honouring the known
// downtime tables of both machines. Every permutation is evaluated, so
// the result is provably optimal. Ties are broken by choosing the
// lexicographically smallest job-ID sequence.
func Solve(jobs []Job, down1, down2 []Interval) (Result, error) {
	return search(jobs, nil, down1, down2)
}

// Replan re-optimises the order of the remaining jobs after some jobs
// have already been started. The prefix keeps its given order and its
// realised timing (it cannot be re-sequenced); only the suffix is
// permuted. The known downtime tables still apply. The returned result
// covers the whole horizon: prefix followed by the optimised suffix.
func Replan(prefix, remaining []Job, down1, down2 []Interval) (Result, error) {
	return search(remaining, prefix, down1, down2)
}

func search(suffix, prefix []Job, down1, down2 []Interval) (Result, error) {
	if err := validate(prefix, suffix); err != nil {
		return Result{}, err
	}
	d1 := normalizeDowntime(down1)
	d2 := normalizeDowntime(down2)

	st := machineState{}
	head := Schedule{}
	if len(prefix) > 0 {
		head = computeSchedule(prefix, d1, d2, st)
		last := head.Timeline[len(head.Timeline)-1]
		st = machineState{freeM1: last.DepartM1, freeM2: last.DepartM2}
	}

	// Enumerate permutations in lexicographic order of job IDs so that
	// keeping the first best schedule yields the smallest ID sequence
	// among all optimal ones.
	perm := sortByID(suffix)
	best := -1
	var bestPerm []Job
	for {
		ms := makespanOf(perm, d1, d2, st)
		if best < 0 || ms < best {
			best = ms
			bestPerm = append([]Job(nil), perm...)
		}
		if !nextPermutation(perm) {
			break
		}
	}

	tail := computeSchedule(bestPerm, d1, d2, st)
	full := mergeSchedules(head, tail)
	res := Result{Schedule: full}
	for _, j := range prefix {
		res.Sequence = append(res.Sequence, j.ID)
	}
	for _, j := range bestPerm {
		res.Sequence = append(res.Sequence, j.ID)
	}
	return res, nil
}

func mergeSchedules(head, tail Schedule) Schedule {
	out := Schedule{Makespan: tail.Makespan}
	if len(head.Timeline) > 0 && len(tail.Timeline) == 0 {
		out.Makespan = head.Makespan
	}
	out.Timeline = append(head.Timeline, tail.Timeline...)
	out.Blocking = append(head.Blocking, tail.Blocking...)
	out.Downtime = append(head.Downtime, tail.Downtime...)
	return out
}

func validate(prefix, suffix []Job) error {
	if len(prefix)+len(suffix) > MaxJobs {
		return errors.New("flowshop: more than 10 jobs")
	}
	seen := make(map[int]bool)
	for _, j := range append(append([]Job(nil), prefix...), suffix...) {
		if j.P1 < 0 || j.P2 < 0 {
			return errors.New("flowshop: negative processing time")
		}
		if seen[j.ID] {
			return errors.New("flowshop: duplicate job ID")
		}
		seen[j.ID] = true
	}
	return nil
}

func sortByID(jobs []Job) []Job {
	out := append([]Job(nil), jobs...)
	for i := 1; i < len(out); i++ {
		for k := i; k > 0 && out[k].ID < out[k-1].ID; k-- {
			out[k], out[k-1] = out[k-1], out[k]
		}
	}
	return out
}

// nextPermutation advances a to the next lexicographic permutation and
// reports whether one exists (Knuth's algorithm L).
func nextPermutation(a []Job) bool {
	i := len(a) - 2
	for i >= 0 && a[i].ID >= a[i+1].ID {
		i--
	}
	if i < 0 {
		return false
	}
	j := len(a) - 1
	for a[j].ID <= a[i].ID {
		j--
	}
	a[i], a[j] = a[j], a[i]
	for lo, hi := i+1, len(a)-1; lo < hi; lo, hi = lo+1, hi-1 {
		a[lo], a[hi] = a[hi], a[lo]
	}
	return true
}

// Verify cross-checks the recursive model against the independent
// discrete-event simulation for one explicit sequence. It returns nil
// when both produce identical makespans, timelines, blocking periods and
// downtime evidence.
func Verify(seq []Job, down1, down2 []Interval) error {
	d1 := normalizeDowntime(down1)
	d2 := normalizeDowntime(down2)
	rec := computeSchedule(seq, d1, d2, machineState{})
	ev := simulateEvents(seq, d1, d2)
	if rec.Makespan != ev.Makespan {
		return errors.New("flowshop: makespan mismatch between model and event simulation")
	}
	if len(rec.Timeline) != len(ev.Timeline) {
		return errors.New("flowshop: timeline length mismatch")
	}
	for i := range rec.Timeline {
		if rec.Timeline[i] != ev.Timeline[i] {
			return errors.New("flowshop: timeline mismatch")
		}
	}
	if !sameBlocking(rec.Blocking, ev.Blocking) {
		return errors.New("flowshop: blocking mismatch")
	}
	if !sameDowntime(rec.Downtime, ev.Downtime) {
		return errors.New("flowshop: downtime evidence mismatch")
	}
	return nil
}

func sameBlocking(a, b []BlockedPeriod) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameDowntime(a, b []DowntimeEvidence) bool {
	if len(a) != len(b) {
		return false
	}
	as := sortedEvidence(a)
	bs := sortedEvidence(b)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// sortedEvidence orders downtime evidence canonically so that the
// recursive model (job order) and the event simulation (chronological
// order) can be compared as sets.
func sortedEvidence(in []DowntimeEvidence) []DowntimeEvidence {
	out := append([]DowntimeEvidence(nil), in...)
	for i := 1; i < len(out); i++ {
		for k := i; k > 0 && evidenceLess(out[k], out[k-1]); k-- {
			out[k], out[k-1] = out[k-1], out[k]
		}
	}
	return out
}

func evidenceLess(a, b DowntimeEvidence) bool {
	if a.Start != b.Start {
		return a.Start < b.Start
	}
	if a.Machine != b.Machine {
		return a.Machine < b.Machine
	}
	if a.JobID != b.JobID {
		return a.JobID < b.JobID
	}
	return a.End < b.End
}
