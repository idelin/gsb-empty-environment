package netsec

import (
	"errors"
	"fmt"
)

// MaxNodes is the largest number of version nodes a manifest may contain.
// The selection algorithm is designed around this small-graph bound.
const MaxNodes = 30

// SecurityLevel is a signed, ordered security rating of a version node.
// Larger values are strictly more secure. The exported constants are
// advisory reference levels; the algorithm treats any integer level as an
// ordered value.
type SecurityLevel int

const (
	LevelInsecure SecurityLevel = 0
	LevelLow      SecurityLevel = 10
	LevelStable   SecurityLevel = 20
	LevelHardened SecurityLevel = 30
)

// Features is the set of capability key/value pairs advertised by a node.
type Features map[string]string

// Compatibility lists feature pairs that a node requires of itself. A node
// is compatible only when every required key exists with the exact value.
type Compatibility struct {
	Required Features
}

// SatisfiedBy reports whether features fulfil every required pair.
func (c Compatibility) SatisfiedBy(features Features) bool {
	for key, want := range c.Required {
		got, ok := features[key]
		if !ok || got != want {
			return false
		}
	}
	return true
}

// SignedLevel binds a security level to its cryptographic signature. The
// signature is verified by a LevelVerifier before the level is trusted.
type SignedLevel struct {
	Level     SecurityLevel
	Signature []byte
}

// VersionNode is one version in the update manifest's version graph.
type VersionNode struct {
	ID            string
	Security      SignedLevel
	Features      Features
	Compatibility Compatibility
	Revoked       bool
	RevokeReason  string
}

// Edge is a manifest-authorized migration. Only edges listed in the manifest
// may be traversed; clients must never infer edges on their own.
type Edge struct {
	From string
	To   string
	Cost int
}

// Manifest is the signed description of the complete version graph.
type Manifest struct {
	Nodes []VersionNode
	Edges []Edge
}

// RejectCode identifies why a selection request could not produce a path.
type RejectCode string

const (
	RejectInvalidManifest       RejectCode = "invalid_manifest"
	RejectVerifierMissing       RejectCode = "verifier_missing"
	RejectCurrentNotFound       RejectCode = "current_not_found"
	RejectNodeNotFound          RejectCode = "node_not_found"
	RejectBadSignature          RejectCode = "bad_signature"
	RejectRevoked               RejectCode = "revoked"
	RejectIncompatible          RejectCode = "incompatible"
	RejectNoEligibleDestination RejectCode = "no_eligible_destination"
)

// RejectReason describes a single rejection. NodeID is empty for graph-level
// rejections and populated for node-local ones.
type RejectReason struct {
	Code    RejectCode
	NodeID  string
	Message string
}

func (r *RejectReason) Error() string {
	if r == nil {
		return "<nil>"
	}
	if r.NodeID != "" {
		return fmt.Sprintf("%s: node %q: %s", r.Code, r.NodeID, r.Message)
	}
	return fmt.Sprintf("%s: %s", r.Code, r.Message)
}

// Path is a validated migration route. Costs[k] is the cost of the edge from
// Versions[k] to Versions[k+1]; TotalCost is the sum of Costs.
type Path struct {
	Versions  []string
	Costs     []int
	TotalCost int
	Level     SecurityLevel
}

func reject(code RejectCode, nodeID, format string, args ...any) *RejectReason {
	return &RejectReason{
		Code:    code,
		NodeID:  nodeID,
		Message: fmt.Sprintf(format, args...),
	}
}

// Validate checks the structural invariants of the manifest independently of
// signatures and selection. Edges must reference known nodes, have non-negative
// cost, and not be duplicated.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("manifest is nil")
	}
	if len(m.Nodes) == 0 {
		return errors.New("manifest contains no nodes")
	}
	if len(m.Nodes) > MaxNodes {
		return fmt.Errorf("manifest has %d nodes, maximum is %d", len(m.Nodes), MaxNodes)
	}

	index := make(map[string]int, len(m.Nodes))
	for i, node := range m.Nodes {
		if node.ID == "" {
			return fmt.Errorf("node at index %d has an empty ID", i)
		}
		if _, exists := index[node.ID]; exists {
			return fmt.Errorf("duplicate node ID %q", node.ID)
		}
		index[node.ID] = i
	}

	type edgeKey struct{ from, to string }
	seen := make(map[edgeKey]struct{}, len(m.Edges))
	for i, edge := range m.Edges {
		if edge.Cost < 0 {
			return fmt.Errorf("edge %q->%q has negative cost %d", edge.From, edge.To, edge.Cost)
		}
		if _, ok := index[edge.From]; !ok {
			return fmt.Errorf("edge at index %d references unknown source %q", i, edge.From)
		}
		if _, ok := index[edge.To]; !ok {
			return fmt.Errorf("edge at index %d references unknown target %q", i, edge.To)
		}
		key := edgeKey{edge.From, edge.To}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("duplicate edge %q->%q", edge.From, edge.To)
		}
		seen[key] = struct{}{}
	}
	return nil
}
