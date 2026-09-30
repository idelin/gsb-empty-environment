package matchsim

import (
	"encoding/json"
	"time"
)

// Duration wraps time.Duration so fixtures can use strings like "300s".
type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// Fault is a known (ground-truth) failure event injected by the simulator.
type Fault struct {
	ID      string     `json:"id"`
	Entity  string     `json:"entity"`
	Symptom string     `json:"symptom"`
	Start   time.Time  `json:"start"`
	End     *time.Time `json:"end,omitempty"` // nil => still open at observation end
}

// Alert is an observed symptom raised by telemetry.
type Alert struct {
	ID      string    `json:"id"`
	Entity  string    `json:"entity"`
	Symptom string    `json:"symptom"`
	Time    time.Time `json:"time"`
}

// Correction is a late-arriving timestamp fix for an alert.
type Correction struct {
	AlertID string    `json:"alertId"`
	NewTime time.Time `json:"newTime"`
}

// Config holds the matching rules.
type Config struct {
	// MaxLead: an alert may precede its fault start by at most this much.
	MaxLead Duration `json:"maxLead"`
	// MaxLag: an alert may follow its fault start by at most this much.
	MaxLag Duration `json:"maxLag"`
	// SymptomClasses maps a raw symptom to an equivalence class; symptoms in
	// the same class are interchangeable (duplicate-worded symptoms).
	SymptomClasses map[string]string `json:"symptomClasses,omitempty"`
	// EntityGroups maps an entity to a compatibility group; entities in the
	// same group are interchangeable. Unmapped entities match only themselves.
	EntityGroups map[string]string `json:"entityGroups,omitempty"`
	// EarlyAlertAsFP: if true, alerts before the fault start are never
	// matched and count as false positives; if false, an alert within
	// MaxLead matches and is reported with its lead (negative latency).
	EarlyAlertAsFP bool `json:"earlyAlertAsFalsePositive,omitempty"`
}

func (c Config) symptomClass(s string) string {
	if cl, ok := c.SymptomClasses[s]; ok {
		return cl
	}
	return s
}

func (c Config) entityGroup(e string) string {
	if g, ok := c.EntityGroups[e]; ok {
		return g
	}
	return e
}

// Scenario is a replayable fixture: faults, alerts, rules and corrections.
type Scenario struct {
	ObservationEnd time.Time    `json:"observationEnd"`
	Config         Config       `json:"config"`
	Faults         []Fault      `json:"faults"`
	Alerts         []Alert      `json:"alerts"`
	Corrections    []Correction `json:"corrections,omitempty"`
}

// censored reports whether a fault is right-censored: it had not ended
// before the observation window closed.
func (sc Scenario) censored(f Fault) bool {
	return f.End == nil || f.End.After(sc.ObservationEnd)
}
