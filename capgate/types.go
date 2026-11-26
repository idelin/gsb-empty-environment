package capgate

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Phase is a lifecycle/build phase recognized by the gate.
type Phase string

const (
	PhaseFetch       Phase = "fetch"
	PhasePreinstall  Phase = "preinstall"
	PhaseBuild       Phase = "build"
	PhasePostinstall Phase = "postinstall"
	PhasePublish     Phase = "publish"
)

// Valid reports whether p is one of the supported phases.
func (p Phase) Valid() bool {
	switch p {
	case PhaseFetch, PhasePreinstall, PhaseBuild, PhasePostinstall, PhasePublish:
		return true
	default:
		return false
	}
}

// Rank gives the canonical lifecycle order. Unknown phases sort after publish.
func (p Phase) Rank() int {
	switch p {
	case PhaseFetch:
		return 0
	case PhasePreinstall:
		return 1
	case PhaseBuild:
		return 2
	case PhasePostinstall:
		return 3
	case PhasePublish:
		return 4
	default:
		return 5
	}
}

// IsInstallScript reports whether the phase is preinstall or postinstall.
func (p Phase) IsInstallScript() bool {
	return p == PhasePreinstall || p == PhasePostinstall
}

// CacheMode describes how an edge uses a cache key.
type CacheMode string

const (
	CacheModeNone      CacheMode = ""
	CacheModeRead      CacheMode = "read"
	CacheModeWrite     CacheMode = "write"
	CacheModeReadWrite CacheMode = "read-write"
)

func (m CacheMode) reads() bool  { return m == CacheModeRead || m == CacheModeReadWrite }
func (m CacheMode) writes() bool { return m == CacheModeWrite || m == CacheModeReadWrite }

// HalfRef identifies one share of a threshold/half credential.
type HalfRef struct {
	// Family is the credential name shared by all halves.
	Family string `json:"family"`
	// Index is the one-based position of this half.
	Index int `json:"index"`
	// Total is the required number of halves in this family.
	Total int `json:"total"`
}

// Grant is a signed capability or temporary credential presented by an edge.
type Grant struct {
	// Capability is the concrete capability label, e.g. "publish:release".
	Capability string `json:"capability"`
	// TransactionID binds all signatures/halves belonging to one transaction.
	TransactionID string `json:"transactionId,omitempty"`
	// TokenID optionally identifies a unique token instance.
	TokenID string `json:"tokenId,omitempty"`
	// Temporary marks short-lived build-host credentials.
	Temporary bool `json:"temporary,omitempty"`
	// Half optionally identifies this grant as one half of a credential.
	Half   *HalfRef `json:"half,omitempty"`
	Signer string   `json:"signer,omitempty"`
	// Signature is an opaque proof verified by Policy.SignatureVerifier.
	Signature string `json:"signature,omitempty"`
}

// Edge is one dependency-graph edge. The security metadata is intentionally
// edge-centric because graph exports commonly attach lifecycle metadata there.
type Edge struct {
	ID string `json:"id"`
	// From and To are node IDs. Propagation follows From -> To.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`

	Pkg    string `json:"pkg"`
	Digest string `json:"digest"`
	Phase  Phase  `json:"phase"`

	// Condition is a Boolean expression over platform/feature names.
	Condition string `json:"condition,omitempty"`
	// PlatformCondition is an input alias for Condition.
	PlatformCondition string `json:"platformCondition,omitempty"`
	// Platform is a shorter input alias for Condition.
	Platform string `json:"platform,omitempty"`
	Optional bool   `json:"optional,omitempty"`

	CacheKey  string    `json:"cacheKey,omitempty"`
	CacheMode CacheMode `json:"cacheMode,omitempty"`
	// Sequence optionally breaks ties within one phase. Lower runs first.
	Sequence int `json:"sequence,omitempty"`

	// Sandboxed must be true only when an install script declares an explicit
	// isolation boundary. False preserves the unsafe inheritance semantics.
	Sandboxed bool `json:"sandboxed,omitempty"`

	// TrustedCacheWrite can suppress strict-cache findings when policy allows
	// a named trusted writer. It is optional and never inferred from digest.
	TrustedCacheWrite bool `json:"trustedCacheWrite,omitempty"`

	Grants []Grant `json:"grants,omitempty"`
}

