package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Podspec is the subset of a CocoaPods JSON podspec that the verifier needs.
type Podspec struct {
	Name    string         `json:"name"`
	Version string         `json:"version"`
	Source  map[string]any `json:"source"`
}

// ParsePodspec parses the JSON encoding of a podspec. The raw bytes are
// returned as well so callers can hash exactly what was read from the mirror.
func ParsePodspec(data []byte) (*Podspec, error) {
	var spec Podspec
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPodspec, err)
	}
	if spec.Name == "" {
		return nil, fmt.Errorf("%w: missing name", ErrInvalidPodspec)
	}
	if spec.Version == "" {
		return nil, fmt.Errorf("%w: missing version", ErrInvalidPodspec)
	}
	if spec.Source == nil {
		return nil, fmt.Errorf("%w: missing source", ErrInvalidPodspec)
	}
	return &spec, nil
}
