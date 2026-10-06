// Package eventgraph builds a causal partial-order (happens-before) graph
// from distributed-system events. Causality is derived exclusively from
// per-process local sequence numbers and send/receive message associations;
// wall-clock timestamps are carried for display only and never influence
// ordering.
package eventgraph

import (
	"fmt"
	"time"
)

// Kind classifies an event.
type Kind int

const (
	KindLocal Kind = iota
	KindSend
	KindRecv
)

func (k Kind) String() string {
	switch k {
	case KindSend:
		return "send"
	case KindRecv:
		return "recv"
	default:
		return "local"
	}
}

// Event is a single occurrence on a process. ID must be globally unique.
// Seq is the process-local sequence number: within one Process, the event
// with Seq n happens-before the event with Seq n+1. Send and Recv events
// are paired through Message. WallClock is display-only metadata.
type Event struct {
	ID        string
	Process   string
	Seq       uint64
	Kind      Kind
	Message   string
	WallClock time.Time
}

// sameCausalContent reports whether two events with the same ID describe
// the same causal facts. WallClock is deliberately excluded: differing
// display timestamps do not constitute a contradiction.
func sameCausalContent(a, b *Event) bool {
	return a.Process == b.Process &&
		a.Seq == b.Seq &&
		a.Kind == b.Kind &&
		a.Message == b.Message
}

func (e *Event) String() string {
	s := fmt.Sprintf("%s{%s#%d %s", e.ID, e.Process, e.Seq, e.Kind)
	if e.Message != "" {
		s += " msg=" + e.Message
	}
	return s + "}"
}
