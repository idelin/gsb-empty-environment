// Package flowshop solves the two-machine permutation flow shop problem
// with a zero-capacity intermediate buffer (blocking flow shop).
//
// Every job is processed on machine 1 first and then on machine 2.
// Because the buffer capacity is zero, a job that finishes on machine 1
// while machine 2 is still busy stays on machine 1 and blocks it.
// Both machines process the jobs in the same order (permutation schedule).
//
// Machines may be down during known, finite intervals. A job being
// processed when its machine goes down is paused but keeps occupying the
// machine; processing resumes when the machine comes back up.
package flowshop

// MaxJobs is the largest number of jobs the exact solver accepts.
// The optimum is found by exhaustive permutation search, which stays
// tractable up to 10! = 3,628,800 permutations.
const MaxJobs = 10

// Job is a single work piece.
type Job struct {
	ID int // unique identifier, used for tie-breaking
	P1 int // processing time on machine 1
	P2 int // processing time on machine 2
}

// Interval is a half-open time interval [Start, End).
type Interval struct {
	Start int
	End   int
}

// BlockedPeriod records one interval during which a finished job kept
// occupying machine 1 because machine 2 was not free yet.
type BlockedPeriod struct {
	JobID int
	Start int
	End   int
}

// DowntimeEvidence records one interval during which a job occupying a
// machine had its processing paused because the machine was down.
type DowntimeEvidence struct {
	Machine int // 1 or 2
	JobID   int
	Start   int
	End     int
}

// JobTimeline describes the complete trajectory of one job.
type JobTimeline struct {
	JobID     int
	EnterM1   int // job loads onto machine 1
	ProcEndM1 int // processing on machine 1 finishes (pauses included)
	DepartM1  int // job leaves machine 1 (== EnterM2); gap after ProcEndM1 is blocking
	EnterM2   int // job loads onto machine 2
	DepartM2  int // job leaves machine 2 (completed)
}

// Schedule is the full result of simulating one job sequence.
type Schedule struct {
	Timeline []JobTimeline // in processing order
	Makespan int
	Blocking []BlockedPeriod    // all blocking intervals on machine 1
	Downtime []DowntimeEvidence // all pause intervals caused by machine downtime
}

// machineState carries the availability of both machines between a fixed
// prefix and a suffix that is still to be scheduled.
type machineState struct {
	freeM1 int // time machine 1 becomes empty
	freeM2 int // time machine 2 becomes empty
}

// addWork computes the finish time of a job that needs dur units of
// processing on a machine with the given (sorted, non-overlapping)
// downtime intervals, starting to occupy the machine at start. The job
// occupies the machine from start until the returned finish time; any
// downtime inside that span pauses the work and is reported as evidence.
func addWork(start, dur int, downs []Interval) (finish int, hits []Interval) {
	cur := start
	rem := dur
	for _, d := range downs {
		if d.End <= cur {
			continue
		}
		if rem == 0 || d.Start >= cur+rem {
			break
		}
		if d.Start <= cur {
			// The machine is already down when the job reaches this
			// point; the job waits while occupying the machine.
			hits = append(hits, Interval{Start: cur, End: d.End})
			cur = d.End
		} else {
			// Work proceeds until the downtime begins, then pauses.
			rem -= d.Start - cur
			hits = append(hits, Interval{Start: d.Start, End: d.End})
			cur = d.End
		}
	}
	return cur + rem, hits
}

// workFinish is the allocation-free core of addWork used in the
// exhaustive search loop, where downtime evidence is not needed.
func workFinish(start, dur int, downs []Interval) int {
	cur := start
	rem := dur
	for _, d := range downs {
		if d.End <= cur {
			continue
		}
		if rem == 0 || d.Start >= cur+rem {
			break
		}
		if d.Start <= cur {
			cur = d.End
		} else {
			rem -= d.Start - cur
			cur = d.End
		}
	}
	return cur + rem
}

// makespanOf evaluates only the makespan of a permutation, without
// building the schedule.
func makespanOf(seq []Job, down1, down2 []Interval, st machineState) int {
	prevDepartM1 := st.freeM1
	prevDepartM2 := st.freeM2
	for _, j := range seq {
		procEnd1 := workFinish(prevDepartM1, j.P1, down1)
		depart1 := procEnd1
		if prevDepartM2 > depart1 {
			depart1 = prevDepartM2
		}
		prevDepartM2 = workFinish(depart1, j.P2, down2)
		prevDepartM1 = depart1
	}
	if len(seq) == 0 {
		if st.freeM1 > st.freeM2 {
			return st.freeM1
		}
		return st.freeM2
	}
	return prevDepartM2
}

// computeSchedule evaluates one permutation with the recursion for the
// blocking flow shop, starting from the given machine state. It is the
// reference model that the discrete-event simulator is checked against.
func computeSchedule(seq []Job, down1, down2 []Interval, st machineState) Schedule {
	s := Schedule{Timeline: make([]JobTimeline, 0, len(seq))}
	prevDepartM1 := st.freeM1
	prevDepartM2 := st.freeM2
	for _, j := range seq {
		enter1 := prevDepartM1
		procEnd1, hits1 := addWork(enter1, j.P1, down1)
		// Zero buffer: the job can only leave machine 1 once machine 2
		// is empty; until then it blocks machine 1.
		depart1 := procEnd1
		if prevDepartM2 > depart1 {
			depart1 = prevDepartM2
		}
		enter2 := depart1
		depart2, hits2 := addWork(enter2, j.P2, down2)

		s.Timeline = append(s.Timeline, JobTimeline{
			JobID:     j.ID,
			EnterM1:   enter1,
			ProcEndM1: procEnd1,
			DepartM1:  depart1,
			EnterM2:   enter2,
			DepartM2:  depart2,
		})
		if depart1 > procEnd1 {
			s.Blocking = append(s.Blocking, BlockedPeriod{JobID: j.ID, Start: procEnd1, End: depart1})
		}
		for _, h := range hits1 {
			s.Downtime = append(s.Downtime, DowntimeEvidence{Machine: 1, JobID: j.ID, Start: h.Start, End: h.End})
		}
		for _, h := range hits2 {
			s.Downtime = append(s.Downtime, DowntimeEvidence{Machine: 2, JobID: j.ID, Start: h.Start, End: h.End})
		}
		prevDepartM1 = depart1
		prevDepartM2 = depart2
	}
	if n := len(s.Timeline); n > 0 {
		s.Makespan = s.Timeline[n-1].DepartM2
	} else if st.freeM1 > st.freeM2 {
		s.Makespan = st.freeM1
	} else {
		s.Makespan = st.freeM2
	}
	return s
}

// normalizeDowntime sorts downtime intervals and merges overlapping or
// adjacent ones, dropping empty intervals.
func normalizeDowntime(downs []Interval) []Interval {
	out := make([]Interval, 0, len(downs))
	for _, d := range downs {
		if d.End > d.Start {
			out = append(out, d)
		}
	}
	for i := 0; i+1 < len(out); i++ {
		for k := i + 1; k < len(out); k++ {
			if out[k].Start < out[i].Start {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	merged := out[:0]
	for _, d := range out {
		if n := len(merged); n > 0 && d.Start <= merged[n-1].End {
			if d.End > merged[n-1].End {
				merged[n-1].End = d.End
			}
			continue
		}
		merged = append(merged, d)
	}
	return merged
}
