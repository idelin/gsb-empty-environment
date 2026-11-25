package upgradeplan

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Hard limits from the task statement.
const (
	MaxDevices  = 8
	MaxReleases = 12
	MaxRounds   = 8
)

// Signer is one signer identity. Different IDs may denote the same public-key
// material; such aliases cast only one threshold vote.
type Signer struct {
	ID          string `json:"id"`
	PublicKey   string `json:"public_key,omitempty"`
	KeyMaterial string `json:"key_material,omitempty"`
}

// SignerSet is an unordered set of signer identities.
type SignerSet []Signer

// InstallState is the finite installation state visible to the planner/host.
type InstallState = DeviceState

// DeviceState describes one installation and its lineage markers.
type DeviceState struct {
	Package string `json:"package,omitempty"`

	// Signers is the current signer set.
	Signers SignerSet `json:"signers,omitempty"`

	// SDK is the concrete device SDK matched against Release.SDKRange.
	SDK int `json:"sdk,omitempty"`

	// SDKRange is optional and useful when an input state represents a device
	// SDK interval rather than one concrete SDK.
	SDKRange SDKRange `json:"device_sdk_range,omitempty"`

	// HasData is the current existing-data marker.
	HasData bool `json:"has_data,omitempty"`

	// InitialData records whether the initial installation had data. It is
	// initialized from HasData by NormalizeProblem/Solve/VerifyPlan.
	InitialData bool `json:"initial_data,omitempty"`

	// DataLost is the sticky lineage violation bit. Once true, no later upgrade
	// can make the trajectory acceptable again.
	DataLost bool `json:"data_lost,omitempty"`
}

// SDKRange is an inclusive device-SDK compatibility interval. Zero MinSDK or
// MaxSDK means that side is unbounded.
type SDKRange struct {
	MinSDK int `json:"min_sdk,omitempty"`
	MaxSDK int `json:"max_sdk,omitempty"`
}

// Contains reports whether sdk is in the inclusive interval.
func (r SDKRange) Contains(sdk int) bool {
	if r.MinSDK != 0 && sdk < r.MinSDK {
		return false
	}
	if r.MaxSDK != 0 && sdk > r.MaxSDK {
		return false
	}
	return true
}

// Covers reports whether the concrete/possible interval r is fully inside q.
func (q SDKRange) Covers(r SDKRange) bool {
	if q.MinSDK != 0 && (r.MinSDK == 0 || r.MinSDK < q.MinSDK) {
		return false
	}
	if q.MaxSDK != 0 && (r.MaxSDK == 0 || r.MaxSDK > q.MaxSDK) {
		return false
	}
	return true
}

// Overlaps reports whether two inclusive intervals overlap.
func (r SDKRange) Overlaps(q SDKRange) bool {
	rmin, rmax := r.bounds()
	qmin, qmax := q.bounds()
	return rmin <= qmax && qmin <= rmax
}

func (r SDKRange) bounds() (int, int) {
	min := r.MinSDK
	max := r.MaxSDK
	if min == 0 {
		min = -1 << 30
	}
	if max == 0 {
		max = 1 << 30
	}
	return min, max
}

// Release is one verified upgrade record / broadcastable release package.
type Release struct {
	// ID is the broadcast release identifier.
	ID string `json:"id"`

	// Package optionally restricts eligibility to installations of this package.
	Package string `json:"package,omitempty"`

	// SDKRange is the release's compatible device-SDK interval.
	SDKRange SDKRange `json:"sdk_range,omitempty"`

	// OldThreshold is the required number of distinct authorized public-key
	// materials. Zero means no old-signer threshold.
	OldThreshold int `json:"old_threshold,omitempty"`

	// OldSigners optionally restricts which current signer materials count.
	// When empty, every distinct material in the current signer set counts.
	OldSigners SignerSet `json:"old_signers,omitempty"`

	// OldSignerKeys is a convenience form of OldSigners for raw key materials.
	OldSignerKeys []string `json:"old_signer_keys,omitempty"`

	// NewSignerSets lists all allowed successor signer sets. An eligible device
	// chooses one entry. NewSigners is a convenience for one deterministic set.
	NewSignerSets []SignerSet `json:"new_signer_sets,omitempty"`
	NewSigners    SignerSet   `json:"new_signers,omitempty"`

	// CarryExistingData must be true when the signed record authorizes carrying
	// pre-existing data through this upgrade. False means an initial-data device
	// loses its lineage and makes that branch unacceptable forever.
	CarryExistingData bool `json:"carry_existing_data,omitempty"`
}

