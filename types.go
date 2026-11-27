// Package capgate implements a supply-chain capability-isolation acceptance
// gate. It compares an old and a new dependency lock graph and decides, by
// exhaustive enumeration over platform bits and feature switches, under which
// legal condition regions the upgrade introduces build instances capable of
// performing a sensitive (publish) operation.
//
// The gate is deliberately conservative: temporary publish credentials are
// bound to a content digest at a specific publish stage, never to a package
// name or alias. Two half-authorisations may only be joined into a full grant
// when they share the issuing transaction id and reach the very same receiving
// instance; credentials must never be carried across stages through shared
// environment variables or a writable cache directory.
package capgate

import "fmt"

// Selector is a single atomic platform/feature condition. A Selector with
// Flag == "" is a platform bit; otherwise it is a feature-switch atom.
type Selector struct {
	Platform string `json:"platform,omitempty"`
	Flag     string `json:"flag,omitempty"`
	On       bool   `json:"on"`
}

// Half describes one half of a split publish authorisation. A full credential
// is reconstructed only from two halves that agree on TxnID and Digest.
type Half struct {
	TxnID      string `json:"txn_id"`
	Digest     string `json:"digest"`
	HalfIndex  int    `json:"half_index"` // 0 or 1
	Stage      string `json:"stage"`      // stage at which this half is issued
	Transport  string `json:"transport"`  // transport summary (channel metadata)
	AliasClaim string `json:"alias_claim,omitempty"`
}

// Edge is a directed dependency edge in a lock graph.
type Edge struct {
	From            string     `json:"from"`
	To              string     `json:"to"`
	Platform        []Selector `json:"platform_conditions"`
	Stage           string     `json:"stage"`
	Optional        bool       `json:"optional"`
	GateFlag        string     `json:"gate_flag,omitempty"`
	GateOn          bool       `json:"gate_on"`
	TransportDigest string     `json:"transport_digest"`
	Half            *Half      `json:"half,omitempty"`
}

// LeakAttempt models an attempt to smuggle an already-issued temporary
// credential from one stage into a later stage through an out-of-band channel
// (a shared environment variable or a writable cache directory). The gate must
// reject every such attempt.
type LeakAttempt struct {
	FromStage   string `json:"from_stage"`
	ToStage     string `json:"to_stage"`
	Node        string `json:"node"`
	Digest      string `json:"digest"`
	TxnID       string `json:"txn_id"`
	Channel     string `json:"channel"` // "env" or "writable_cache"
	Description string `json:"description,omitempty"`
}

// Node is a lock-graph node. Host tools and target packages may share a Name;
// they remain distinct nodes because ID and Digest differ.
type Node struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Host   bool   `json:"host,omitempty"` // true for a build-host tool
}

// Graph is one version (old or new) of the resolved lock graph.
type Graph struct {
	Label string        `json:"label"`
	Nodes []Node        `json:"nodes"`
	Edges []Edge        `json:"edges"`
	Leaks []LeakAttempt `json:"leak_attempts,omitempty"`
}

// Job is the full gate input.
type Job struct {
	Platforms      []string `json:"platforms"`
	Flags          []string `json:"feature_flags"`
	Stages         []string `json:"stages"` // ordered, e.g. fetch, build, publish
	SensitiveStage string   `json:"sensitive_stage"`
	// AllowMultiPlatform permits several platform bits to be true at once. By
	// default target platforms are mutually exclusive: exactly one platform bit
	// is set in an enumerated legal combo.
	AllowMultiPlatform bool  `json:"allow_multi_platform,omitempty"`
	Old                Graph `json:"old"`
	New                Graph `json:"new"`
}

// Combo is one exhaustive assignment of platform bits and feature switches.
type Combo struct {
	PlatformValues map[string]bool `json:"platform_values"`
	FlagValues     map[string]bool `json:"flag_values"`
}

func (c Combo) key() string {
	return fmt.Sprintf("p=%v f=%v", c.PlatformValues, c.FlagValues)
}

// InstanceRef identifies one node instantiated at one stage.
type InstanceRef struct {
	Node  string `json:"node"`
	Name  string `json:"name"`
	Stage string `json:"stage"`
}

func (r InstanceRef) String() string { return r.Node + "@" + r.Stage }

// EdgeHop records one traversed edge for an independently re-walkable chain.
type EdgeHop struct {
	From            string     `json:"from"`
	To              string     `json:"to"`
	Stage           string     `json:"stage"`
	Optional        bool       `json:"optional"`
	Conditions      []Selector `json:"conditions"`
	TransportDigest string     `json:"transport_digest"`
	HalfIndex       int        `json:"half_index"`
	Transport       string     `json:"transport"`
}

// HalfProvenance is one half plus the path that delivered it.
type HalfProvenance struct {
	TxnID  string    `json:"txn_id"`
	Digest string    `json:"digest"`
	Half   Half      `json:"half"`
	Path   []EdgeHop `json:"path"`
}

// GrantChain is a full, mergeable authorisation that completes a sensitive op.
type GrantChain struct {
	Instance       InstanceRef      `json:"instance"`
	TxnID          string           `json:"txn_id"`
	Digest         string           `json:"digest"`
	Stage          string           `json:"stage"`
	Halves         []HalfProvenance `json:"halves"`
	TransportOK    bool             `json:"transport_ok"`
	DigestBound    bool             `json:"digest_bound"`
	SensitiveStage bool             `json:"sensitive_stage"`
	SameTxn        bool             `json:"same_txn"`
	SameReceiver   bool             `json:"same_receiver"`
}

// Denial records a rejected credential or merge, with a stable reason code.
type Denial struct {
	Instance string `json:"instance,omitempty"`
	Reason   string `json:"reason"`
	Detail   string `json:"detail"`
}

// EvalResult is the per-combo evaluation of one graph version.
type EvalResult struct {
	Capable    map[string][]GrantChain `json:"capable"`
	Denials    []Denial                `json:"denials"`
	Iterations int                     `json:"iterations"`
	VisitMax   int                     `json:"max_revisit_depth"`
}

// Cube is a partial assignment represented as tri-state per atom; nil means
// "don't care". A cube denotes a set of combos.
type Cube struct {
	Platforms map[string]*bool `json:"platforms"`
	Flags     map[string]*bool `json:"flags"`
}

// Region is a non-empty set of disjoint cubes sharing one delta signature.
type Region struct {
	ID       string       `json:"id"`
	Cubes    []Cube       `json:"cubes"`
	Added    []string     `json:"added_capabilities"`
	Chains   []GrantChain `json:"chains"`
	ComboIDs []string     `json:"combo_ids"`
}

// CheckResult is one semantic verification outcome.
type CheckResult struct {
	ID     string `json:"id"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

// Coverage certifies exhaustive enumeration and region properties.
type Coverage struct {
	TotalCombos     int      `json:"total_combos"`
	CombosInRegions int      `json:"combos_covered"`
	Complete        bool     `json:"complete"`
	RegionsDisjoint bool     `json:"regions_disjoint"`
	NoDelta         bool     `json:"no_delta"`
	Evidence        []string `json:"evidence"`
}

// Report is the final acceptance report.
type Report struct {
	ComboCount int           `json:"combo_count"`
	Regions    []Region      `json:"regions"`
	Checks     []CheckResult `json:"semantic_checks"`
	Coverage   Coverage      `json:"coverage"`
	Denials    []Denial      `json:"denials"`
	NoDelta    bool          `json:"no_delta"`
}
