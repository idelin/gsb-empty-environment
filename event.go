package netsec

import (
	"encoding/json"
	"fmt"
)

const eventVersion = 1

const (
	eventStartRound = "round_start"
	eventCommitment = "commitment"
	eventReveal     = "reveal"
	eventTimeout    = "timeout"
)

// event is the durable representation of one accepted external action. The
// result is deliberately not stored: it is a deterministic function of the
// accepted reveal events.
type event struct {
	Version          int      `json:"version"`
	Kind             string   `json:"kind"`
	Round            string   `json:"round"`
	Participant      string   `json:"participant,omitempty"`
	Data             []byte   `json:"data,omitempty"`
	Participants     []string `json:"participants,omitempty"`
	DeadlineUnixNano int64    `json:"deadline_unix_nano,omitempty"`
}

func encodeEvent(ev event) ([]byte, error) {
	ev.Version = eventVersion
	payload, err := json.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("netsec: encode event: %w", err)
	}
	return payload, nil
}

func decodeEvent(payload []byte) (event, error) {
	var ev event
	if err := json.Unmarshal(payload, &ev); err != nil {
		return event{}, fmt.Errorf("%w: decode event: %v", ErrJournalCorrupt, err)
	}
	if ev.Version != eventVersion {
		return event{}, fmt.Errorf("%w: unsupported event version %d", ErrJournalCorrupt, ev.Version)
	}
	if ev.Round == "" {
		return event{}, fmt.Errorf("%w: event has no round", ErrJournalCorrupt)
	}
	switch ev.Kind {
	case eventStartRound, eventCommitment, eventReveal, eventTimeout:
	default:
		return event{}, fmt.Errorf("%w: unknown event kind %q", ErrJournalCorrupt, ev.Kind)
	}
	return ev, nil
}
