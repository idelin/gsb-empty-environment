// Package barrierpipe implements a streaming pipeline whose multi-input
// operators perform barrier-aligned snapshots: every input carries barrier
// sequence numbers, and an operator may snapshot barrier n only after all
// active inputs have arrived at barrier n. Inputs that reach a barrier early
// are paused (no data advancement) until alignment completes; a closed input
// counts as having arrived at every subsequent barrier.
package barrierpipe

import (
	"fmt"
	"math"
	"reflect"
)

type EventKind int

const (
	DataEvent EventKind = iota
	BarrierEvent
)

// Record is a data record. Gen is the barrier generation it belongs to:
// records read before barrier n on an input have Gen <= n.
type Record struct {
	Src   int
	Gen   int
	Value int
}

type Event struct {
	Kind    EventKind
	Barrier int // valid when Kind == BarrierEvent
	Rec     Record
}

func Data(src, gen, value int) Event {
	return Event{Kind: DataEvent, Rec: Record{Src: src, Gen: gen, Value: value}}
}

func Barrier(seq int) Event {
	return Event{Kind: BarrierEvent, Barrier: seq}
}

// Snapshot is the operator state captured when a barrier aligns.
type Snapshot struct {
	Barrier  int
	Consumed []int // per-input records consumed at snapshot time
}

// Aligner is the barrier-alignment state machine of a multi-input operator.
// It is not safe for concurrent use; serialize calls per operator.
type Aligner struct {
	NumInputs int

	arrived  []int  // highest barrier seq arrived per input
	closed   []bool // closed inputs count as arrived at all later barriers
	blocked  []bool // inputs paused at the pending barrier
	pending  int    // barrier currently being aligned, 0 = none
	consumed []int

	Snapshots []Snapshot
}

func NewAligner(numInputs int) *Aligner {
	return &Aligner{
		NumInputs: numInputs,
		arrived:   make([]int, numInputs),
		closed:    make([]bool, numInputs),
		blocked:   make([]bool, numInputs),
		consumed:  make([]int, numInputs),
	}
}

// Blocked reports whether input i is paused at the pending barrier; the
// caller must not deliver further data events from it until it unblocks.
func (a *Aligner) Blocked(i int) bool { return a.blocked[i] }

func (a *Aligner) Closed(i int) bool { return a.closed[i] }

// OnEvent feeds one event from input i and returns the events to emit
// downstream (the data record itself, plus a barrier once it aligns).
func (a *Aligner) OnEvent(i int, ev Event) []Event {
	switch ev.Kind {
	case DataEvent:
		if a.blocked[i] {
			panic(fmt.Sprintf("aligner: input %d advanced data while blocked at barrier %d", i, a.pending))
		}
		a.consumed[i]++
		return []Event{ev}
	case BarrierEvent:
		if a.pending != 0 && ev.Barrier != a.pending {
			panic(fmt.Sprintf("aligner: input %d delivered barrier %d while barrier %d is pending", i, ev.Barrier, a.pending))
		}
		a.arrived[i] = ev.Barrier
		a.blocked[i] = true
		a.pending = ev.Barrier
		return a.align()
	}
	return nil
}

// Close marks input i as closed: it counts as arrived at the pending and
// every subsequent barrier.
func (a *Aligner) Close(i int) []Event {
	a.closed[i] = true
	a.blocked[i] = false
	a.arrived[i] = math.MaxInt
	return a.align()
}

func (a *Aligner) align() []Event {
	var out []Event
	for a.pending != 0 && a.allArrived(a.pending) {
		snap := Snapshot{Barrier: a.pending, Consumed: append([]int(nil), a.consumed...)}
		a.Snapshots = append(a.Snapshots, snap)
		out = append(out, Barrier(a.pending))
		for i := 0; i < a.NumInputs; i++ {
			if !a.closed[i] && a.arrived[i] == a.pending {
				a.blocked[i] = false
			}
		}
		a.pending = 0
	}
	return out
}

func (a *Aligner) allArrived(barrier int) bool {
	for i := 0; i < a.NumInputs; i++ {
		if a.closed[i] {
			continue
		}
		if a.arrived[i] < barrier {
			return false
		}
	}
	return true
}

// Operator runs an Aligner against live input channels, applying
// backpressure by simply not selecting on blocked inputs.
type Operator struct {
	al     *Aligner
	inputs []<-chan Event
	Output chan Event

	// MaxBacklog records the maximum pending-events depth observed per
	// input channel; it can never exceed the channel capacity.
	MaxBacklog []int
}

func NewOperator(inputs []<-chan Event, outBuf int) *Operator {
	return &Operator{
		al:         NewAligner(len(inputs)),
		inputs:     inputs,
		Output:     make(chan Event, outBuf),
		MaxBacklog: make([]int, len(inputs)),
	}
}

func (op *Operator) Aligner() *Aligner { return op.al }

func (op *Operator) Run() {
	n := len(op.inputs)
	active := make([]bool, n)
	for i := range active {
		active[i] = true
	}
	remaining := n
	for remaining > 0 {
		var cases []reflect.SelectCase
		var idx []int
		for i := 0; i < n; i++ {
			if active[i] && !op.al.Blocked(i) {
				cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(op.inputs[i])})
				idx = append(idx, i)
			}
		}
		if len(cases) == 0 {
			panic("operator stalled: every active input is blocked")
		}
		chosen, val, ok := reflect.Select(cases)
		i := idx[chosen]
		var out []Event
		if !ok {
			active[i] = false
			remaining--
			out = op.al.Close(i)
		} else {
			out = op.al.OnEvent(i, val.Interface().(Event))
		}
		for _, ev := range out {
			op.Output <- ev
		}
		for j := 0; j < n; j++ {
			if l := len(op.inputs[j]); l > op.MaxBacklog[j] {
				op.MaxBacklog[j] = l
			}
		}
	}
	close(op.Output)
}

// Source emits plan[g] records of generation g+1 followed by Barrier(g+1)
// for each g, then closes out. If closeAfterBarrier > 0 the source closes
// early, right after emitting that barrier. delay, if non-nil, is invoked
// before each record to shuffle interleaving across inputs.
func Source(id int, plan []int, valueBase int, delay func(), closeAfterBarrier int, out chan<- Event) {
	defer close(out)
	for g, count := range plan {
		for k := 0; k < count; k++ {
			if delay != nil {
				delay()
			}
			out <- Data(id, g+1, valueBase+k)
		}
		out <- Barrier(g + 1)
		if closeAfterBarrier == g+1 {
			return
		}
	}
}

// VerifyDownstream checks that records do not cross barrier generations:
// the downstream stream must be gen-1 records, Barrier(1), gen-2 records,
// Barrier(2), and so on up to numBarriers.
func VerifyDownstream(events []Event, numBarriers int) error {
	gen := 1
	nextBarrier := 1
	for _, ev := range events {
		switch ev.Kind {
		case BarrierEvent:
			if ev.Barrier != nextBarrier {
				return fmt.Errorf("barrier %d out of order, want %d", ev.Barrier, nextBarrier)
			}
			nextBarrier++
			gen++
		case DataEvent:
			if ev.Rec.Gen != gen {
				return fmt.Errorf("record (src=%d gen=%d val=%d) leaked into downstream generation %d",
					ev.Rec.Src, ev.Rec.Gen, ev.Rec.Value, gen)
			}
		}
	}
	if nextBarrier != numBarriers+1 {
		return fmt.Errorf("saw %d barriers, want %d", nextBarrier-1, numBarriers)
	}
	return nil
}
