package artifactflow

import "encoding/hex"

// Path is a tenant-scoped logical artifact path. All paths are expressed
// relative to the tenant root and must never escape it.
type Path string

// Digest is a content digest in the form "alg:hex", for example
// "sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae".
// Digests are compared case-insensitively on the hex component.
type Digest string

// SignerRole identifies the role that signed a receipt or is permitted to
// sign a slot. The empty string acts as a wildcard when validating a slot.
type SignerRole string

// SourceID identifies an independently supplied root artifact, i.e. the
// provenance origin that a build instance derives everything else from.
type SourceID string

// Receipt is a build receipt whose cryptographic signature has already been
// verified by the caller. The flow verifier only reasons about the signed
// claims; it never verifies raw signatures itself.
//
// A receipt may describe an independently supplied root artifact (RootSource
// set, no derivation), an output produced by a step (StepID set), or both.
type Receipt struct {
	// ID is the caller-assigned unique identifier of the receipt.
	ID string
	// Path is the logical path of the artifact described by the receipt.
	Path Path
	// Digest is the content digest of the described artifact.
	Digest Digest
	// Signer is the role that produced the signature.
	Signer SignerRole
	// RootSource, when non-empty, marks this receipt as evidence of a root
	// artifact drawn from this source. Root receipts may also be step
	// outputs (for example an import step that records an external source).
	RootSource SourceID
	// StepID, when non-empty, states that the artifact was produced by an
	// output slot of this step.
	StepID string
}

// IsRoot reports whether the receipt can act as a root artifact.
func (r Receipt) IsRoot() bool { return r.RootSource != "" }

// IsProduced reports whether the receipt claims to be a step output.
func (r Receipt) IsProduced() bool { return r.StepID != "" }

// Slot describes one input or output slot of a candidate.
type Slot struct {
	// Name is unique within the candidate and referenced by Rule.Input / Output.
	Name string
	// Path is the declared logical path of the slot. It may be a full path
	// or a capture pattern such as "dist/{name}.tar.gz".
	Path Path
	// Digest is the content digest required by the slot. A bound receipt
	// must carry the same digest. When Path contains captures the digest
	// still pins the exact content, so a renamed clean artifact cannot be
	// silently replaced by same-name different-content data.
	Digest Digest
	// AllowedSigners restricts the receipt signer roles accepted by the
	// slot. An empty list accepts any signer role.
	AllowedSigners []SignerRole
	// Consumed is meaningful for inputs only. A consumed input can be used
	// on at most one edge of the whole build instance; a shareable input
	// (Consumed == false) may be forked to any number of edges.
	Consumed bool
}

// Rule declares one allowed rename edge between an input slot and an output
// slot. The output artifact must be produced through a rule; an output slot
// without rules is produced at its declared path and does not depend on a
// rename edge (it still depends on the candidate's inputs).
type Rule struct {
	// Input names an input slot of the same candidate.
	Input string
	// Output names an output slot of the same candidate.
	Output string
	// Target, when non-empty, overrides the output slot path pattern. It
	// may contain capture components which must bind consistently with the
	// captures of the input slot path.
	Target Path
}

// Candidate is one alternative implementation of a step. At most one
// candidate of each step may be realized in a single build instance, so
// outputs from different candidates can never be combined.
type Candidate struct {
	// Name is unique within the step.
	Name    string
	Inputs  []Slot
	Outputs []Slot
	Rules   []Rule
}

// Step groups mutually exclusive alternative candidates.
type Step struct {
	ID         string
	Candidates []Candidate
}

// ManifestEntry is one required final artifact of the release.
type ManifestEntry struct {
	Path   Path
	Digest Digest
	// Sensitive marks artifacts whose root-source attribution must be
	// identical across every globally consistent correspondence.
	Sensitive bool
}

