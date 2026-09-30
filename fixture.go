package alertseg

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// Fixture is a replayable, locally synthesized telemetry script.
type Fixture struct {
	Name           string        `json:"name"`
	RepeatInterval string        `json:"repeat_interval,omitempty"` // e.g. "10m"
	Steps          []FixtureStep `json:"steps"`
}

// FixtureStep is one timed action: a telemetry event (source+state), a
// suppression toggle (suppress), or a pure clock advance (tick).
type FixtureStep struct {
	At       time.Time `json:"at"`
	Source   string    `json:"source,omitempty"`
	State    State     `json:"state,omitempty"`
	Suppress *bool     `json:"suppress,omitempty"`
	Tick     bool      `json:"tick,omitempty"`
}

func LoadFixture(path string) (*Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Fixture
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %v", path, err)
	}
	return &f, nil
}

// Replay runs a fixture against a fresh monitor and returns it.
func Replay(f *Fixture) (*Monitor, error) {
	var repeat time.Duration
	if f.RepeatInterval != "" {
		d, err := time.ParseDuration(f.RepeatInterval)
		if err != nil {
			return nil, fmt.Errorf("repeat_interval: %v", err)
		}
		repeat = d
	}
	m := NewMonitor(repeat)

	steps := append([]FixtureStep(nil), f.Steps...)
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].At.Before(steps[j].At) })

	for i, st := range steps {
		m.Tick(st.At) // let repeats fire as time advances
		switch {
		case st.Suppress != nil:
			m.SetSuppressed(*st.Suppress, st.At)
		case st.Tick:
			// pure clock advance, already applied above
		case st.Source != "" && st.State != "":
			if st.State != StateOK && st.State != StateFiring {
				return nil, fmt.Errorf("step %d: unknown state %q", i, st.State)
			}
			m.Apply(TelemetryEvent{Time: st.At, Source: st.Source, State: st.State})
		default:
			return nil, fmt.Errorf("step %d: need source+state, suppress, or tick", i)
		}
	}
	return m, nil
}
