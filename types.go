package closure

import "fmt"

// Identity is the exact identity bound by provenance. Source and digest are
// both significant: same name from different sources never shares proof.
type Identity struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	Source  string `json:"source"`
	Digest  string `json:"digest"`
}

// ID is the concise name for an Identity.
type ID = Identity

// PackageID is an alias for host code that prefers that name.
type PackageID = Identity

// Candidate is an alias because the input is a finite candidate table.
type Candidate = Package

func (id Identity) String() string {
	return fmt.Sprintf("%s@v%d(source=%q,digest=%q)", id.Name, id.Version, id.Source, id.Digest)
}

func (id Identity) empty() bool {
	return id.Name == "" && id.Version == 0 && id.Source == "" && id.Digest == ""
}

// Literal is a possibly negated build flag.
type Literal struct {
	// Flag is the preferred field name. Name is accepted as an alias.
	Flag string `json:"flag,omitempty"`
	Name string `json:"name,omitempty"`

	// Negated means !Flag. Neg is accepted as an alias.
	Negated bool `json:"negated,omitempty"`
	Neg     bool `json:"neg,omitempty"`
}

func (l Literal) flagName() string {
	if l.Flag != "" {
		return l.Flag
	}
	return l.Name
}

func (l Literal) negated() bool { return l.Negated || l.Neg }

// PosLiteral returns flag.
func PosLiteral(flag string) Literal { return Literal{Flag: flag} }

// NegLiteral returns !flag.
func NegLiteral(flag string) Literal { return Literal{Flag: flag, Negated: true} }

// Clause is a binary Boolean clause: Literals[0] OR Literals[1].
type Clause [2]Literal

// BinaryClause is a descriptive alias for Clause.
type BinaryClause = Clause

// Either constructs a binary clause.
func Either(a, b Literal) Clause { return Clause{a, b} }

// Condition is a conjunction of required flag values. A nil or empty condition
// is unconditionally true. For example Condition{"release": true, "debug": false}.
type Condition map[string]bool

// Dependency is one requirement of a package. Alternatives are exact candidate
// identities; at most one identity is chosen for each package name globally.
type Dependency struct {
	// Name is optional when alternatives are supplied; it makes evidence and
	// empty-alternative failures easier to read.
	Name string `json:"name,omitempty"`

	// Alternatives is the preferred candidate list.
	Alternatives []Identity `json:"alternatives,omitempty"`

	// Alts and Options are accepted aliases for Alternatives.
	Alts    []Identity `json:"alts,omitempty"`
	Options []Identity `json:"options,omitempty"`

	// When is the preferred activation condition. Condition is an alias field.
	When      Condition `json:"when,omitempty"`
	Condition Condition `json:"condition,omitempty"`

	// Flag/Negated provide a compact one-literal condition.
	Flag    string `json:"flag,omitempty"`
	Negated bool   `json:"negated,omitempty"`
}

// Package is one exact candidate identity and its dependency requirements.
type Package struct {
	// ID is the preferred exact identity. Identity is an alias field.
	ID       Identity `json:"id"`
	Identity Identity `json:"identity,omitempty"`

	// Flat identity fields are accepted when ID is omitted.
	Name    string `json:"name,omitempty"`
	Version int    `json:"version,omitempty"`
	Source  string `json:"source,omitempty"`
	Digest  string `json:"digest,omitempty"`

	// Deps is the preferred dependency list. Dependencies is an alias.
	Deps         []Dependency `json:"deps,omitempty"`
	Dependencies []Dependency `json:"dependencies,omitempty"`
}

