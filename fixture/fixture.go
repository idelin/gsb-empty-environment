// Package fixture loads locally synthesized telemetry traces from JSON
// files so they can be replayed by a test harness. It deliberately does
// not talk to any monitoring or APM platform.
//
// File shape:
//
//	{
//	  "reference": "client",
//	  "events": [
//	    {"id":"c1", "node":"client", "ts": 1000},
//	    {"id":"s1", "node":"server", "ts": "5m"}
//	  ],
//	  "edges": [{"from":"c1", "to":"s1"}]
//	}
//
// Timestamps accept either an integer number of nanoseconds since epoch
// or a relative duration string such as "20ms" / "-5us" (time.ParseDuration).
package fixture

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"skewcheck/skew"
)

// Timestamp is an observed local timestamp. Use TS for the resolved value
// (nanoseconds since epoch).
type Timestamp struct {
	TS int64
}

func (t *Timestamp) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("fixture: parse timestamp %q: %w", s, err)
		}
		t.TS = int64(d)
		return nil
	}
	if err := json.Unmarshal(data, &t.TS); err != nil {
		return fmt.Errorf("fixture: timestamp must be int nanos or duration string: %w", err)
	}
	return nil
}

func (t Timestamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.TS)
}

// Event is the on-disk event representation.
type Event struct {
	ID   string    `json:"id"`
	Node string    `json:"node"`
	TS   Timestamp `json:"ts"`
}

// Edge is an explicit causal constraint referencing event IDs.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Spec is a complete local trace fixture.
type Spec struct {
	Reference string  `json:"reference"`
	Wide      string  `json:"wide,omitempty"` // optional duration, e.g. "87600h"
	Events    []Event `json:"events"`
	Edges     []Edge  `json:"edges"`

	wideNS int64
}

// Load parses a fixture file and validates it.
func Load(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Spec
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("fixture: decode %s: %w", path, err)
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *Spec) validate() error {
	if s.Reference == "" {
		return fmt.Errorf("fixture: reference is required")
	}
	if len(s.Events) == 0 {
		return fmt.Errorf("fixture: at least one event is required")
	}
	seen := make(map[string]bool, len(s.Events))
	hasRef := false
	for _, e := range s.Events {
		if e.ID == "" {
			return fmt.Errorf("fixture: event with empty id")
		}
		if seen[e.ID] {
			return fmt.Errorf("fixture: duplicate event id %q", e.ID)
		}
		seen[e.ID] = true
		if e.Node == "" {
			return fmt.Errorf("fixture: event %q has empty node", e.ID)
		}
		if e.Node == s.Reference {
			hasRef = true
		}
	}
	if !hasRef {
		return fmt.Errorf("fixture: reference node %q has no events", s.Reference)
	}
	for _, ed := range s.Edges {
		if ed.From == "" || ed.To == "" {
			return fmt.Errorf("fixture: edge with empty endpoint")
		}
		if !seen[ed.From] {
			return fmt.Errorf("fixture: edge references unknown event %q", ed.From)
		}
		if !seen[ed.To] {
			return fmt.Errorf("fixture: edge references unknown event %q", ed.To)
		}
	}
	if s.Wide != "" {
		d, err := time.ParseDuration(s.Wide)
		if err != nil {
			return fmt.Errorf("fixture: parse wide %q: %w", s.Wide, err)
		}
		s.wideNS = int64(d)
	}
	return nil
}

// WideNS returns the configured wide bound in nanoseconds, or 0 for the
// analyzer default.
func (s *Spec) WideNS() int64 { return s.wideNS }

// Graph builds the analyzer graph from the fixture.
func (s *Spec) Graph() *skew.Graph {
	g := &skew.Graph{}
	for _, e := range s.Events {
		g.Events = append(g.Events, skew.Event{ID: e.ID, Node: e.Node, TS: e.TS.TS})
	}
	for _, ed := range s.Edges {
		g.Edges = append(g.Edges, skew.Edge{From: ed.From, To: ed.To})
	}
	return g
}

// Replay yields events in observed timestamp order (ID tiebreaker), as a
// local harness would replay them. The callback may stop replay by
// returning an error.
func (s *Spec) Replay(fn func(skew.Event) error) error {
	evs := make([]skew.Event, len(s.Events))
	for i, e := range s.Events {
		evs[i] = skew.Event{ID: e.ID, Node: e.Node, TS: e.TS.TS}
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].TS != evs[j].TS {
			return evs[i].TS < evs[j].TS
		}
		return evs[i].ID < evs[j].ID
	})
	for _, e := range evs {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}
