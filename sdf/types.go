// Package sdf implements exact bounded-buffer analysis and periodic
// scheduling for synchronous dataflow graphs used by a hardware inference
// runtime.
//
// A firing of an actor consumes a fixed number of tokens on every incoming
// edge when it starts and produces a fixed number of tokens on every outgoing
// edge only when it completes. Feedback edges may be preloaded with delay
// tokens. A single shared compute unit runs at most one firing at a time; DMA
// runs concurrently but every in-flight firing reserves one of a finite pool
// of descriptors for its whole lifetime. Edge buffers are statically sized
// and the output slots of an in-flight firing are reserved at launch.
package sdf

import (
	"errors"
	"math/big"
)

// Edge is a directed FIFO connection.
//
// Exactly one of Src/Dst may be negative: a negative Src marks an external
// input edge fed by an Input pattern; a negative Dst marks an external output
// edge whose produced tokens count towards the sliding-window constraint.
// Both endpoints internal means a normal (possibly feedback) edge.
type Edge struct {
	Src   int // source actor id, -1 for an external input
	Dst   int // destination actor id, -1 for an external output
	Prod  int // tokens produced per firing of Src (internal source only)
	Cons  int // tokens consumed per firing of Dst (internal destination only)
	Delay int // preloaded delay tokens
	Cap   int // buffer capacity in token slots; 0 lets the solver choose
}

// IsInput reports whether the edge carries an external periodic input.
func (e Edge) IsInput() bool { return e.Src < 0 }

// IsOutput reports whether the edge carries an externally committed output.
func (e Edge) IsOutput() bool { return e.Dst < 0 }

// Input describes an ultimately periodic stream of external input tokens as
// long runs. Every cycle lasts Period time units and delivers Count tokens
// spread over Runs; Runs repeat forever. A run emits its tokens one per time
// unit starting at FirstTime; successive runs start Gap time units after the
// previous run's first token. Long idle gaps are represented symbolically
// without per-tick enumeration.
type Input struct {
	EdgeID    int
	Period    int64
	Count     int64 // tokens per period
	FirstTime int64
	Gap       int64 // start-to-start gap between consecutive runs
	Runs      int64 // number of runs per period
}

// Resources model the shared execution hardware.
type Resources struct {
	// Descriptors is the size of the shared DMA descriptor pool. Every
	// in-flight firing holds one descriptor from launch to completion.
	Descriptors int
}

// Problem is a fully specified bounded-buffer scheduling question.
type Problem struct {
	Actors  int
	Edges   []Edge
	Cost    []int64 // firing compute cost per actor (time units, >= 1)
	Inputs  []Input
	Res     Resources
	WindowN int64 // every N input tokens ...
	WindowM int64 // ... must have at least M outputs committed
	MaxCap  int   // per-edge capacity upper bound (hardware limit)
}

// Status classifies the outcome of an analysis.
type Status int

const (
	StatusUnknown Status = iota
	// StatusFeasible: a bounded-capacity periodic schedule exists and the
	// sliding-window constraint holds.
	StatusFeasible
	// StatusInconsistent: no positive repetition vector exists, so no finite
	// buffer can sustain unbounded execution.
	StatusInconsistent
	// StatusDeadlock: the graph blocks because an edge cannot accumulate
	// enough tokens (e.g. a feedback edge with insufficient initial delay).
	StatusDeadlock
	// StatusOverrun: no capacity up to MaxCap prevents a buffer from
	// overflowing under the offered periodic input (output space stays
	// reserved by another in-flight firing).
	StatusOverrun
	// StatusWindow: liveness is achievable but the sliding-window constraint
	// is violated even though the long-run average rate may be sufficient.
	StatusWindow
	// StatusInconclusive: the exact search terminated its safety envelope
	// without proving a state repeat. Never reported as stability.
	StatusInconclusive
)

