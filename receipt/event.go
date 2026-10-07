package receipt

import (
	"encoding/json"
	"fmt"
)

// EventType discriminates the events persisted in the log.
type EventType string

const (
	EventBootstrap EventType = "bootstrap"
	EventReceipt   EventType = "receipt"
	EventSupersede EventType = "supersede"
)

// Event is one entry of the persistent log. Exactly one of Receipt /
// Supersede is set, matching Type.
type Event struct {
	Seq       int64            `json:"seq"`
	Type      EventType        `json:"type"`
	Bootstrap *string          `json:"bootstrap,omitempty"`
	Receipt   *Receipt         `json:"receipt,omitempty"`
	Supersede *SupersedeRecord `json:"supersede,omitempty"`
}

// SupersedeRecord is a committed resolution replacing one execution
// generation by its successor.
type SupersedeRecord struct {
	Old string `json:"old"`
	New string `json:"new"`
}

func (s SupersedeRecord) validate() error {
	if s.Old == "" || s.New == "" {
		return fmt.Errorf("supersede: empty generation")
	}
	if s.Old == s.New {
		return fmt.Errorf("supersede: generation %q cannot supersede itself", s.Old)
	}
	return nil
}

// encodeEvent serializes one event as a single JSON line.
func encodeEvent(e Event) ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func decodeEvent(line []byte) (Event, error) {
	var e Event
	err := json.Unmarshal(line, &e)
	return e, err
}
