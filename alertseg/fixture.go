package alertseg

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"time"
)

// Fixture is a replayable scenario: synthesized telemetry events plus the
// notification policy under which they are replayed.
type Fixture struct {
	Name           string   `json:"name"`
	RepeatInterval duration `json:"repeat_interval"`
	Suppressions   []window `json:"suppressions"`
	Events         []event  `json:"events"`
	// AdvanceTo moves virtual time past the last event so that pending
	// repeats and suppression lifts are flushed. Optional.
	AdvanceTo timestamp `json:"advance_to"`
}

type duration struct{ time.Duration }

func (d *duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		d.Duration = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("bad duration %q: %v", s, err)
	}
	d.Duration = v
	return nil
}

type timestamp struct{ time.Time }

func (t *timestamp) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		t.Time = time.Time{}
		return nil
	}
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fmt.Errorf("bad timestamp %q: %v", s, err)
	}
	t.Time = v
	return nil
}

type window struct {
	Start timestamp `json:"start"`
	End   timestamp `json:"end"`
}

type event struct {
	At    timestamp `json:"at"`
	State string    `json:"state"`
}

// LoadFixture reads a fixture file.
func LoadFixture(path string) (*Fixture, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Fixture
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	return &f, nil
}

// Replay runs the fixture through a fresh engine and returns it.
func (f *Fixture) Replay() (*Engine, error) {
	windows := make([]Window, len(f.Suppressions))
	for i, w := range f.Suppressions {
		windows[i] = Window{Start: w.Start.Time, End: w.End.Time}
	}
	eng := NewEngine(f.RepeatInterval.Duration, windows)
	var last time.Time
	for i, ev := range f.Events {
		st, err := ParseState(ev.State)
		if err != nil {
			return nil, fmt.Errorf("event %d: %v", i, err)
		}
		if ev.At.Time.Before(last) {
			return nil, fmt.Errorf("event %d: out of order at %s", i, ev.At.Time)
		}
		last = ev.At.Time
		eng.Apply(Event{At: ev.At.Time, State: st})
	}
	end := f.AdvanceTo.Time
	if end.IsZero() {
		end = last
	}
	eng.AdvanceTo(end)
	return eng, nil
}
