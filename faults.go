package paradl

import (
	"encoding/json"
	"fmt"
	"os"
)

// FaultFile describes injected faults for the local simulation. All request
// numbers are 1-based counts of Fetch calls on the source.
type FaultFile struct {
	// ShortWrites make the response to the Nth request carry fewer bytes
	// than requested (the actual range shrinks from the end).
	ShortWrites []ShortWriteFault `json:"short_writes"`
	// VersionChanges mutate the source content and bump its entity version
	// before the Nth request is served.
	VersionChanges []VersionChangeFault `json:"version_changes"`
	// StaleResponses capture a snapshot of the source at the Nth request
	// and serve it back DelayRequests later, simulating an old response
	// arriving late.
	StaleResponses []StaleResponseFault `json:"stale_responses"`
	// CrashOnCheckpointSave makes the downloader abort with ErrSimulatedCrash
	// instead of performing the Nth checkpoint save (1-based). Zero disables.
	CrashOnCheckpointSave int `json:"crash_on_checkpoint_save"`
}

// ShortWriteFault truncates the response to the request numbered OnRequest.
type ShortWriteFault struct {
	OnRequest int   `json:"on_request"`
	KeepBytes int64 `json:"keep_bytes"`
}

// VersionChangeFault rewrites the source content before serving OnRequest.
type VersionChangeFault struct {
	OnRequest  int    `json:"on_request"`
	NewSeed    int64  `json:"new_seed"`    // seed for the new deterministic content
	NewSize    int64  `json:"new_size"`    // <= 0 keeps the current size
	NewVersion string `json:"new_version"` // optional; defaults to "v<counter>"
}

// StaleResponseFault replays an old snapshot after a delay.
type StaleResponseFault struct {
	OnRequest     int `json:"on_request"`     // request that captures the snapshot
	DelayRequests int `json:"delay_requests"` // requests to wait before serving it
}

// LoadFaults reads a fault file. A nil path or missing file yields no faults.
func LoadFaults(path string) (*FaultFile, error) {
	if path == "" {
		return &FaultFile{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fault file: %w", err)
	}
	var f FaultFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse fault file %s: %w", path, err)
	}
	return &f, nil
}