// Problem is the input to the release-gate verification. All receipts are
// assumed to carry valid signatures already.
type Problem struct {
	// TenantRoot is the path the whole flow is scoped to. Normalized paths
	// must remain inside it.
	TenantRoot Path
	// Receipts is the unordered set of signed receipts.
	Receipts []Receipt
	// Steps declares the build topology. At most MaxSteps steps and
	// MaxCandidates candidates per step are permitted.
	Steps []Step
	// Manifest lists the required final artifacts.
	Manifest []ManifestEntry
	// Budget bounds the exhaustive search. Zero selects DefaultBudget.
	Budget int64
}

// Report is the verdict for a problem.
type Report struct {
	// Status is StatusOK, StatusAmbiguous or StatusInfeasible.
	Status Status
	// Certificate is present exactly for StatusOK and is independently
	// verifiable without re-running the build.
	Certificate *Certificate
	// Ambiguity is present exactly for StatusAmbiguous and carries two
	// feasible correspondences with different root-source attribution.
	Ambiguity *Ambiguity
	// Reason explains an infeasible verdict.
	Reason string
}

// Status is the classification of a verification report.
type Status string

const (
	// StatusOK means a unique root-source attribution exists for every
	// sensitive manifest entry.
	StatusOK Status = "ok"
	// StatusAmbiguous means two feasible correspondences disagree on the
	// root-source set of a sensitive manifest entry.
	StatusAmbiguous Status = "ambiguous"
	// StatusInfeasible means no globally consistent correspondence exists.
	StatusInfeasible Status = "infeasible"
)

// Ambiguity records two globally consistent correspondences that disagree on
// the root-source set of at least one sensitive manifest entry. Both source
// lists are self-contained identifiers, so the conflicting authorizations can
// be inspected without re-running the build.
type Ambiguity struct {
	// ManifestPath is the sensitive entry whose attribution is contested.
	ManifestPath Path
	// Digest is the digest of the contested manifest entry.
	Digest Digest
	// FirstSources and SecondSources are the two distinct root-source sets.
	FirstSources  []SourceID
	SecondSources []SourceID
	// First and Second are the full witness assignments.
	First  Assignment
	Second Assignment
}

// Assignment is one globally consistent mapping between receipts, steps and
// artifacts. It is included verbatim in certificates and ambiguity reports.
type Assignment struct {
	// Steps lists the realized step candidates in step order.
	Steps []RealizedStep
	// Roots lists the receipt IDs used as root artifacts, sorted.
	Roots []string
}

// RealizedStep records which candidate of a step was realized and which
// receipts were bound to its slots.
type RealizedStep struct {
	StepID         string
	CandidateName  string
	InputReceipts  []SlotBinding
	OutputReceipts []SlotBinding
}

// SlotBinding pairs a slot name with the receipt bound to it.
type SlotBinding struct {
	Slot      string
	ReceiptID string
}

// Limits enforced on problems.
const (
	MaxSteps      = 16
	MaxCandidates = 3
	// DefaultBudget bounds the exhaustive backtracking search when the
	// problem does not specify one.
	DefaultBudget int64 = 500_000
	// maxRealizationsPerCandidate bounds the enumeration of slot bindings.
	maxRealizationsPerCandidate = 2000
)

// ValidationError describes a malformed problem.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return "artifactflow: " + e.Reason
	}
	return "artifactflow: invalid " + e.Field + ": " + e.Reason
}

// BudgetExceededError reports that the exhaustive search hit its budget.
type BudgetExceededError struct {
	Budget int64
}

func (e *BudgetExceededError) Error() string {
	return "artifactflow: search budget exhausted (" + itoa(e.Budget) + "); attribution cannot be proven unique"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func normalizeDigest(d Digest) string {
	b := []byte(d)
	for i := range b {
		c := b[i]
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// digestHex validates the hex component of a digest.
func digestValid(d Digest) bool {
	s := string(d)
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			h := s[i+1:]
			if len(h) == 0 || len(h)%2 != 0 {
				return false
			}
			if _, err := hex.DecodeString(h); err != nil {
				return false
			}
			return true
		}
	}
	return false
}
