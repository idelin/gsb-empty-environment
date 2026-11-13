package netsec

import (
	"fmt"
	"strings"
)

// Program is a set of per-thread event scripts. The index in Threads is the
// thread number used both for thread events and for the lexicographic
// tie-break between witnesses of equal length. Updater events may appear in
// any script; they always act globally.
type Program struct {
	Threads [][]Event
}

// NewProgram constructs a program from per-thread event slices.
func NewProgram(threads ...[]Event) Program {
	return Program{Threads: threads}
}

// Validate checks the model bounds and event shapes.
func (p Program) Validate() error {
	if len(p.Threads) > MaxThreads {
		return fmt.Errorf("program has %d threads, maximum is %d", len(p.Threads), MaxThreads)
	}
	for i, script := range p.Threads {
		if len(script) > MaxEventsPerThread {
			return fmt.Errorf("thread %d has %d events, maximum is %d",
				i, len(script), MaxEventsPerThread)
		}
		for j, ev := range script {
			if err := ev.validateFields(); err != nil {
				return fmt.Errorf("thread %d event %d: %w", i, j, err)
			}
		}
	}
	return nil
}

// WitnessStep is one event in a witness interleaving.
type WitnessStep struct {
	Thread    int
	Event     Event
	Advanced  bool
	Reclaimed []ObjectRef
}

func (s WitnessStep) String() string {
	var suffix string
	if s.Event.Kind == EventTryAdvance {
		suffix = fmt.Sprintf(" (advanced=%t)", s.Advanced)
	}
	if len(s.Reclaimed) > 0 {
		suffix += fmt.Sprintf(" (reclaimed=%v)", s.Reclaimed)
	}
	return fmt.Sprintf("T%d:%s%s", s.Thread, s.Event, suffix)
}

// Witness is a shortest event interleaving that reaches a use-after-free.
type Witness struct {
	Steps     []WitnessStep
	Violation Violation
	State     *State
}

// Length returns the number of events in the witness.
func (w *Witness) Length() int {
	if w == nil {
		return 0
	}
	return len(w.Steps)
}

// Actors returns the thread number of each witness event.
func (w *Witness) Actors() []int {
	if w == nil {
		return nil
	}
	actors := make([]int, len(w.Steps))
	for i, step := range w.Steps {
		actors[i] = step.Thread
	}
	return actors
}

// Events returns only the events of the witness.
func (w *Witness) Events() []Event {
	if w == nil {
		return nil
	}
	events := make([]Event, len(w.Steps))
	for i, step := range w.Steps {
		events[i] = step.Event
	}
	return events
}

func (w *Witness) String() string {
	if w == nil {
		return "<no witness>"
	}
	var b strings.Builder
	for _, step := range w.Steps {
		b.WriteString(step.String())
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "violation: %s", w.Violation)
	return b.String()
}

// FindShortestWitness searches all legal interleavings of the per-thread
// scripts. It returns nil, nil when no use-after-free is reachable. Among
// witnesses of minimal length, the lexicographically smallest sequence of
// thread numbers is returned; children are explored in ascending thread order.
func FindShortestWitness(p Program) (*Witness, error) {
	return FindShortestWitnessWithOptions(p, DefaultOptions())
}

// FindShortestWitnessWithOptions is FindShortestWitness with explicit state
// options. For a normal correctness search use DefaultOptions.
func FindShortestWitnessWithOptions(p Program, opts Options) (*Witness, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	threadCount := len(p.Threads)
	st, err := NewStateWithOptions(threadCount, opts)
	if err != nil {
		return nil, err
	}

	startPos := make([]int, threadCount)
	queue := []searchNode{{pos: startPos, state: st, parent: -1}}
	visited := map[string]struct{}{searchKey(startPos, st): struct{}{}}

	for head := 0; head < len(queue); head++ {
		cur := &queue[head]
		for thread := 0; thread < threadCount; thread++ {
			if cur.pos[thread] >= len(p.Threads[thread]) {
				continue
			}
			ev := p.Threads[thread][cur.pos[thread]]
			nextState := cur.state.Clone()
			applyThread := ThreadID(thread)
			if ev.Kind.IsUpdaterEvent() {
				applyThread = UpdaterThread
			}
			out, err := nextState.Apply(applyThread, ev)
			if err != nil {
				continue
			}

			nextPos := append([]int(nil), cur.pos...)
			nextPos[thread]++

			if out.Violation != nil {
				idx := len(queue)
				queue = append(queue, searchNode{
					pos:       nextPos,
					state:     nextState,
					parent:    head,
					thread:    thread,
					event:     ev,
					advanced:  out.Advanced,
					reclaimed: out.Reclaimed,
				})
				return buildWitness(queue, idx, *out.Violation), nil
			}

			key := searchKey(nextPos, nextState)
			if _, seen := visited[key]; seen {
				continue
			}
			visited[key] = struct{}{}
			queue = append(queue, searchNode{
				pos:       nextPos,
				state:     nextState,
				parent:    head,
				thread:    thread,
				event:     ev,
				advanced:  out.Advanced,
				reclaimed: out.Reclaimed,
			})
		}
	}
	return nil, nil
}

type searchNode struct {
	pos       []int
	state     *State
	parent    int
	thread    int
	event     Event
	advanced  bool
	reclaimed []ObjectRef
}

func buildWitness(queue []searchNode, idx int, violation Violation) *Witness {
	var reversed []WitnessStep
	for at := idx; at >= 0 && queue[at].parent >= 0; at = queue[at].parent {
		n := queue[at]
		reversed = append(reversed, WitnessStep{
			Thread:    n.thread,
			Event:     n.event,
			Advanced:  n.advanced,
			Reclaimed: append([]ObjectRef(nil), n.reclaimed...),
		})
	}
	steps := make([]WitnessStep, len(reversed))
	for i := range reversed {
		steps[len(reversed)-1-i] = reversed[i]
	}
	return &Witness{
		Steps:     steps,
		Violation: violation,
		State:     queue[idx].state.Clone(),
	}
}

func searchKey(pos []int, st *State) string {
	var b strings.Builder
	for i, p := range pos {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", p)
	}
	b.WriteByte('|')
	b.WriteString(st.key())
	return b.String()
}