// UpgradeRecord is an alias for host code using the problem vocabulary.
type UpgradeRecord = Release

// Target defines the acceptable final trusted signer sets.
type Target struct {
	// Trusted is one required signer set.
	Trusted SignerSet `json:"trusted,omitempty"`

	// TrustedSets allows several final trusted sets. If nonempty it takes
	// precedence over Trusted.
	TrustedSets []SignerSet `json:"trusted_sets,omitempty"`

	// ExactIDs compares signer IDs as well as public-key materials. By default
	// equality is by distinct public-key material, matching trust semantics.
	ExactIDs bool `json:"exact_ids,omitempty"`
}

// Problem is the complete finite planning input.
type Problem struct {
	// Package is the default package for devices/releases that omit Package.
	Package string `json:"package,omitempty"`

	// Devices is a convenience form for one known initial fleet.
	Devices []DeviceState `json:"devices,omitempty"`

	// InitialFleets is the initial belief: every fleet is an initial situation
	// that the plan must handle. When empty, Devices is used as the sole fleet.
	InitialFleets [][]DeviceState `json:"initial_fleets,omitempty"`

	// InitialStates is an optional finite state universe. When DeviceCount is
	// positive and neither Devices nor InitialFleets is supplied, Problem
	// generates every unordered multiset of DeviceCount states as an initial
	// situation.
	InitialStates []DeviceState `json:"initial_states,omitempty"`

	// DeviceCount is used with InitialStates.
	DeviceCount int `json:"device_count,omitempty"`

	// Releases are the verified upgrade records.
	Releases []Release `json:"releases,omitempty"`

	// Target is the required trusted final signer set(s).
	Target Target `json:"target,omitempty"`

	// MaxRounds defaults to MaxRounds and may not exceed MaxRounds.
	MaxRounds int `json:"max_rounds,omitempty"`
}

// Broadcast is the only publisher action: specify one release package.
type Broadcast struct {
	ReleaseID string `json:"release_id"`
	Package   string `json:"package,omitempty"`
}

// PlanNode is one node of the count-observation decision tree.
type PlanNode struct {
	// Terminal is true when this belief branch is complete.
	Terminal bool `json:"terminal,omitempty"`

	// Broadcast is the single instruction to send at a nonterminal branch.
	Broadcast *Broadcast `json:"broadcast,omitempty"`

	// Branches are keyed by the only runtime observation: successful count.
	Branches map[int]*PlanNode `json:"branches,omitempty"`

	// WorstRemainingRounds is the maximum number of broadcasts below this node.
	WorstRemainingRounds int `json:"worst_remaining_rounds"`
}

// Plan is a shortest worst-case conditional broadcast plan.
type Plan struct {
	WorstRounds int       `json:"worst_rounds"`
	Root        *PlanNode `json:"root"`
}

// Next returns the branch after observing successCount.
func (n *PlanNode) Next(successCount int) (*PlanNode, bool) {
	if n == nil {
		return nil, false
	}
	child, ok := n.Branches[successCount]
	return child, ok
}

// NewSignerSet builds a signer set from IDs. An empty public key means the ID
// itself is treated as distinct key material.
func NewSignerSet(ids ...string) SignerSet {
	out := make(SignerSet, 0, len(ids))
	for _, id := range ids {
		out = append(out, Signer{ID: id})
	}
	return out
}

// KeySigner builds a signer whose ID and public-key material are both key.
func KeySigner(publicKey string) Signer {
	return Signer{ID: publicKey, PublicKey: publicKey}
}

// KeySignerSet builds a signer set from raw public-key materials.
func KeySignerSet(keys ...string) SignerSet {
	out := make(SignerSet, 0, len(keys))
	for _, key := range keys {
		out = append(out, KeySigner(key))
	}
	return out
}

// AliasSigner builds one ID that is an alias for publicKey.
func AliasSigner(id, publicKey string) Signer {
	return Signer{ID: id, PublicKey: publicKey}
}

