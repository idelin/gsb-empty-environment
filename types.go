package main

import (
	"encoding/json"
	"time"
)

// Fault is a known (ground-truth) fault event injected into the simulator.
type Fault struct {
	ID      string     `json:"id"`
	Entity  string     `json:"entity"`
	Symptom string     `json:"symptom"`
	Start   time.Time  `json:"start"`
	End     *time.Time `json:"end,omitempty"` // nil => still open (right-censored at obs end)
}

// Alert is an observed symptom raised by telemetry.
type Alert struct {
	ID      string    `json:"id"`
	Entity  string    `json:"entity"`
	Symptom string    `json:"symptom"`
	Time    time.Time `json:"time"`
}

// Duration is a time.Duration that unmarshals from strings like "20m".
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Config holds the matching rules.
type Config struct {
	// MaxLead: an alert may fire at most this long before fault start and
	// still count as an early detection (lead), unless EarlyAsFP is set.
	MaxLead Duration `json:"maxLead"`
	// MaxLag: an alert may fire at most this long after fault end.
	MaxLag Duration `json:"maxLag"`
	// EarlyAsFP: if true, any alert before fault start is a false positive.
	EarlyAsFP bool `json:"earlyAsFP"`
	// ObsEnd: end of the observation window; faults still open then are
	// right-censored and reported separately (not counted as missed).
	ObsEnd time.Time `json:"obsEnd"`
}

// Fixture is a replayable, locally synthesized telemetry scenario.
type Fixture struct {
	Config      Config       `json:"config"`
	Faults      []Fault      `json:"faults"`
	Alerts      []Alert      `json:"alerts"`
	Corrections []Correction `json:"corrections"`
}
