package sortexec

// Phase identifies the executor stage at which a barrier fires.
type Phase int

const (
	PhaseProduce Phase = iota // sorting input runs and spilling them to disk
	PhaseMerge                // k-way merging runs into the sorted file
	PhaseDeliver              // streaming sorted batches to the caller
)

func (p Phase) String() string {
	switch p {
	case PhaseProduce:
		return "produce"
	case PhaseMerge:
		return "merge"
	case PhaseDeliver:
		return "deliver"
	}
	return "unknown"
}

// Barriers are optional test hooks. Enter fires once when a phase begins;
// Step fires before each record processed in that phase, with n counting
// records seen so far in the phase. Hooks may block or cancel the context
// to reproduce races deterministically.
type Barriers struct {
	Enter func(p Phase)
	Step  func(p Phase, n int64)
}

func (b Barriers) enter(p Phase) {
	if b.Enter != nil {
		b.Enter(p)
	}
}

func (b Barriers) step(p Phase, n int64) {
	if b.Step != nil {
		b.Step(p, n)
	}
}