// EffectiveCondition returns the first populated platform-condition alias.
func (e *Edge) EffectiveCondition() string {
	if e == nil {
		return ""
	}
	for _, candidate := range []string{e.Condition, e.PlatformCondition, e.Platform} {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

// Graph is the old or new dependency graph.
type Graph struct {
	Name  string  `json:"name,omitempty"`
	Edges []*Edge `json:"edges"`
}

// InstanceKey is the identity on which optional branches may unify.
type InstanceKey struct {
	Pkg    string `json:"pkg"`
	Digest string `json:"digest"`
	Phase  Phase  `json:"phase"`
}

// String renders pkg@digest#phase.
func (k InstanceKey) String() string {
	return fmt.Sprintf("%s@%s#%s", k.Pkg, k.Digest, k.Phase)
}

// Match reports whether a wildcard matcher equals k. Empty fields match all.
func (k InstanceKey) Match(m InstanceKey) bool {
	return (m.Pkg == "" || m.Pkg == k.Pkg) &&
		(m.Digest == "" || m.Digest == k.Digest) &&
		(m.Phase == "" || m.Phase == k.Phase)
}

// PrivilegeUnion is a capability set that becomes high privilege when jointly held.
type PrivilegeUnion struct {
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	// SameTransaction requires every selected grant to have one TransactionID.
	SameTransaction bool `json:"sameTransaction,omitempty"`
}

// SignatureVerifier validates an opaque signed grant. Implementations may wrap
// ed25519, Sigstore, KMS, or another host-specific trust root.
type SignatureVerifier interface {
	VerifySignature(Grant) error
}

// SignatureVerifierFunc adapts a function to SignatureVerifier.
type SignatureVerifierFunc func(Grant) error

// VerifySignature implements SignatureVerifier.
func (f SignatureVerifierFunc) VerifySignature(g Grant) error { return f(g) }

// Policy configures legal feature combinations and the high-privilege boundary.
type Policy struct {
	// Features fixes feature enumeration order/domain. Condition variables not
	// listed here are appended automatically.
	Features []string `json:"features,omitempty"`
	// LegalCondition restricts the exhaustive feature universe. Empty means all.
	LegalCondition string `json:"legalCondition,omitempty"`
	// MaxFeatures bounds exhaustive enumeration. Default and hard maximum 12;
	// the package is intentionally a small-feature exhaustive oracle.
	MaxFeatures int `json:"maxFeatures,omitempty"`

	// HighPrivilegePhases defaults to ["publish"].
	HighPrivilegePhases []Phase `json:"highPrivilegePhases,omitempty"`
	// HighPrivilegeCapabilities marks any instance holding one label as high.
	HighPrivilegeCapabilities []string `json:"highPrivilegeCapabilities,omitempty"`
	// HighPrivilegeInstances marks tuple matchers as high-privilege contexts.
	HighPrivilegeInstances []InstanceKey `json:"highPrivilegeInstances,omitempty"`
	// HighPrivilegeUnions marks jointly held capability sets as boundary breaches.
	HighPrivilegeUnions []PrivilegeUnion `json:"highPrivilegeUnions,omitempty"`
	// DenyOptionalUnification treats two individually compliant optional
	// branches that reach one high-privilege instance as union escalation.
	// Default true.
	DenyOptionalUnification *bool `json:"denyOptionalUnification,omitempty"`

	// DenyTemporaryCredentials makes temporary credentials at high-privilege
	// phases a finding. Default true.
	DenyTemporaryCredentials *bool `json:"denyTemporaryCredentials,omitempty"`
	// StrictSharedCache makes any earlier cross-edge write to a publish cache
	// reader a finding. Default true.
	StrictSharedCache *bool `json:"strictSharedCache,omitempty"`

	// RequireSignatures rejects grants that cannot pass this verifier during
	// analysis/replay. Nil performs structural transaction checks only.
	SignatureVerifier SignatureVerifier `json:"-"`
}

// DefaultPolicy returns the conservative gate policy.
func DefaultPolicy() Policy {
	denyTemp := true
	strictCache := true
	return Policy{
		MaxFeatures:              12,
		HighPrivilegePhases:      []Phase{PhasePublish},
		DenyTemporaryCredentials: &denyTemp,
		StrictSharedCache:        &strictCache,
	}
}

func (p Policy) denyTemporary() bool {
	return p.DenyTemporaryCredentials == nil || *p.DenyTemporaryCredentials
}

func (p Policy) strictCache() bool {
	return p.StrictSharedCache == nil || *p.StrictSharedCache
}

func (p Policy) denyOptionalUnification() bool {
	return p.DenyOptionalUnification == nil || *p.DenyOptionalUnification
}

func (p Policy) highPrivilegePhase(phase Phase) bool {
	if len(p.HighPrivilegePhases) == 0 {
		return phase == PhasePublish
	}
	for _, candidate := range p.HighPrivilegePhases {
		if candidate == phase {
			return true
		}
	}
	return false
}

func (p Policy) validate() error {
	if p.MaxFeatures < 0 {
		return errors.New("capgate: MaxFeatures must not be negative")
	}
	if p.MaxFeatures > 12 {
		return fmt.Errorf("capgate: MaxFeatures hard limit is 12 for exhaustive prime-implicant enumeration, got %d", p.MaxFeatures)
	}
	if _, err := compileCondition(p.LegalCondition); err != nil {
		return fmt.Errorf("capgate: invalid LegalCondition: %w", err)
	}
	for i, u := range p.HighPrivilegeUnions {
		if len(u.Capabilities) == 0 {
			return fmt.Errorf("capgate: HighPrivilegeUnions[%d] has no capabilities", i)
		}
	}
	return nil
}

// Cube is a conjunction of fixed feature values.
type Cube struct {
	Literals []Literal `json:"literals"`
}

// Literal is one fixed feature value.
type Literal struct {
	Feature string `json:"feature"`
	Value   bool   `json:"value"`
}

func cubeFromMask(features []string, fixedMask, valueMask int) Cube {
	var literals []Literal
	for i, name := range features {
		bit := 1 << i
		if fixedMask&bit != 0 {
			literals = append(literals, Literal{Feature: name, Value: valueMask&bit != 0})
		}
	}
	return Cube{Literals: literals}
}

// Expr renders the cube as a Boolean conjunction.
func (c Cube) Expr() string {
	if len(c.Literals) == 0 {
		return "true"
	}
	parts := make([]string, 0, len(c.Literals))
	for _, lit := range c.Literals {
		if lit.Value {
			parts = append(parts, lit.Feature)
		} else {
			parts = append(parts, "!"+lit.Feature)
		}
	}
	return joinSortedStable(parts)
}

func joinSortedStable(parts []string) string {
	sort.Strings(parts)
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " && "
		}
		out += p
	}
	return out
}