func (s Signer) publicMaterial() string {
	if k := strings.TrimSpace(s.KeyMaterial); k != "" {
		return k
	}
	return strings.TrimSpace(s.PublicKey)
}

// Material returns the canonical threshold material for a signer.
func (s Signer) Material() string {
	if k := s.publicMaterial(); k != "" {
		return "key:" + k
	}
	return "id:" + strings.TrimSpace(s.ID)
}

func (s Signer) canonical() string {
	return "(" + strconv.Quote(strings.TrimSpace(s.ID)) + "," + strconv.Quote(s.Material()) + ")"
}

// Normalize trims, fills omitted IDs, removes exact duplicates, and sorts.
// Aliases sharing a key are intentionally retained.
func (ss SignerSet) Normalize() SignerSet {
	out := make(SignerSet, 0, len(ss))
	seen := make(map[string]struct{}, len(ss))
	for _, signer := range ss {
		signer.ID = strings.TrimSpace(signer.ID)
		signer.PublicKey = strings.TrimSpace(signer.PublicKey)
		signer.KeyMaterial = strings.TrimSpace(signer.KeyMaterial)
		if signer.ID == "" {
			if k := signer.publicMaterial(); k != "" {
				signer.ID = k
			} else {
				signer.ID = ""
			}
		}
		key := signer.canonical()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, signer)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].canonical() < out[j].canonical() })
	return out
}

// DistinctKeyCount counts votes by distinct public-key material.
func (ss SignerSet) DistinctKeyCount() int {
	keys := make(map[string]struct{})
	for _, signer := range ss.Normalize() {
		keys[signer.Material()] = struct{}{}
	}
	return len(keys)
}

// Materials returns sorted distinct key materials.
func (ss SignerSet) Materials() []string {
	set := make(map[string]struct{})
	for _, signer := range ss.Normalize() {
		set[signer.Material()] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func (ss SignerSet) idSet() map[string]struct{} {
	out := make(map[string]struct{})
	for _, signer := range ss.Normalize() {
		out[strings.TrimSpace(signer.ID)] = struct{}{}
	}
	return out
}

func sameStringSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func (t Target) matches(signers SignerSet) bool {
	signers = signers.Normalize()
	sets := t.TrustedSets
	if len(sets) == 0 && len(t.Trusted) > 0 {
		sets = []SignerSet{t.Trusted}
	}
	for _, trusted := range sets {
		trusted = trusted.Normalize()
		if t.ExactIDs {
			if sameStringSet(signers.idSet(), trusted.idSet()) {
				return true
			}
			continue
		}
		if sameStringSet(materialSet(signers), materialSet(trusted)) {
			return true
		}
	}
	return false
}

func materialSet(ss SignerSet) map[string]struct{} {
	out := make(map[string]struct{})
	for _, material := range ss.Materials() {
		out[material] = struct{}{}
	}
	return out
}

// ValidateLimits checks the explicit finite bounds.
func (p Problem) ValidateLimits() error {
	if len(p.Releases) > MaxReleases {
		return fmt.Errorf("upgradeplan: at most %d releases, got %d", MaxReleases, len(p.Releases))
	}
	fleets := p.InitialFleets
	if len(fleets) == 0 && len(p.Devices) > 0 {
		fleets = [][]DeviceState{p.Devices}
	}
	for i, fleet := range fleets {
		if len(fleet) > MaxDevices {
			return fmt.Errorf("upgradeplan: initial fleet %d has %d devices, max %d", i, len(fleet), MaxDevices)
		}
	}
	if p.MaxRounds > MaxRounds {
		return fmt.Errorf("upgradeplan: at most %d rounds, got %d", MaxRounds, p.MaxRounds)
	}
	if p.MaxRounds < 0 {
		return fmt.Errorf("upgradeplan: MaxRounds must be non-negative")
	}
	if p.DeviceCount > MaxDevices {
		return fmt.Errorf("upgradeplan: at most %d devices, got DeviceCount %d", MaxDevices, p.DeviceCount)
	}
	if p.DeviceCount < 0 {
		return fmt.Errorf("upgradeplan: DeviceCount must be non-negative")
	}
	return nil
}
