package netsec

import (
	"sort"
	"strings"
)

// MaxNodes is the maximum number of versions accepted in a manifest.
const MaxNodes = 30

// VersionID identifies one version in a manifest.
type VersionID = string

// SecurityLevel is the security rank carried by a signed version node.
// Larger values are safer.
type SecurityLevel = uint64

// SignedLevel is a security level together with the signature that
// authenticates it. A nil LevelVerifier on Manifest means signatures are
// treated as trusted configuration and are not checked.
type SignedLevel struct {
	Level     SecurityLevel `json:"level"`
	KeyID     string        `json:"keyID,omitempty"`
	Signature []byte        `json:"signature,omitempty"`
}

// UnsignedLevel is a convenience constructor primarily useful for tests and
// for manifests whose signatures are authenticated elsewhere.
func UnsignedLevel(level SecurityLevel) SignedLevel {
	return SignedLevel{Level: level}
}

// LevelVerifier authenticates a node's signed security level. It is called
// once per node during selection.
type LevelVerifier func(version VersionID, level SignedLevel) error

// Environment describes the host properties used by compatibility rules.
type Environment = map[string]string

// Compatibility describes the conditions under which a version can be used.
// Requirements are exact key/value matches. Predicate, when non-nil, is an
// additional arbitrary condition. The zero value is compatible with every
// environment.
type Compatibility struct {
	Requirements map[string]string      `json:"requirements,omitempty"`
	Predicate    func(Environment) bool `json:"-"`
}

// Match reports whether the environment satisfies the compatibility rules.
// When it does not, failures contains deterministic human-readable details.
func (c Compatibility) Match(env Environment) (ok bool, failures []string) {
	keys := make([]string, 0, len(c.Requirements))
	for key := range c.Requirements {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		want := c.Requirements[key]
		got, present := env[key]
		switch {
		case !present:
			failures = append(failures, "missing environment key "+quote(key))
		case got != want:
			failures = append(failures, "environment key "+quote(key)+" is "+quote(got)+", want "+quote(want))
		}
	}
	if c.Predicate != nil && !c.Predicate(env) {
		failures = append(failures, "custom compatibility predicate rejected the environment")
	}
	return len(failures) == 0, failures
}

// Matches is the boolean form of Match.
func (c Compatibility) Matches(env Environment) bool {
	ok, _ := c.Match(env)
	return ok
}

// Node is one version in the migration graph.
type Node struct {
	Version       VersionID     `json:"version"`
	Level         SignedLevel   `json:"level"`
	Compatibility Compatibility `json:"compatibility,omitempty"`
	Revoked       bool          `json:"revoked,omitempty"`
}

// Compatible reports whether the node accepts env.
func (n Node) Compatible(env Environment) bool {
	return n.Compatibility.Matches(env)
}

// Edge is a legal migration explicitly listed by the update manifest. No
// edge is inferred from version ordering.
type Edge struct {
	From VersionID `json:"from"`
	To   VersionID `json:"to"`
	Cost uint64    `json:"cost"`
}

// Manifest is the signed update metadata supplied by the update service.
type Manifest struct {
	Nodes       []Node        `json:"nodes"`
	Edges       []Edge        `json:"edges,omitempty"`
	VerifyLevel LevelVerifier `json:"-"`
}

// Convenience aliases for callers that use graph-oriented terminology.
type (
	Graph        = Manifest
	VersionGraph = Manifest
	VersionNode  = Node
	VersionEdge  = Edge
)

// Node returns the node with the given version.
func (m Manifest) Node(version VersionID) (Node, bool) {
	for _, node := range m.Nodes {
		if node.Version == version {
			return node, true
		}
	}
	return Node{}, false
}

func quote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