// Matches reports whether a satisfies every literal.
func (c Cube) Matches(a Assignment) bool {
	for _, lit := range c.Literals {
		if a.value(lit.Feature) != lit.Value {
			return false
		}
	}
	return true
}

func (c Cube) masks(features []string) (fixedMask, valueMask int) {
	index := make(map[string]int, len(features))
	for i, f := range features {
		index[f] = i
	}
	for _, lit := range c.Literals {
		i, ok := index[lit.Feature]
		if !ok {
			continue
		}
		bit := 1 << i
		fixedMask |= bit
		if lit.Value {
			valueMask |= bit
		}
	}
	return fixedMask, valueMask
}

// ConditionBlock is a pairwise legal-disjoint decision block.
type ConditionBlock struct {
	ID       string      `json:"id"`
	Instance InstanceKey `json:"instance"`
	// Expr is the replayable conjunction and is disjoint from other blocks for
	// the same instance under the legal feature universe.
	Expr string `json:"expr"`
	Cube Cube   `json:"cube"`
	// PrimeExpr is an original minimal prime implicant containing this block.
	PrimeExpr string `json:"primeExpr,omitempty"`
	PrimeCube Cube   `json:"primeCube,omitempty"`
	// OriginalPrime is true when Cube itself is an exact prime implicant of
	// the delta function. Otherwise Cube is a disjoint residual-prime
	// restriction of PrimeCube.
	OriginalPrime bool `json:"originalPrime,omitempty"`
	// ResidualPrime is true when Cube is prime after earlier disjoint blocks
	// and already-covered assignments are treated as forbidden care values.
	ResidualPrime bool `json:"residualPrime"`
	// Assignments are the concrete legal combinations covered by this block.
	Assignments []Assignment `json:"assignments"`
	// ChainIDs points into DiffReport.Chains.
	ChainIDs []string `json:"chainIds,omitempty"`
}

