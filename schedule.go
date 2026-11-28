package zerobubble

import (
	"fmt"
	"sort"
	"strings"
)

// CommKind distinguishes the two pipeline point-to-point messages.
type CommKind uint8

const (
	ActivationComm CommKind = iota + 1
	GradientComm
)

func (k CommKind) String() string {
	switch k {
	case ActivationComm:
		return "activation"
	case GradientComm:
		return "gradient"
	default:
		return "unknown"
	}
}

// Comm is one matched Send/Recv pair on a directed adjacent-stage link.
// SendStart and RecvStart coincide for a synchronous rendezvous handshake.
type Comm struct {
	ID         string
	Kind       CommKind
	Microbatch int
	FromStage  int
	ToStage    int
	Producer   TaskID
	Consumer   TaskID

	SendStart float64
	SendEnd   float64
	RecvStart float64
	RecvEnd   float64
}

// Schedule contains a complete executable timeline.
type Schedule struct {
	Mode   Mode
	Config Config

	Tasks []Task
	Comms []Comm

	Makespan float64

	// Work is the sum of all compute-task durations. Capacity is P*makespan.
	Work     float64
	Capacity float64

	// BubbleTime is Capacity-Work; BubbleRate is BubbleTime/Capacity.
	BubbleTime  float64
	BubbleRate  float64
	Utilization float64

	PeakMemory   []int64
	PeakMemoryAt []float64

	// Overall peak across stages.
	MaxPeakMemory      int64
	MaxPeakMemoryStage int
	MaxPeakMemoryAt    float64
}

// Task returns the task with the given identity.
func (s *Schedule) Task(kind TaskKind, microbatch, stage int) (Task, bool) {
	for _, t := range s.Tasks {
		if t.ID.Kind == kind && t.ID.Microbatch == microbatch && t.ID.Stage == stage {
			return t, true
		}
	}
	return Task{}, false
}

// StageTasks returns the tasks of one stage ordered by start time.
func (s *Schedule) StageTasks(stage int) []Task {
	out := make([]Task, 0)
	for _, t := range s.Tasks {
		if t.ID.Stage == stage {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if nearlyEqual(out[i].Start, out[j].Start) {
			return out[i].ID.Kind < out[j].ID.Kind
		}
		return out[i].Start < out[j].Start
	})
	return out
}

// Gantt renders an exact interval table followed by a compact stage timeline.
func (s *Schedule) Gantt() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s schedule (P=%d, M=%d, makespan=%.3f, bubble=%.2f%%)\n",
		s.Mode, s.Config.P, s.Config.M, s.Makespan, 100*s.BubbleRate)
	fmt.Fprintln(&b, "stage | task | start | end | duration | dependencies")
	fmt.Fprintln(&b, "------+------+-------+-----+----------+-------------")
	tasks := append([]Task(nil), s.Tasks...)
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].ID.Stage != tasks[j].ID.Stage {
			return tasks[i].ID.Stage < tasks[j].ID.Stage
		}
		if !nearlyEqual(tasks[i].Start, tasks[j].Start) {
			return tasks[i].Start < tasks[j].Start
		}
		return tasks[i].ID.Kind < tasks[j].ID.Kind
	})
	for _, t := range tasks {
		deps := make([]string, 0, len(t.Deps))
		for _, d := range t.Deps {
			deps = append(deps, d.String())
		}
		fmt.Fprintf(&b, "%5d | %-4s | %5.3f | %3.3f | %8.3f | %s\n",
			t.ID.Stage, t.ID.ShortName(), t.Start, t.End, t.Duration(), strings.Join(deps, ", "))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "stage | peak-memory | peak-at")
	fmt.Fprintln(&b, "------+-------------+--------")
	for stage := 0; stage < s.Config.P; stage++ {
		at := 0.0
		if stage < len(s.PeakMemoryAt) {
			at = s.PeakMemoryAt[stage]
		}
		peak := int64(0)
		if stage < len(s.PeakMemory) {
			peak = s.PeakMemory[stage]
		}
		fmt.Fprintf(&b, "%5d | %11d | %7.3f\n", stage, peak, at)
	}
	fmt.Fprintln(&b)
	for stage := 0; stage < s.Config.P; stage++ {
		fmt.Fprintf(&b, "S%-2d ", stage)
		for _, t := range s.StageTasks(stage) {
			fmt.Fprintf(&b, "[%.3f,%.3f)%s ", t.Start, t.End, t.ID.ShortName())
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}

func nearlyEqual(a, b float64) bool {
	return absFloat(a-b) <= timeEpsilon
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
