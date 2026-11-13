package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// DefaultMaxDepth bounds explored sequences to 12 messages.
const DefaultMaxDepth = 12

// ErrNondeterministic marks a device that produced inconsistent outcomes
// for the same state and message.
var ErrNondeterministic = errors.New("netsec: nondeterministic device implementation")

// ErrStateNotSerializable marks a device state that failed the
// serialization round-trip or deterministic-encoding check.
var ErrStateNotSerializable = errors.New("netsec: device state not serializable")

// Outcome is one observed device reaction, used in nondeterminism reports.
type Outcome struct {
	Next  State
	Wrote bool
}

// NondeterminismError describes a transition on which the device disagreed
// with itself. A security finding must never be derived from it.
type NondeterminismError struct {
	State  State
	Msg    Message
	First  Outcome
	Second Outcome
}

func (e *NondeterminismError) Error() string {
	return fmt.Sprintf("%v: state %s msg %s: first=%+v second=%+v",
		ErrNondeterministic, e.State, e.Msg, e.First, e.Second)
}

// Unwrap exposes ErrNondeterministic for errors.Is.
func (e *NondeterminismError) Unwrap() error { return ErrNondeterministic }

// SerializationError describes a state that is not serializable or whose
// encoding is not deterministic.
type SerializationError struct {
	State  State
	Detail string
}

func (e *SerializationError) Error() string {
	return fmt.Sprintf("%v: state %s: %s", ErrStateNotSerializable, e.State, e.Detail)
}

// Unwrap exposes ErrStateNotSerializable for errors.Is.
func (e *SerializationError) Unwrap() error { return ErrStateNotSerializable }

// StepRecord is one explored transition with before/after snapshots of the
// device state plus the parallel reference run.
type StepRecord struct {
	Depth     int     `json:"depth"`
	Msg       Message `json:"msg"`
	ProofOK   bool    `json:"proofOK"`
	Before    State   `json:"before"`
	After     State   `json:"after"`
	RefBefore State   `json:"refBefore"`
	RefAfter  State   `json:"refAfter"`
	Wrote     bool    `json:"wrote"`
	RefWrote  bool    `json:"refWrote"`
	// BeforeSnapshot and AfterSnapshot are the canonical serialized
	// device-state snapshots taken around this step.
	BeforeSnapshot []byte `json:"beforeSnapshot"`
	AfterSnapshot  []byte `json:"afterSnapshot"`
}

// Node is a search-frontier position: the paired device/reference states
// and the trace that reached them.
type Node struct {
	DUT   State
	Ref   State
	Trace []StepRecord
}

// Depth returns the number of messages consumed to reach the node.
func (n Node) Depth() int { return len(n.Trace) }

// Vulnerability is a shortest found sequence on which the device executed a
// Write that the reference model denies.
type Vulnerability struct {
	Trace []StepRecord
}

// Messages returns just the message sequence of the finding.
func (v *Vulnerability) Messages() []Message {
	msgs := make([]Message, len(v.Trace))
	for i, rec := range v.Trace {
		msgs[i] = rec.Msg
	}
	return msgs
}

func (v *Vulnerability) String() string {
	var buf bytes.Buffer
	for i, rec := range v.Trace {
		if i > 0 {
			buf.WriteString(" -> ")
		}
		buf.WriteString(rec.Msg.String())
	}
	return buf.String()
}

// Result summarizes one exploration run.
type Result struct {
	// Vulnerability is the shortest offending sequence, nil when none was
	// found. It is always nil when a nondeterminism or serialization error
	// is returned alongside the result.
	Vulnerability *Vulnerability
	// Complete is true when the search space within MaxDepth was exhausted
	// without finding a vulnerability.
	Complete bool
	// BudgetExhausted is true when the run stopped because the expansion
	// budget ran out; Frontier then holds the preserved search frontier.
	BudgetExhausted bool
	// Expanded counts expanded nodes.
	Expanded int
	// Frontier holds unexpanded nodes: the preserved search frontier after
	// budget exhaustion (feed back via Options.Frontier to resume), plus
	// nodes parked at the depth limit.
	Frontier []Node
}

// Options configures Explore. The zero value is valid.
type Options struct {
	// MaxDepth bounds sequence length; defaults to DefaultMaxDepth (12).
	MaxDepth int
	// Budget bounds node expansions; 0 means unlimited.
	Budget int
	// Verifier judges Proof signature bits; defaults to FixedBitVerifier.
	Verifier Verifier
	// Frontier resumes a previous budget-exhausted run. Note that the
	// visited set is not preserved across runs, so resumed exploration may
	// revisit state pairs; findings remain correct and minimal for the
	// resumed portion.
	Frontier []Node
}

