package evidence

import (
	"encoding/json"
	"io"
	"sync"
)

// Event is one locally synthesized telemetry event. Events are
// recorded in memory only; no monitoring platform is involved.
type Event struct {
	Seq    int    `json:"seq"`
	Type   string `json:"type"`
	Detail string `json:"detail"`
}

// Telemetry is an in-memory event log. It can be serialized to a JSON
// fixture and replayed byte-for-byte for deterministic tests.
type Telemetry struct {
	mu     sync.Mutex
	events []Event
}

func (t *Telemetry) emit(typ, detail string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, Event{Seq: len(t.events) + 1, Type: typ, Detail: detail})
}

// Events returns a copy of the recorded events.
func (t *Telemetry) Events() []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Event, len(t.events))
	copy(out, t.events)
	return out
}

// SaveFixture serializes the event log as a replayable JSON fixture.
func (t *Telemetry) SaveFixture(w io.Writer) error {
	return json.NewEncoder(w).Encode(t.Events())
}

// ReplayFixture loads a fixture previously written by SaveFixture.
func ReplayFixture(r io.Reader) ([]Event, error) {
	var events []Event
	if err := json.NewDecoder(r).Decode(&events); err != nil {
		return nil, err
	}
	return events, nil
}
