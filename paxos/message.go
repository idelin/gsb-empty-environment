package paxos

import (
	"fmt"
	"strings"
)

type MsgKind int

const (
	MsgPrepare MsgKind = iota
	MsgPromise
	MsgAccept
	MsgAccepted
	MsgChosen
)

func (k MsgKind) String() string {
	switch k {
	case MsgPrepare:
		return "Prepare"
	case MsgPromise:
		return "Promise"
	case MsgAccept:
		return "Accept"
	case MsgAccepted:
		return "Accepted"
	case MsgChosen:
		return "Chosen"
	}
	return "?"
}

// AcceptedEntry is one acceptor's accepted (ballot, value) for a slot.
type AcceptedEntry struct {
	Ballot Ballot
	Value  string
}

type Message struct {
	Kind     MsgKind
	From     int
	To       int
	Ballot   Ballot
	Slot     int
	Value    string
	Accepted map[int]AcceptedEntry // Promise only: acceptor's accepted table
}

func (m Message) String() string {
	head := fmt.Sprintf("%d->%d %s", m.From, m.To, m.Kind)
	switch m.Kind {
	case MsgPrepare:
		return fmt.Sprintf("%s b=%s", head, m.Ballot)
	case MsgPromise:
		return fmt.Sprintf("%s b=%s accepted=%s", head, m.Ballot, formatAccepted(m.Accepted))
	case MsgAccept, MsgAccepted:
		return fmt.Sprintf("%s b=%s slot=%d v=%s", head, m.Ballot, m.Slot, printable(m.Value))
	case MsgChosen:
		return fmt.Sprintf("%s slot=%d v=%s", head, m.Slot, printable(m.Value))
	}
	return head
}

func formatAccepted(m map[int]AcceptedEntry) string {
	if len(m) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(m))
	for _, slot := range sortedKeys(m) {
		parts = append(parts, fmt.Sprintf("%d:%s@%s", slot, printable(m[slot].Value), m[slot].Ballot))
	}
	return "{" + strings.Join(parts, " ") + "}"
}