func (s Status) String() string {
	switch s {
	case StatusFeasible:
		return "feasible"
	case StatusInconsistent:
		return "inconsistent"
	case StatusDeadlock:
		return "deadlock"
	case StatusOverrun:
		return "overrun"
	case StatusWindow:
		return "window-violation"
	case StatusInconclusive:
		return "inconclusive"
	default:
		return "unknown"
	}
}

// EventKind enumerates schedule event types.
type EventKind int

const (
	EventInputArrive EventKind = iota
	EventLaunch
	EventComplete
	EventOutputCommit
)

func (k EventKind) String() string {
	switch k {
	case EventInputArrive:
		return "input"
	case EventLaunch:
		return "launch"
	case EventComplete:
		return "complete"
	case EventOutputCommit:
		return "output"
	default:
		return "?"
	}
}

// Event is one entry of a replayable schedule. Times are absolute; counts are
// cumulative over the displayed segment (prefix + one steady period).
type Event struct {
	Time       int64
	Kind       EventKind
	Actor      int   // for Launch/Complete
	Edge       int   // for InputArrive/OutputCommit
	Tokens     int   // token delta carried by the event
	InInputs   int64 // cumulative external inputs up to this event
	OutOutputs int64 // cumulative external outputs up to this event
}

// Schedule is an eventually periodic, symbolically replayable schedule.
//
// PrefixEvents occur once; CycleEvents then repeat forever with time shifted
// by multiples of CycleTime. The steady-state repetition vector q gives the
// firings of each actor in one cycle and CycleInputs/CycleOutputs give the
// token balance.
type Schedule struct {
	PrefixEvents []Event
	CycleEvents  []Event
	CycleTime    int64
	CycleInputs  int64
	CycleOutputs int64
	Repetition   []int64
	CycleStart   int64 // absolute time anchoring CycleEvents[0]
	caps         []int // capacities the schedule was synthesized against
}

// Certificate is the machine-checkable witness that the reported capacity
// vector cannot be reduced any further.
type Certificate struct {
	// EdgeLowerBound[e] is the smallest capacity (0..MaxCap) for which edge e
	// alone admits unbounded token accumulation without overflow.
	EdgeLowerBound []int
	// Reduction[e] is true when decrementing capacity e by one from the
	// reported optimum already makes the whole problem infeasible. Every edge
	// whose capacity exceeds its structural lower bound is covered this way;
	// edges at the bound are certified by LowerBoundWitness.
	Reduction []bool
	// LowerBoundWitness[e] explains why EdgeLowerBound[e] cannot be smaller:
	// the maximum backlog reachable in the isolated edge analysis when the
	// capacity is EdgeLowerBound[e]-1.
	LowerBoundWitness []int
	// Enumeration is the number of capacity vectors (in total-then-
	// lexicographic order) proven infeasible before the optimum was reached.
	Enumeration int
	// ConsistencyRank holds the rank evidence for the repetition vector.
	ConsistencyRank int
}

// Result is the full analysis output.
type Result struct {
	Status   Status
	Reason   string
	Cap      []int // chosen capacity per edge; nil when infeasible
	TotalCap int
	Schedule *Schedule
	Cert     *Certificate
	// Repetition is the smallest positive integer firing balance vector.
	Repetition []int64
}

// ErrMalformed is returned for structurally invalid problem descriptions.
var ErrMalformed = errors.New("sdf: malformed problem")

// ErrInconclusive marks a Result with StatusInconclusive.
var ErrInconclusive = errors.New("sdf: exact search envelope exhausted without a state repeat")