// Attestation binds proof to one exact identity, the flag condition under
// which it applies, and the signer's policy generation.
type Attestation struct {
	// ID is the preferred attested identity. Identity and Subject are aliases.
	ID       Identity `json:"id"`
	Identity Identity `json:"identity,omitempty"`
	Subject  Identity `json:"subject,omitempty"`
	Package  Identity `json:"package,omitempty"`

	// Condition/When are the flag condition. Empty means all configurations.
	Condition Condition `json:"condition,omitempty"`
	When      Condition `json:"when,omitempty"`

	// PolicyGeneration is the preferred generation field.
	PolicyGeneration uint64 `json:"policy_generation,omitempty"`
	Generation       uint64 `json:"generation,omitempty"`
	Gen              uint64 `json:"gen,omitempty"`
}

// Request is the closed-world input to closure acceptance.
type Request struct {
	// Root is an optional fully specified root identity.
	Root         Identity `json:"root,omitempty"`
	RootID       Identity `json:"root_id,omitempty"`
	Target       Identity `json:"target,omitempty"`
	RootIdentity Identity `json:"root_identity,omitempty"`

	// RootName plus RootDigest/TargetDigest identifies the target digest.
	RootName     string `json:"root_name,omitempty"`
	RootPackage  string `json:"root_package,omitempty"`
	RootDigest   string `json:"root_digest,omitempty"`
	TargetDigest string `json:"target_digest,omitempty"`
	Digest       string `json:"digest,omitempty"`

	// VariableOrder is the required BDD order. Order, Flags and BuildFlags are
	// accepted aliases. If several are supplied, the first nonempty one wins.
	VariableOrder []string `json:"variable_order,omitempty"`
	Order         []string `json:"order,omitempty"`
	Flags         []string `json:"flags,omitempty"`
	BuildFlags    []string `json:"build_flags,omitempty"`

	// Packages is the candidate table. Candidates and Table are aliases.
	Packages   []Package `json:"packages,omitempty"`
	Candidates []Package `json:"candidates,omitempty"`
	Table      []Package `json:"table,omitempty"`

	// Attestations is the provenance table. Provenances and Proofs are aliases.
	Attestations []Attestation `json:"attestations,omitempty"`
	Provenances  []Attestation `json:"provenances,omitempty"`
	Proofs       []Attestation `json:"proofs,omitempty"`

	// Clauses is the conjunction of binary global constraints. Constraints is
	// an alias.
	Clauses     []Clause `json:"clauses,omitempty"`
	Constraints []Clause `json:"constraints,omitempty"`

	// PolicyGeneration selects attestations from one signer policy epoch.
	PolicyGeneration uint64 `json:"policy_generation,omitempty"`
	Generation       uint64 `json:"generation,omitempty"`

	// NodeBudget limits shared nonterminal BDD nodes. Zero or a negative value
	// means unlimited. Budget and MaxNodes are aliases.
	NodeBudget int `json:"node_budget,omitempty"`
	Budget     int `json:"budget,omitempty"`
	MaxNodes   int `json:"max_nodes,omitempty"`

	// TrustRoot/RootTrusted/SkipRootProof explicitly treat the pinned root
	// target digest as the host's trust anchor. This is the default.
	TrustRoot     bool `json:"trust_root,omitempty"`
	RootTrusted   bool `json:"root_trusted,omitempty"`
	SkipRootProof bool `json:"skip_root_proof,omitempty"`

	// RequireRootProof/RequireRootAttestation/DoNotTrustRoot instead require
	// an applicable attestation for the root identity as well.
	RequireRootProof       bool `json:"require_root_proof,omitempty"`
	RequireRootAttestation bool `json:"require_root_attestation,omitempty"`
	DoNotTrustRoot         bool `json:"do_not_trust_root,omitempty"`
}

// Status tells whether analysis produced complete regions or had to return an
// explicit undecided result (currently due to node budget exhaustion).
type Status string

const (
	StatusDecided   Status = "decided"
	StatusUnknown   Status = "unknown"
	StatusUndecided        = StatusUnknown
)

// Decision aliases for hosts that describe budget exhaustion as unknown.
const (
	DecisionDecided   = StatusDecided
	DecisionUnknown   = StatusUnknown
	DecisionUndecided = StatusUndecided
)

