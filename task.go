package zerobubble

import "fmt"

// TaskKind identifies one of the three Zero-Bubble pipeline computations.
type TaskKind uint8

const (
	Forward TaskKind = iota + 1
	Backward
	Weight
)

// Long-form aliases are provided for callers that prefer explicit Kind names.
const (
	KindForward  = Forward
	KindBackward = Backward
	KindWeight   = Weight
)

func (k TaskKind) String() string {
	switch k {
	case Forward:
		return "F"
	case Backward:
		return "B"
	case Weight:
		return "W"
	default:
		return "?"
	}
}

// TaskID uniquely identifies a compute task.
type TaskID struct {
	Kind       TaskKind
	Microbatch int
	Stage      int
}

func (id TaskID) String() string {
	return fmt.Sprintf("%s(%d,%d)", id.Kind, id.Microbatch, id.Stage)
}

// ShortName is the compact form used in Gantt timelines.
func (id TaskID) ShortName() string {
	return fmt.Sprintf("%s%d", id.Kind, id.Microbatch)
}

// Task is a scheduled F, B, or W operation. Intervals are half-open:
// [Start, End).
type Task struct {
	ID    TaskID
	Start float64
	End   float64
	Deps  []TaskID
}

func (t Task) Duration() float64 {
	return t.End - t.Start
}

// Mode selects the schedule family.
type Mode string

const (
	Mode1F1B       Mode = "1F1B"
	ModeZeroBubble Mode = "zero-bubble"
)
