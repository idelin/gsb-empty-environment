package artifactflow

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Digest is an opaque content digest. The package never interprets its
// algorithm or encoding; two artifacts are content-equal only when the digest
// strings are exactly equal.
type Digest = string

// Role is the signer role declared by a root artifact or a build receipt.
type Role = string

// Consumption describes what a step does with one input artifact.
type Consumption uint8

const (
	// Share permits the input instance to be used by any number of edges.
	Share Consumption = iota
	// Consume permits the input instance to be used by at most one edge in a
	// build. A consumed instance cannot be consumed a second time.
	Consume
)

// String returns the stable name of the consumption mode.
func (c Consumption) String() string {
	switch c {
	case Share:
		return "share"
	case Consume:
		return "consume"
	default:
		return fmt.Sprintf("Consumption(%d)", uint8(c))
	}
}

// UnmarshalJSON accepts either the numeric form (0/1) or the names "share" and
// "consume", case-insensitively.
func (c *Consumption) UnmarshalJSON(data []byte) error {
	var number uint8
	if err := json.Unmarshal(data, &number); err == nil {
		if number > uint8(Consume) {
			return fmt.Errorf("artifactflow: unknown numeric consumption mode %d", number)
		}
		*c = Consumption(number)
		return nil
	}
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("artifactflow: consumption must be 0, 1, %q or %q: %w", "share", "consume", err)
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "share":
		*c = Share
	case "consume":
		*c = Consume
	default:
		return fmt.Errorf("artifactflow: unknown consumption mode %q", name)
	}
	return nil
}

// RootArtifact is an externally supplied, already signed artifact. Its ID is
// the provenance identifier reported in certificates and ambiguity results.
// IDs must be unique and may not start with the reserved prefix "receipt:",
// which identifies provenance roots created by input-less source steps.
type RootArtifact struct {
	ID     string `json:"id"`
	Tenant string `json:"tenant,omitempty"`
	Path   string `json:"path"`
	Digest Digest `json:"digest"`
	Signer Role   `json:"signer"`
}

// RenameRule permits an edge from a producer path to a consumer path. A rule
// contains either no capture (a full exact rename) or exactly one {name}
// component in both From and To. The same captured component must appear in
// both sides.
type RenameRule struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// InputPort is one logical input required by a receipt.
type InputPort struct {
	Path        string      `json:"path"`
	Digest      Digest      `json:"digest"`
	Consumption Consumption `json:"consumption"`
}

// OutputPort is one logical output produced by a receipt.
type OutputPort struct {
	Path   string `json:"path"`
	Digest Digest `json:"digest"`
}

// Receipt is one already signature-verified implementation of a build step.
// Receipts are grouped by Step. Steps are allowed to be supplied in any order,
// and a step may have up to three alternative receipts. Exactly one receipt
// per step participates in a build.
type Receipt struct {
	ID             string       `json:"id"`
	Step           string       `json:"step"`
	Implementation string       `json:"implementation,omitempty"`
	Tenant         string       `json:"tenant,omitempty"`
	Signer         Role         `json:"signer"`
	Inputs         []InputPort  `json:"inputs,omitempty"`
	Outputs        []OutputPort `json:"outputs,omitempty"`
	Renames        []RenameRule `json:"renames,omitempty"`
}

// FinalArtifact is one entry of the publishable manifest.
type FinalArtifact struct {
	Tenant          string `json:"tenant,omitempty"`
	Path            string `json:"path"`
	Digest          Digest `json:"digest"`
	Sensitive       bool   `json:"sensitive,omitempty"`
	RequiredSigners []Role `json:"requiredSigners,omitempty"`
}

// Policy carries release-wide signer constraints.
type Policy struct {
	// AllowedSigners, when non-empty, restricts every root and receipt that
	// participates in a build.
	AllowedSigners []Role `json:"allowedSigners,omitempty"`
}

// ReceiptSet is the complete, unordered input to the release gate.
type ReceiptSet struct {
	Roots    []RootArtifact  `json:"roots"`
	Receipts []Receipt       `json:"receipts"`
	Finals   []FinalArtifact `json:"finals"`
	Policy   Policy          `json:"policy,omitempty"`
}

// InstanceKind distinguishes source artifacts from step outputs in a
// certificate.
type InstanceKind string

const (
	InstanceRoot   InstanceKind = "root"
	InstanceOutput InstanceKind = "output"
)

// InstanceRef identifies one concrete artifact instance in a build. A name
// and digest alone are not enough: two sources may carry the same content.
type InstanceRef struct {
	Kind        InstanceKind `json:"kind"`
	RootID      string       `json:"rootId,omitempty"`
	ReceiptID   string       `json:"receiptId,omitempty"`
	OutputIndex int          `json:"outputIndex,omitempty"`
}

// Status is the release-gate verdict.
type Status string

