package eventgraph

import (
	"fmt"
	"sort"
)

// ViolationKind classifies a message-graph invariant breach.
type ViolationKind int

const (
	// ViolationRecvWithoutSend: a recv names a message no send claims.
	ViolationRecvWithoutSend ViolationKind = iota
	// ViolationDuplicateSend: two sends claim the same message id.
	ViolationDuplicateSend
	// ViolationDuplicateRecv: two recvs claim the same message id.
	ViolationDuplicateRecv
	// ViolationUnmatchedSend: a send's message is never received
	// (reported as informational; the message may be in flight).
	ViolationUnmatchedSend
	// ViolationSeqGap: a process's local sequence numbers are not
	// contiguous from 1, so events are missing from the import.
	ViolationSeqGap
	// ViolationCycle: the associations close a causality cycle.
	ViolationCycle
)

func (k ViolationKind) String() string {
	switch k {
	case ViolationRecvWithoutSend:
		return "recv-without-send"
	case ViolationDuplicateSend:
		return "duplicate-send"
	case ViolationDuplicateRecv:
		return "duplicate-recv"
	case ViolationUnmatchedSend:
		return "unmatched-send"
	case ViolationSeqGap:
		return "seq-gap"
	case ViolationCycle:
		return "cycle"
	}
	return "unknown"
}

// Violation is one broken message-graph invariant.
type Violation struct {
	Kind    ViolationKind
	Message string   // message id involved, if any
	Events  []string // event IDs involved, sorted
	Detail  string
}

// Validate checks the message-graph invariants that must hold for a
// complete, consistent import, regardless of the order events arrived:
//
//   - every recv pairs with exactly one send of the same message id;
//   - every send pairs with at most one recv (zero recvs is reported as
//     informational, since a message may legitimately be in flight);
//   - per-process sequence numbers are contiguous starting at 1;
//   - the happens-before relation is acyclic.
//
// Because edges are linked as endpoints arrive (AddEvent), an out-of-order
// import that is complete and consistent yields zero violations.
func (g *Graph) Validate() []Violation {
	var out []Violation

	for msg, rs := range g.recvs {
		if _, ok := g.sends[msg]; !ok {
			ids := sortedIDs(rs)
			out = append(out, Violation{
				Kind:    ViolationRecvWithoutSend,
				Message: msg,
				Events:  ids,
				Detail:  fmt.Sprintf("message %q received but never sent", msg),
			})
		}
		if len(rs) > 1 {
			out = append(out, Violation{
				Kind:    ViolationDuplicateRecv,
				Message: msg,
				Events:  sortedIDs(rs),
				Detail:  fmt.Sprintf("message %q received %d times", msg, len(rs)),
			})
		}
	}
	for msg, s := range g.sends {
		if len(g.recvs[msg]) == 0 {
			out = append(out, Violation{
				Kind:    ViolationUnmatchedSend,
				Message: msg,
				Events:  []string{s.ID},
				Detail:  fmt.Sprintf("message %q sent but never received", msg),
			})
		}
	}
	for msg, dups := range g.dupSends {
		events := append([]string{g.sends[msg].ID}, sortedIDs(dups)...)
		sort.Strings(events)
		out = append(out, Violation{
			Kind:    ViolationDuplicateSend,
			Message: msg,
			Events:  events,
			Detail:  fmt.Sprintf("message %q sent %d times", msg, len(dups)+1),
		})
	}

	procs := make([]string, 0, len(g.byProc))
	for p := range g.byProc {
		procs = append(procs, p)
	}
	sort.Strings(procs)
	for _, p := range procs {
		seqs := g.byProc[p]
		var max uint64
		for s := range seqs {
			if s > max {
				max = s
			}
		}
		for s := uint64(1); s <= max; s++ {
			if _, ok := seqs[s]; !ok {
				out = append(out, Violation{
					Kind:   ViolationSeqGap,
					Detail: fmt.Sprintf("process %s is missing seq %d (max %d)", p, s, max),
				})
			}
		}
	}

	if _, err := g.TopoSort(); err != nil {
		if ce, ok := err.(*CycleError); ok {
			out = append(out, Violation{
				Kind:   ViolationCycle,
				Events: ce.Cycle,
				Detail: ce.Error(),
			})
		}
	}

	// Deterministic output independent of map iteration order.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Message != out[j].Message {
			return out[i].Message < out[j].Message
		}
		return out[i].Detail < out[j].Detail
	})
	return out
}

func sortedIDs(evs []*Event) []string {
	ids := make([]string, 0, len(evs))
	for _, e := range evs {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	return ids
}
