package netsec

import (
	"encoding/json"
	"fmt"
	"io"
)

// Entry type markers used by NuGet packages.lock.json.
const (
	TypeDirect            = "Direct"
	TypeTransitive        = "Transitive"
	TypeCentralTransitive = "CentralTransitive"
)

// LockFile is the in-memory representation of a NuGet packages.lock.json.
type LockFile struct {
	Version      int                             `json:"version"`
	Dependencies map[string]map[string]LockEntry `json:"dependencies"`
}

// LockEntry is a single resolved package entry under one target framework.
type LockEntry struct {
	Type         string            `json:"type"`
	Requested    string            `json:"requested,omitempty"`
	Resolved     string            `json:"resolved"`
	ContentHash  string            `json:"contentHash,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

// ParseLockFile decodes a packages.lock.json stream into a LockFile.
func ParseLockFile(r io.Reader) (*LockFile, error) {
	var lf LockFile
	dec := json.NewDecoder(r)
	if err := dec.Decode(&lf); err != nil {
		return nil, fmt.Errorf("parse lock file: %w", err)
	}
	return &lf, nil
}