const (
	StatusVerified  Status = "verified"
	StatusAmbiguous Status = "ambiguous"
	StatusRejected  Status = "rejected"
)

// ProblemCode is a stable machine-readable rejection reason.
type ProblemCode string

const (
	ProblemNoConsistentBuild ProblemCode = "no_consistent_build"
	ProblemMissingFinal      ProblemCode = "missing_final"
	ProblemSignerRole        ProblemCode = "signer_role_not_allowed"
)

// Problem describes a reason a receipt set was rejected.
type Problem struct {
	Code    ProblemCode `json:"code"`
	Message string      `json:"message"`
	Step    string      `json:"step,omitempty"`
}

// Result is the verdict returned by Verify. A nil error with StatusVerified
// means exactly one root-provenance attribution exists for every sensitive
// final artifact. StatusAmbiguous means at least two feasible attributions
// disagree; Ambiguity then contains both source groups.
type Result struct {
	Status      Status       `json:"status"`
	Certificate *Certificate `json:"certificate,omitempty"`
	Ambiguity   *Ambiguity   `json:"ambiguity,omitempty"`
	Problems    []Problem    `json:"problems,omitempty"`
	RootSets    []FinalRoots `json:"rootSets,omitempty"`
}

// OK reports whether the set was verified without ambiguity.
func (r *Result) OK() bool {
	return r != nil && r.Status == StatusVerified
}

// FinalRoots records the root provenance of one final artifact in the
// certificate carried by Result. Uniqueness is enforced for sensitive final
// artifacts; non-sensitive entries are still reported from that certificate.
type FinalRoots struct {
	Index int      `json:"index"`
	Roots []string `json:"roots"`
}

// FinalRootSet is the public summary form of FinalRoots.
type FinalRootSet = FinalRoots

// Attribution is one possible provenance attribution for a final artifact.
// Roots are the source identifiers; Receipts are the signed build receipts
// used along the derivation.
type Attribution struct {
	Roots       []string     `json:"roots"`
	Receipts    []string     `json:"receipts"`
	Signers     []Role       `json:"signers"`
	Instance    InstanceRef  `json:"instance"`
	Certificate *Certificate `json:"certificate,omitempty"`
}

// Ambiguity reports two feasible, conflicting attributions for one sensitive
// final artifact. It is enough to reject authorization without rebuilding.
type Ambiguity struct {
	FinalIndex int           `json:"finalIndex"`
	Artifact   FinalArtifact `json:"artifact"`
	First      Attribution   `json:"first"`
	Second     Attribution   `json:"second"`
}

// CertificateVersion is the certificate wire-format version.
const CertificateVersion = "artifactflow/v1"

// Selection records the chosen candidate receipt for one step.
type Selection struct {
	Step           string `json:"step"`
	Receipt        string `json:"receipt"`
	Implementation string `json:"implementation,omitempty"`
	Signer         Role   `json:"signer"`
}

// Edge records one artifact correspondence: a concrete source instance feeds
// one input port of a selected receipt.
type Edge struct {
	Step       string      `json:"step"`
	Receipt    string      `json:"receipt"`
	InputIndex int         `json:"inputIndex"`
	Source     InstanceRef `json:"source"`
	Rule       string      `json:"rule,omitempty"`
}

// ArtifactRecord records the provenance of one concrete instance used by the
// build.
type ArtifactRecord struct {
	Instance InstanceRef `json:"instance"`
	Tenant   string      `json:"tenant,omitempty"`
	Path     string      `json:"path"`
	Digest   Digest      `json:"digest"`
	Roots    []string    `json:"roots"`
	Receipts []string    `json:"receipts"`
	Signers  []Role      `json:"signers"`
}

// FinalRecord records which concrete instance satisfied a final manifest
// entry and what provenance it carried.
type FinalRecord struct {
	Index    int         `json:"index"`
	Tenant   string      `json:"tenant,omitempty"`
	Path     string      `json:"path"`
	Digest   Digest      `json:"digest"`
	Instance InstanceRef `json:"instance"`
	Roots    []string    `json:"roots"`
	Receipts []string    `json:"receipts"`
	Signers  []Role      `json:"signers"`
}

// Certificate is a self-contained, independently checkable witness of one
// globally consistent build. BodyHash covers the canonical JSON encoding of
// the certificate with BodyHash cleared.
type Certificate struct {
	Version    string           `json:"version"`
	BodyHash   string           `json:"bodyHash"`
	Selections []Selection      `json:"selections"`
	Edges      []Edge           `json:"edges"`
	Artifacts  []ArtifactRecord `json:"artifacts"`
	Finals     []FinalRecord    `json:"finals"`
}

// DecodeReceiptSet decodes an unordered receipt set from JSON.
func DecodeReceiptSet(r io.Reader) (*ReceiptSet, error) {
	var set ReceiptSet
	if err := decodeJSON(r, &set); err != nil {
		return nil, err
	}
	return &set, nil
}