// RegionName identifies one of the three output regions.
type RegionName string

const (
	RegionPass        RegionName = "pass"
	RegionReject      RegionName = "reject"
	RegionUnbuildable RegionName = "unbuildable"
)

// Report contains the three shared ROBDD roots and replayable rejection
// evidence. A nil/zero-value Report with StatusUndecided must not be treated as
// an acceptance result.
type Report struct {
	Status Status `json:"status"`

	// Graph owns Pass, Reject, Unbuildable and Allowed nodes.
	Graph *Graph `json:"-"`

	Pass        NodeID `json:"pass_node"`
	Reject      NodeID `json:"reject_node"`
	Unbuildable NodeID `json:"unbuildable_node"`
	Allowed     NodeID `json:"allowed_node"`

	// NodeCount is the shared graph's allocated nonterminal node count.
	NodeCount int `json:"node_count"`
	Budget    int `json:"budget"`

	// Evidence contains one independently checkable witness per discovered
	// bad-resolution/unproven-identity pair.
	Evidence []Evidence `json:"evidence,omitempty"`

	// Undecided is true when Status == StatusUndecided. Cause wraps the reason.
	Undecided      bool  `json:"undecided,omitempty"`
	Unknown        bool  `json:"unknown,omitempty"`
	BudgetExceeded bool  `json:"budget_exceeded,omitempty"`
	Cause          error `json:"-"`
}

// Result is an alias for hosts that prefer that name.
type Result = Report

// BDDSet is an alias for the three output diagrams.
type BDDSet = Report

// Analysis is another descriptive Report alias.
type Analysis = Report

// Decided reports whether all three regions are complete.
func (r *Report) Decided() bool { return r != nil && r.Status == StatusDecided }

// IsUndecided reports whether analysis returned an explicit undecided result.
func (r *Report) IsUndecided() bool {
	return r == nil || r.Status == StatusUndecided || r.Status == StatusUnknown || r.Undecided
}

// Err returns the non-fatal reason for an undecided report.
func (r *Report) Err() error {
	if r == nil {
		return fmt.Errorf("closure: nil report")
	}
	return r.Cause
}

// Region evaluates all three diagrams and returns the unique region. It returns
// an empty RegionName if the assignment is outside the allowed constraint set.
func (r *Report) Region(config map[string]bool) RegionName {
	if r == nil || r.Graph == nil || !r.Decided() {
		return ""
	}
	if r.Graph.Eval(r.Pass, config) {
		return RegionPass
	}
	if r.Graph.Eval(r.Reject, config) {
		return RegionReject
	}
	if r.Graph.Eval(r.Unbuildable, config) {
		return RegionUnbuildable
	}
	return ""
}

// EvalRegion evaluates one named output region.
func (r *Report) EvalRegion(region RegionName, config map[string]bool) bool {
	if r == nil || r.Graph == nil {
		return false
	}
	switch region {
	case RegionPass:
		return r.Graph.Eval(r.Pass, config)
	case RegionReject:
		return r.Graph.Eval(r.Reject, config)
	case RegionUnbuildable:
		return r.Graph.Eval(r.Unbuildable, config)
	case "allowed":
		return r.Graph.Eval(r.Allowed, config)
	default:
		return false
	}
}

// Assignments enumerates all satisfying assignments for one output region.
// It is primarily intended for exhaustive checks with at most six variables.
func (r *Report) Assignments(region RegionName) []Assignment {
	if r == nil || r.Graph == nil || !r.Decided() {
		return nil
	}
	switch region {
	case RegionPass:
		return r.Graph.AllAssignments(r.Pass)
	case RegionReject:
		return r.Graph.AllAssignments(r.Reject)
	case RegionUnbuildable:
		return r.Graph.AllAssignments(r.Unbuildable)
	default:
		return nil
	}
}
