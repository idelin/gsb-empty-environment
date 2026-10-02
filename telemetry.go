package logtmpl

import (
	"bufio"
	"encoding/json"
	"io"
	"sort"
)

// TelemetryEvent is a locally synthesized telemetry event. There is no
// external monitoring integration: events are kept in memory and can be
// written to / replayed from JSONL fixtures.
type TelemetryEvent struct {
	Seq    int               `json:"seq"`
	Kind   string            `json:"kind"`
	Fields map[string]string `json:"fields,omitempty"`
}

// Telemetry collects emitted events in order.
type Telemetry struct {
	events []TelemetryEvent
}

// Emit appends a synthesized event.
func (t *Telemetry) Emit(kind string, fields map[string]string) {
	t.events = append(t.events, TelemetryEvent{
		Seq:    len(t.events),
		Kind:   kind,
		Fields: fields,
	})
}

// Events returns a copy of all emitted events.
func (t *Telemetry) Events() []TelemetryEvent {
	return append([]TelemetryEvent(nil), t.events...)
}

// WriteFixture serializes events as JSON lines for later replay.
func WriteFixture(w io.Writer, events []TelemetryEvent) error {
	enc := json.NewEncoder(w)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return nil
}

// ReadFixture loads events from a JSONL fixture.
func ReadFixture(r io.Reader) ([]TelemetryEvent, error) {
	var events []TelemetryEvent
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev TelemetryEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, sc.Err()
}

// ReplayTemplates reconstructs the live template set from a telemetry
// event stream (template_add / template_retract). It returns the sorted
// signatures of live templates. This lets a fixture replay verify that a
// run's telemetry fully determines its final state.
func ReplayTemplates(events []TelemetryEvent) []string {
	live := map[string]string{}
	for _, ev := range events {
		switch ev.Kind {
		case "template_add":
			live[ev.Fields["id"]] = ev.Fields["signature"]
		case "template_retract":
			delete(live, ev.Fields["id"])
		}
	}
	sigs := make([]string, 0, len(live))
	for _, s := range live {
		sigs = append(sigs, s)
	}
	sort.Strings(sigs)
	return sigs
}