// validate normalizes and checks the problem and returns derived adjacency
// information used by every analysis stage.
func (p *Problem) validate() (*index, error) {
	if p.Actors <= 0 || p.Actors > 64 {
		return nil, errors.New("sdf: actor count out of range")
	}
	if len(p.Cost) != p.Actors {
		return nil, errors.New("sdf: one firing cost per actor required")
	}
	for _, c := range p.Cost {
		if c <= 0 {
			return nil, errors.New("sdf: firing costs must be positive")
		}
	}
	if p.Res.Descriptors < 1 {
		return nil, errors.New("sdf: at least one DMA descriptor required")
	}
	if p.WindowN <= 0 || p.WindowM < 0 || p.WindowM > p.WindowN {
		return nil, errors.New("sdf: window requires 0 <= M <= N and N > 0")
	}
	if p.MaxCap < 1 {
		return nil, errors.New("sdf: MaxCap must be positive")
	}
	idx := &index{
		in:       make([][]int, p.Actors),
		out:      make([][]int, p.Actors),
		edgeIn:   make([]int, len(p.Edges)),
		edgeOut:  make([]int, len(p.Edges)),
		inputPat: map[int]Input{},
	}
	seenInput := map[int]bool{}
	for eid, e := range p.Edges {
		if e.Prod < 0 || e.Cons < 0 || e.Delay < 0 {
			return nil, ErrMalformed
		}
		switch {
		case e.IsInput() && e.IsOutput():
			return nil, errors.New("sdf: edge cannot be both external input and output")
		case e.IsInput():
			if e.Dst < 0 || e.Dst >= p.Actors || e.Prod != 0 || e.Cons <= 0 {
				return nil, ErrMalformed
			}
			idx.in[e.Dst] = append(idx.in[e.Dst], eid)
			idx.edgeIn[eid] = e.Dst
			idx.edgeOut[eid] = -1
		case e.IsOutput():
			if e.Src < 0 || e.Src >= p.Actors || e.Cons != 0 || e.Prod <= 0 {
				return nil, ErrMalformed
			}
			idx.out[e.Src] = append(idx.out[e.Src], eid)
			idx.edgeOut[eid] = e.Src
			idx.edgeIn[eid] = -1
		default:
			if e.Src < 0 || e.Src >= p.Actors || e.Dst < 0 || e.Dst >= p.Actors || e.Prod <= 0 || e.Cons <= 0 {
				return nil, ErrMalformed
			}
			idx.out[e.Src] = append(idx.out[e.Src], eid)
			idx.in[e.Dst] = append(idx.in[e.Dst], eid)
			idx.edgeOut[eid] = e.Src
			idx.edgeIn[eid] = e.Dst
		}
		if e.Cap < 0 {
			return nil, errors.New("sdf: negative edge capacity")
		}
		if e.Delay > p.MaxCap || (e.Cap != 0 && e.Delay > e.Cap) {
			return nil, errors.New("sdf: delay exceeds capacity")
		}
	}
	for _, in := range p.Inputs {
		if in.EdgeID < 0 || in.EdgeID >= len(p.Edges) || !p.Edges[in.EdgeID].IsInput() {
			return nil, errors.New("sdf: input pattern must target an input edge")
		}
		if seenInput[in.EdgeID] {
			return nil, errors.New("sdf: duplicate input pattern")
		}
		seenInput[in.EdgeID] = true
		if in.Period <= 0 || in.Count <= 0 || in.Runs <= 0 || in.Gap <= 0 {
			return nil, errors.New("sdf: input pattern requires positive period/count/runs/gap")
		}
		if in.Count < in.Runs {
			return nil, errors.New("sdf: at least one token per run")
		}
		if safeMul(in.Gap, in.Runs-1) > in.Period {
			return nil, errors.New("sdf: input runs exceed period")
		}
		idx.inputPat[in.EdgeID] = in
	}
	for eid, e := range p.Edges {
		if e.IsInput() && !seenInput[eid] {
			return nil, errors.New("sdf: every input edge needs an input pattern")
		}
	}
	return idx, nil
}

// index holds derived adjacency.
type index struct {
	in       [][]int // actor -> incoming edge ids
	out      [][]int // actor -> outgoing edge ids
	edgeIn   []int   // edge -> destination actor (-1 for output edges)
	edgeOut  []int   // edge -> source actor (-1 for input edges)
	inputPat map[int]Input
}

func safeMul(a, b int64) int64 {
	r := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	if !r.IsInt64() {
		return 1<<63 - 1
	}
	return r.Int64()
}