// ChainStep is one independently checkable replay transition.
type ChainStep struct {
	Order         int    `json:"order"`
	Kind          string `json:"kind"`
	EdgeID        string `json:"edgeId,omitempty"`
	Edge          *Edge  `json:"edge,omitempty"`
	Grant         *Grant `json:"grant,omitempty"`
	From          string `json:"from,omitempty"`
	To            string `json:"to,omitempty"`
	Capability    string `json:"capability,omitempty"`
	TransactionID string `json:"transactionId,omitempty"`
	BranchID      string `json:"branchId,omitempty"`
	CacheKey      string `json:"cacheKey,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

// ChainBranch is one acyclic contribution path into a unified grant chain.
type ChainBranch struct {
	BranchID string      `json:"branchId"`
	Optional bool        `json:"optional"`
	Steps    []ChainStep `json:"steps"`
	Grants   []Grant     `json:"grants,omitempty"`
}

// GrantChain is a self-contained, independently replayable witness.
type GrantChain struct {
	ID         string        `json:"id"`
	Instance   InstanceKey   `json:"instance"`
	Kind       string        `json:"kind"`
	Assignment Assignment    `json:"assignment"`
	Branches   []ChainBranch `json:"branches,omitempty"`
	Steps      []ChainStep   `json:"steps"`
	CacheKey   string        `json:"cacheKey,omitempty"`
	TxID       string        `json:"txId,omitempty"`
	EdgeIDs    []string      `json:"edgeIds,omitempty"`
	Valid      bool          `json:"valid"`
	Reason     string        `json:"reason,omitempty"`
}

// AssignmentCoverage records exact old/new high-privilege identities at one point.
type AssignmentCoverage struct {
	Assignment Assignment    `json:"assignment"`
	OldHigh    []InstanceKey `json:"oldHigh"`
	NewHigh    []InstanceKey `json:"newHigh"`
	Added      []InstanceKey `json:"added"`
}

// CoverageProof is an exhaustive certificate over the small feature universe.
type CoverageProof struct {
	FeatureNames       []string             `json:"featureNames"`
	LegalCondition     string               `json:"legalCondition,omitempty"`
	TotalCombinations  int                  `json:"totalCombinations"`
	LegalCombinations  int                  `json:"legalCombinations"`
	CheckedAssignments int                  `json:"checkedAssignments"`
	NoAdded            bool                 `json:"noAdded"`
	Assignments        []AssignmentCoverage `json:"assignments"`
	Digest             string               `json:"digest"`
}

// DiffReport is the stable result of comparing old and new graphs.
type DiffReport struct {
	Features        []string            `json:"features"`
	LegalCondition  string              `json:"legalCondition,omitempty"`
	Added           []AddedInstance     `json:"added"`
	Blocks          []ConditionBlock    `json:"blocks"`
	Chains          []*GrantChain       `json:"chains"`
	Coverage        CoverageProof       `json:"coverage"`
	PrimeImplicants []PrimeImplicantSet `json:"primeImplicants,omitempty"`
	Stats           DiffStats           `json:"stats"`
}

// AddedInstance groups all new condition blocks and witnesses for one tuple.
type AddedInstance struct {
	Instance         InstanceKey      `json:"instance"`
	Kinds            []string         `json:"kinds"`
	AddedAssignments []Assignment     `json:"addedAssignments"`
	PrimeImplicants  []string         `json:"primeImplicants"`
	Blocks           []ConditionBlock `json:"blocks"`
	ChainIDs         []string         `json:"chainIds"`
}

// PrimeImplicantSet associates exact PIs with an instance.
type PrimeImplicantSet struct {
	Instance InstanceKey `json:"instance"`
	Cubes    []Cube      `json:"cubes"`
	Exprs    []string    `json:"exprs"`
}

// DiffStats contains deterministic run metadata useful in CI logs.
type DiffStats struct {
	LegalAssignments int `json:"legalAssignments"`
	AddedInstances   int `json:"addedInstances"`
	Blocks           int `json:"blocks"`
	Chains           int `json:"chains"`
	PrimeImplicants  int `json:"primeImplicants"`
	Iterations       int `json:"iterations"`
}
