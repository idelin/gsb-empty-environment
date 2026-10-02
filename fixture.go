package promlite

import (
	"encoding/json"
	"fmt"
	"os"
)

// Fixture is the on-disk replay format for synthesized telemetry events.
type Fixture struct {
	Events []Sample `json:"events"`
}

// LoadFixture reads a fixture file into a Store.
func LoadFixture(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Fixture
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing fixture %s: %w", path, err)
	}
	s := NewStore()
	s.Add(f.Events...)
	return s, nil
}

// SaveFixture writes a Store's events as a replayable fixture file.
func SaveFixture(path string, s *Store) error {
	data, err := json.MarshalIndent(Fixture{Events: s.Samples()}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
