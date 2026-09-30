package spaneval

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadRules loads a rule graph fixture from a JSON file.
func LoadRules(path string) (*Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root Rule
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("spaneval: rules %s: %w", path, err)
	}
	if err := Validate(&root); err != nil {
		return nil, fmt.Errorf("spaneval: rules %s: %w", path, err)
	}
	return &root, nil
}

// LoadEvents loads an event log fixture (JSON array) from a file.
func LoadEvents(path string) ([]Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("spaneval: events %s: %w", path, err)
	}
	return events, nil
}