func (o *Options) withDefaults() Options {
	cfg := Options{}
	if o != nil {
		cfg = *o
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = DefaultMaxDepth
	}
	if cfg.Verifier == nil {
		cfg.Verifier = FixedBitVerifier{}
	}
	return cfg
}

type statePair struct {
	dut State
	ref State
}

// snapshot serializes a device state and verifies the encoding is
// deterministic and round-trips losslessly.
func snapshot(st State) ([]byte, error) {
	if err := st.Validate(); err != nil {
		return nil, &SerializationError{State: st, Detail: err.Error()}
	}
	first, err := json.Marshal(st)
	if err != nil {
		return nil, &SerializationError{State: st, Detail: err.Error()}
	}
	second, err := json.Marshal(st)
	if err != nil || !bytes.Equal(first, second) {
		return nil, &SerializationError{State: st, Detail: "encoding is not deterministic"}
	}
	var back State
	if err := json.Unmarshal(first, &back); err != nil || back != st {
		return nil, &SerializationError{State: st, Detail: "serialization round-trip mismatch"}
	}
	return first, nil
}

// Explore runs a breadth-first search over message sequences of up to
// MaxDepth messages, looking for the shortest sequence on which the device
// under test executes a Write that the reference model denies. Messages are
// enumerated in OrderedMessages order. Divergent internal states that
// reject Writes identically are not reported.
func Explore(dev Device, opts *Options) (*Result, error) {
	cfg := opts.withDefaults()
	res := &Result{}

	visited := make(map[statePair]bool)
	var queue []Node
	if len(cfg.Frontier) > 0 {
		queue = append(queue, cfg.Frontier...)
		for _, n := range queue {
			visited[statePair{dut: n.DUT, ref: n.Ref}] = true
		}
	} else {
		init := InitialState()
		queue = []Node{{DUT: init, Ref: init}}
		visited[statePair{dut: init, ref: init}] = true
	}

	msgs := OrderedMessages()

	for len(queue) > 0 {
		if cfg.Budget > 0 && res.Expanded >= cfg.Budget {
			res.BudgetExhausted = true
			res.Frontier = append(res.Frontier, queue...)
			return res, nil
		}
		node := queue[0]
		queue = queue[1:]

		depth := node.Depth()
		if depth >= cfg.MaxDepth {
			// Parked at the depth limit; kept for the caller's visibility.
			res.Frontier = append(res.Frontier, node)
			continue
		}
		res.Expanded++

		for _, msg := range msgs {
			ok := cfg.Verifier.Verify(msg)

			next1, wrote1 := dev.Step(node.DUT, msg, ok)
			next2, wrote2 := dev.Step(node.DUT, msg, ok)
			if next1 != next2 || wrote1 != wrote2 {
				return res, &NondeterminismError{
					State:  node.DUT,
					Msg:    msg,
					First:  Outcome{Next: next1, Wrote: wrote1},
					Second: Outcome{Next: next2, Wrote: wrote2},
				}
			}

			beforeSnap, err := snapshot(node.DUT)
			if err != nil {
				return res, err
			}
			afterSnap, err := snapshot(next1)
			if err != nil {
				return res, err
			}

			refNext, refWrote := ReferenceStep(node.Ref, msg, ok)

			rec := StepRecord{
				Depth:          depth + 1,
				Msg:            msg,
				ProofOK:        ok,
				Before:         node.DUT,
				After:          next1,
				RefBefore:      node.Ref,
				RefAfter:       refNext,
				Wrote:          wrote1,
				RefWrote:       refWrote,
				BeforeSnapshot: beforeSnap,
				AfterSnapshot:  afterSnap,
			}

			if msg.Kind == KindWrite && wrote1 && !refWrote {
				trace := make([]StepRecord, 0, depth+1)
				trace = append(trace, node.Trace...)
				trace = append(trace, rec)
				res.Vulnerability = &Vulnerability{Trace: trace}
				return res, nil
			}

			pair := statePair{dut: next1, ref: refNext}
			if visited[pair] {
				continue
			}
			visited[pair] = true
			trace := make([]StepRecord, 0, depth+1)
			trace = append(trace, node.Trace...)
			trace = append(trace, rec)
			queue = append(queue, Node{DUT: next1, Ref: refNext, Trace: trace})
		}
	}

	res.Complete = true
	return res, nil
}
