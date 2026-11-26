package capgate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrInvalidSignature can be returned by host SignatureVerifier implementations.
var ErrInvalidSignature = errors.New("invalid grant signature")

// Acceptance is the host-facing entry point. It is an explicitly named alias
// for Compare so call sites read as an acceptance-gate evaluation.
func Acceptance(oldGraph, newGraph *Graph, policy Policy) (*DiffReport, error) {
	return Compare(oldGraph, newGraph, policy)
}

// DecodeGraph decodes one JSON dependency graph.
func DecodeGraph(r io.Reader) (*Graph, error) {
	var g Graph
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("capgate: decode graph: %w", err)
	}
	return &g, nil
}

// CompareJSON decodes both graphs and runs Compare.
func CompareJSON(oldJSON, newJSON []byte, policy Policy) (*DiffReport, error) {
	var oldGraph Graph
	if len(oldJSON) > 0 {
		if err := json.Unmarshal(oldJSON, &oldGraph); err != nil {
			return nil, fmt.Errorf("capgate: decode old graph: %w", err)
		}
	}
	var newGraph Graph
	if err := json.Unmarshal(newJSON, &newGraph); err != nil {
		return nil, fmt.Errorf("capgate: decode new graph: %w", err)
	}
	return Compare(&oldGraph, &newGraph, policy)
}

// Pass reports whether the delta contains no newly added high-privilege instance.
func (r *DiffReport) Pass() bool {
	return r != nil && len(r.Added) == 0
}

// WriteJSON writes an indented machine-readable report.
func (r *DiffReport) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("capgate: encode report: %w", err)
	}
	return nil
}

// GateError returns a non-nil error when the acceptance gate must fail.
func (r *DiffReport) GateError() error {
	if r == nil {
		return fmt.Errorf("capgate: nil report")
	}
	if len(r.Added) == 0 {
		return nil
	}
	return &GateFailure{Added: len(r.Added), Blocks: len(r.Blocks), Chains: len(r.Chains)}
}

// GateFailure is returned when new high-privilege instances are introduced.
type GateFailure struct {
	Added  int `json:"added"`
	Blocks int `json:"blocks"`
	Chains int `json:"chains"`
}

func (e *GateFailure) Error() string {
	return fmt.Sprintf("capgate: supply-chain capability gate failed: %d new instance(s), %d condition block(s), %d replay chain(s)", e.Added, e.Blocks, e.Chains)
}
