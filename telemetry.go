package clocksync

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// EventKind classifies telemetry events.
type EventKind string

const (
	Send EventKind = "send"
	Recv EventKind = "recv"
)

// Event is a single telemetry record. Timestamps are taken from the
// local clock of the emitting node; events are synthesized locally and
// replayed from fixtures.
type Event struct {
	Node string    `json:"node"`
	Kind EventKind `json:"kind"`
	Msg  string    `json:"msg"`
	TS   float64   `json:"ts"`
}

// Fixture is a replayable telemetry capture.
type Fixture struct {
	Nodes  []string `json:"nodes"`
	Events []Event  `json:"events"`
}

// LoadFixture decodes a fixture from r.
func LoadFixture(r io.Reader) (Fixture, error) {
	var f Fixture
	if err := json.NewDecoder(r).Decode(&f); err != nil {
		return Fixture{}, fmt.Errorf("decode fixture: %w", err)
	}
	return f, nil
}

// Message pairs a send event with its matching recv event.
type Message struct {
	ID             string
	From, To       string
	SendTS, RecvTS float64
}

// PairMessages replays events and matches send/recv pairs by message id.
func PairMessages(events []Event) ([]Message, error) {
	pending := make(map[string]Event)
	var msgs []Message
	for _, ev := range events {
		switch ev.Kind {
		case Send:
			if _, dup := pending[ev.Msg]; dup {
				return nil, fmt.Errorf("duplicate send for message %q", ev.Msg)
			}
			pending[ev.Msg] = ev
		case Recv:
			send, ok := pending[ev.Msg]
			if !ok {
				return nil, fmt.Errorf("recv without send for message %q", ev.Msg)
			}
			delete(pending, ev.Msg)
			msgs = append(msgs, Message{
				ID:     ev.Msg,
				From:   send.Node,
				To:     ev.Node,
				SendTS: send.TS,
				RecvTS: ev.TS,
			})
		default:
			return nil, fmt.Errorf("unknown event kind %q", ev.Kind)
		}
	}
	if len(pending) > 0 {
		ids := make([]string, 0, len(pending))
		for id := range pending {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("send without recv for message %q", ids[0])
	}
	return msgs, nil
}

// Nodes returns the union of explicit nodes and message endpoints,
// in first-seen order.
func Nodes(explicit []string, msgs []Message) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, n := range explicit {
		add(n)
	}
	for _, m := range msgs {
		add(m.From)
		add(m.To)
	}
	return out
}
