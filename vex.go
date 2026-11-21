// Package vex verifies publisher-supplied VEX claims for Go release artifacts.
//
// The package consumes signed symbol summaries and signed link manifests. It
// never executes an artifact and never interprets a binary. All conclusions are
// derived from declared object identities, archive group order, conditions,
// symbol bindings, and references.
//
// An object identity is the triple (compiler, source digest, build
// configuration). A symbol name is never used as a substitute for that
// identity.
package vex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const Version = "1"

// Verdict is the result of accepting a VEX claim.
type Verdict string

const (
	// NotAffected means every allowed, linkable configuration, and every legal
	// weak/group binding for those configurations, is entry-unreachable from
	// every marked object identity.
	NotAffected Verdict = "not_affected"
	// Affected means at least one allowed, linkable configuration and legal
	// binding reaches a marked object identity.
	Affected Verdict = "affected"
	// Unknown means the available signed evidence is insufficient to prove
	// either of the two conclusions above.
	Unknown Verdict = "unknown"
)

// Status is retained as a descriptive alias for Verdict.
type Status = Verdict
type VEXStatus = Verdict

const (
	StatusNotAffected = NotAffected
	StatusAffected    = Affected
	StatusUnknown     = Unknown
	Unaffected        = NotAffected
)

func (v Verdict) String() string { return string(v) }

// ConfigVerdict describes the disposition of one deployer-allowed build
// configuration.
type ConfigVerdict string

const (
	ConfigNotAffected ConfigVerdict = "not_affected"
	ConfigAffected    ConfigVerdict = "affected"
	ConfigUnknown     ConfigVerdict = "unknown"
	ConfigNotLinkable ConfigVerdict = "not_linkable"
)

// BindingKind distinguishes ordinary definitions from implementations that
// participate in a choose-one group.
type BindingKind string

const (
	BindingStrong    BindingKind = "strong"
	BindingWeak      BindingKind = "weak"
	BindingGroup     BindingKind = "group"
	BindingChooseOne             = BindingGroup

	Strong    = BindingStrong
	Weak      = BindingWeak
	Group     = BindingGroup
	Grouped   = BindingGroup
	ChooseOne = BindingGroup
)

// Signature is the structural signature envelope understood by this package.
// A SignatureVerifier can be supplied when a real cryptographic trust root is
// available. If no verifier is supplied, presence plus an optional payload
// digest is checked.
type Signature struct {
	Algorithm     string `json:"algorithm,omitempty"`
	KeyID         string `json:"keyId,omitempty"`
	Value         string `json:"value,omitempty"`
	PayloadDigest string `json:"payloadDigest,omitempty"`
	Signed        bool   `json:"signed,omitempty"`
}

// SignatureVerifier verifies that signature authenticates payload. signature
// may be a Signature, a string, []byte, or another value supplied by the host.
type SignatureVerifier interface {
	Verify(payload []byte, signature any) error
}

// SignatureVerifierFunc adapts a function to SignatureVerifier.
type SignatureVerifierFunc func(payload []byte, signature any) error

func (f SignatureVerifierFunc) Verify(payload []byte, signature any) error {
	return f(payload, signature)
}

// ObjectID identifies one object in a release build. BuildConfig is the
// canonical field; Config is accepted as an input alias.
type ObjectID struct {
	Compiler      string `json:"compiler"`
	SourceDigest  string `json:"sourceDigest"`
	BuildConfig   string `json:"buildConfig,omitempty"`
	Config        string `json:"config,omitempty"`
	CompilerID    string `json:"compilerId,omitempty"`
	Digest        string `json:"digest,omitempty"`
	SourceHash    string `json:"sourceHash,omitempty"`
	Configuration string `json:"configuration,omitempty"`
}

// Reference is a symbol reference. Symbol is canonical; Name and Target are
// accepted aliases. Condition is a build-tag expression. Optional references do
// not make a configuration unlinkable when unresolved.
type Reference struct {
	Symbol     string   `json:"symbol"`
	Name       string   `json:"name,omitempty"`
	Target     string   `json:"target,omitempty"`
	Condition  string   `json:"condition,omitempty"`
	When       string   `json:"when,omitempty"`
	Conditions []string `json:"conditions,omitempty"`
	Optional   bool     `json:"optional,omitempty"`
	Weak       bool     `json:"weak,omitempty"`
	Indirect   bool     `json:"indirect,omitempty"`
}

// Symbol is one exported implementation supplied by an object.
type Symbol struct {
	Name        string      `json:"name"`
	Object      ObjectID    `json:"object,omitempty"`
	ObjectID    ObjectID    `json:"objectId,omitempty"`
	Binding     BindingKind `json:"binding,omitempty"`
	Kind        BindingKind `json:"kind,omitempty"`
	Linkage     BindingKind `json:"linkage,omitempty"`
	Group       string      `json:"group,omitempty"`
	GroupID     string      `json:"groupId,omitempty"`
	ChoiceGroup string      `json:"choiceGroup,omitempty"`
	Condition   string      `json:"condition,omitempty"`
	Conditions  []string    `json:"conditions,omitempty"`
	Strong      bool        `json:"strong,omitempty"`
	Weak        bool        `json:"weak,omitempty"`
	References  []Reference `json:"references,omitempty"`
	Refs        []string    `json:"refs,omitempty"`
}

// Object is a signed symbol summary. ID is canonical; ObjectID is an input
// alias. References at object level become active whenever the object is
// linked. Symbol-level references become active when that symbol is selected.
type Object struct {
	ID          ObjectID    `json:"id"`
	ObjectID    ObjectID    `json:"objectId,omitempty"`
	Symbols     []Symbol    `json:"symbols,omitempty"`
	Definitions []Symbol    `json:"definitions,omitempty"`
	References  []Reference `json:"references,omitempty"`
	Refs        []string    `json:"refs,omitempty"`
	Conditions  []string    `json:"conditions,omitempty"`
	Incomplete  bool        `json:"incomplete,omitempty"`
	Signature   any         `json:"signature,omitempty"`
	Signed      bool        `json:"signed,omitempty"`
}

// SymbolSummary is a descriptive alias for Object.
type SymbolSummary = Object

// SignedSymbolSummary is a wrapper accepted by Evidence. The inner summary may
// also carry its own signature.
type SignedSymbolSummary struct {
	Summary   Object   `json:"summary"`
	Object    Object   `json:"object,omitempty"`
	ObjectID  ObjectID `json:"objectId,omitempty"`
	Signature any      `json:"signature,omitempty"`
	Signed    bool     `json:"signed,omitempty"`
}

// ArchiveGroup is one archive/scan group. Groups are processed in declaration
// order. Members are considered in declaration order during each fixed-point
// pass.
type ArchiveGroup struct {
	ID         string     `json:"id,omitempty"`
	Name       string     `json:"name,omitempty"`
	Members    []ObjectID `json:"members,omitempty"`
	Objects    []ObjectID `json:"objects,omitempty"`
	Condition  string     `json:"condition,omitempty"`
	Conditions []string   `json:"conditions,omitempty"`
	Incomplete bool       `json:"incomplete,omitempty"`
}

// LinkManifest is a signed description of entries, archive groups, and
// linkability. Incomplete means missing definitions cannot be interpreted as
// proof that a configuration is unlinkable.
type LinkManifest struct {
	ID               string         `json:"id,omitempty"`
	Config           string         `json:"config,omitempty"`
	BuildConfig      string         `json:"buildConfig,omitempty"`
	Configuration    string         `json:"configuration,omitempty"`
	EntrySymbols     []string       `json:"entrySymbols,omitempty"`
	Entry            []string       `json:"entry,omitempty"`
	Entries          []Reference    `json:"entries,omitempty"`
	EntryRefs        []Reference    `json:"entryRefs,omitempty"`
	EntryReferences  []Reference    `json:"entryReferences,omitempty"`
	ArchiveGroups    []ArchiveGroup `json:"archiveGroups,omitempty"`
	Groups           []ArchiveGroup `json:"groups,omitempty"`
	Objects          []ObjectID     `json:"objects,omitempty"`
	Condition        string         `json:"condition,omitempty"`
	Conditions       []string       `json:"conditions,omitempty"`
	Complete         bool           `json:"complete,omitempty"`
	Incomplete       bool           `json:"incomplete,omitempty"`
	Linkable         bool           `json:"linkable,omitempty"`
	LinkabilityKnown bool           `json:"linkabilityKnown,omitempty"`
	Signature        any            `json:"signature,omitempty"`
	Signed           bool           `json:"signed,omitempty"`
}

// SignedLinkManifest is a wrapper accepted by Evidence.
type SignedLinkManifest struct {
	Manifest     LinkManifest `json:"manifest"`
	LinkManifest LinkManifest `json:"linkManifest,omitempty"`
	Signature    any          `json:"signature,omitempty"`
	Signed       bool         `json:"signed,omitempty"`
}

// CandidateConfig is a signed build configuration offered by the host.
type CandidateConfig struct {
	ID                string        `json:"id,omitempty"`
	Config            string        `json:"config,omitempty"`
	BuildConfig       string        `json:"buildConfig,omitempty"`
	Configuration     string        `json:"configuration,omitempty"`
	Tags              []string      `json:"tags,omitempty"`
	BuildTags         []string      `json:"buildTags,omitempty"`
	ActiveConditions  []string      `json:"activeConditions,omitempty"`
	Allowed           bool          `json:"allowed,omitempty"`
	AllowedByDeployer bool          `json:"allowedByDeployer,omitempty"`
	DeployerAllowed   bool          `json:"deployerAllowed,omitempty"`
	Linkable          bool          `json:"linkable,omitempty"`
	LinkabilityKnown  bool          `json:"linkabilityKnown,omitempty"`
	ManifestID        string        `json:"manifestId,omitempty"`
	Manifest          *LinkManifest `json:"manifest,omitempty"`
	Signature         any           `json:"signature,omitempty"`
	Signed            bool          `json:"signed,omitempty"`
}

// Evidence is the complete input to acceptance. Alias fields are accepted so
// that hosts using "objects", "summaries", or "manifests" terminology can pass
// their native records directly.
type Evidence struct {
	Artifact      string `json:"artifact,omitempty"`
	Vulnerability string `json:"vulnerability,omitempty"`

	Marked        []ObjectID `json:"marked,omitempty"`
	MarkedObjects []ObjectID `json:"markedObjects,omitempty"`
	Vulnerable    []ObjectID `json:"vulnerable,omitempty"`

	AllowedConfigs         []string `json:"allowedConfigs,omitempty"`
	Allowed                []string `json:"allowed,omitempty"`
	DeployerAllowedConfigs []string `json:"deployerAllowedConfigs,omitempty"`

	Configs    []CandidateConfig `json:"configs,omitempty"`
	Candidates []CandidateConfig `json:"candidates,omitempty"`

	Manifests       []LinkManifest       `json:"manifests,omitempty"`
	LinkManifests   []LinkManifest       `json:"linkManifests,omitempty"`
	SignedManifests []SignedLinkManifest `json:"signedManifests,omitempty"`

	Summaries       []Object              `json:"summaries,omitempty"`
	Objects         []Object              `json:"objects,omitempty"`
	SymbolSummaries []Object              `json:"symbolSummaries,omitempty"`
	SignedSummaries []SignedSymbolSummary `json:"signedSummaries,omitempty"`

	RequireSignatures *bool             `json:"requireSignatures,omitempty"`
	Verifier          SignatureVerifier `json:"-"`
	MaxBindingStates  int               `json:"maxBindingStates,omitempty"`
}

// ArtifactEvidence is a descriptive alias for Evidence.
type ArtifactEvidence = Evidence

// Descriptive aliases used by release hosts.
type Input = Evidence
type Request = Evidence
type Claim = Evidence
type Candidate = CandidateConfig
type Config = CandidateConfig
type Manifest = LinkManifest
type Summary = Object
type SymbolDefinition = Symbol
type ObjectKey = ObjectID
type ObjectIdentity = ObjectID
type Result = Certificate
type Proof = Certificate
type Archive = ArchiveGroup

// Options supplies non-evidence verification policy.
type Options struct {
	RequireSignatures *bool
	Verifier          SignatureVerifier
	MaxBindingStates  int
}

// Certificate is an independently checkable proof of a VEX verdict. Verify
// recomputes it from the same signed evidence.
type Certificate struct {
	Version           string        `json:"version"`
	Verdict           Verdict       `json:"verdict"`
	Reason            string        `json:"reason,omitempty"`
	Artifact          string        `json:"artifact,omitempty"`
	Vulnerability     string        `json:"vulnerability,omitempty"`
	Marked            []ObjectID    `json:"marked,omitempty"`
	AllowedConfigs    []string      `json:"allowedConfigs,omitempty"`
	Configs           []ConfigProof `json:"configs,omitempty"`
	EvidenceDigest    string        `json:"evidenceDigest"`
	CertificateDigest string        `json:"certificateDigest"`
}

// ConfigProof is the extraction, binding, and reachability proof for one
// deployer-allowed configuration.
type ConfigProof struct {
	Config           string            `json:"config"`
	Verdict          ConfigVerdict     `json:"verdict"`
	Linkable         bool              `json:"linkable"`
	EvidenceComplete bool              `json:"evidenceComplete"`
	MissingEvidence  []string          `json:"missingEvidence,omitempty"`
	Extracted        []ObjectID        `json:"extracted,omitempty"`
	Extraction       []ExtractionStep  `json:"extraction,omitempty"`
	Groups           []GroupSelection  `json:"groups,omitempty"`
	Bindings         []BindingSet      `json:"bindings,omitempty"`
	Reachable        []ObjectID        `json:"reachable,omitempty"`
	MarkedReachable  []ObjectID        `json:"markedReachable,omitempty"`
	Conditions       []ConditionResult `json:"conditions,omitempty"`
	Enumerated       int               `json:"enumerated"`
	Notes            []string          `json:"notes,omitempty"`
}

// ExtractionStep records why an object was pulled from a group.
type ExtractionStep struct {
	Group     string   `json:"group"`
	Index     int      `json:"index"`
	Object    ObjectID `json:"object"`
	Satisfied []string `json:"satisfied,omitempty"`
	NewRefs   []string `json:"newRefs,omitempty"`
}

// GroupSelection records one consistent choose-one-group choice.
type GroupSelection struct {
	Group  string   `json:"group"`
	Object ObjectID `json:"object"`
}

// BindingChoice records the implementation selected for a symbol.
type BindingChoice struct {
	Symbol string      `json:"symbol"`
	Object ObjectID    `json:"object"`
	Kind   BindingKind `json:"kind"`
}

// BindingSet is one complete legal weak/group binding.
type BindingSet struct {
	Index           int              `json:"index"`
	Groups          []GroupSelection `json:"groups,omitempty"`
	Bindings        []BindingChoice  `json:"bindings,omitempty"`
	Reachable       []ObjectID       `json:"reachable,omitempty"`
	MarkedReachable []ObjectID       `json:"markedReachable,omitempty"`
	Linkable        bool             `json:"linkable"`
	Failure         string           `json:"failure,omitempty"`
}

// ConditionResult records one evaluated build condition.
type ConditionResult struct {
	Owner      string `json:"owner"`
	Expression string `json:"expression"`
	Result     bool   `json:"result"`
}

// Error types.
var (
	ErrInvalidInput       = errors.New("vex: invalid input")
	ErrInvalidCertificate = errors.New("vex: invalid certificate")
)

// Analyze accepts Evidence or *Evidence, an Options value, or the JSON encoding
// of Evidence. It returns a deterministic certificate. Verification failures in
// signed evidence are represented as an Unknown verdict, not as a panic.
func Analyze(input any, options ...any) (*Certificate, error) {
	ev, err := coerceEvidence(input)
	if err != nil {
		return nil, err
	}
	opts, err := coerceOptions(options...)
	if err != nil {
		return nil, err
	}
	return analyze(ev, opts)
}

// Evaluate is an alias for Analyze.
func Evaluate(input any, options ...any) (*Certificate, error) {
	return Analyze(input, options...)
}

// Accept is an alias for Analyze.
func Accept(input any, options ...any) (*Certificate, error) {
	return Analyze(input, options...)
}

// Check is an alias for Analyze.
func Check(input any, options ...any) (*Certificate, error) {
	return Analyze(input, options...)
}

// Prove is an alias for Analyze.
func Prove(input any, options ...any) (*Certificate, error) {
	return Analyze(input, options...)
}

// EvaluateArtifact is an alias for Analyze.
func EvaluateArtifact(input any, options ...any) (*Certificate, error) {
	return Analyze(input, options...)
}

// Decide returns only the verdict from a fresh analysis.
func Decide(input any, options ...any) (Verdict, error) {
	cert, err := Analyze(input, options...)
	if err != nil {
		return Unknown, err
	}
	return cert.Verdict, nil
}

// VerifyCertificate recomputes the certificate from evidence. When evidence is
// omitted, only the certificate's internal digest is checked.
func VerifyCertificate(cert *Certificate, evidence ...any) error {
	return Verify(cert, evidence...)
}

// Verify checks a certificate. With evidence, verification is a full replay.
func Verify(cert *Certificate, evidence ...any) error {
	if cert == nil {
		return fmt.Errorf("%w: nil certificate", ErrInvalidCertificate)
	}
	if cert.Version != Version {
		return fmt.Errorf("%w: unsupported version %q", ErrInvalidCertificate, cert.Version)
	}
	if cert.Verdict != NotAffected && cert.Verdict != Affected && cert.Verdict != Unknown {
		return fmt.Errorf("%w: invalid verdict %q", ErrInvalidCertificate, cert.Verdict)
	}
	if err := checkCertificateDigest(cert); err != nil {
		return err
	}
	if len(evidence) == 0 {
		return nil
	}
	ev, err := coerceEvidence(evidence[0])
	if err != nil {
		return err
	}
	opts, err := coerceOptions(evidence[1:]...)
	if err != nil {
		return err
	}
	recomputed, err := analyze(ev, opts)
	if err != nil {
		return err
	}
	if recomputed.Verdict != cert.Verdict ||
		recomputed.EvidenceDigest != cert.EvidenceDigest ||
		recomputed.CertificateDigest != cert.CertificateDigest {
		return fmt.Errorf("%w: certificate does not match replayed evidence", ErrInvalidCertificate)
	}
	if !equalJSON(recomputed, cert) {
		return fmt.Errorf("%w: proof details were modified", ErrInvalidCertificate)
	}
	return nil
}

// Validate is an alias for VerifyCertificate.
func Validate(cert *Certificate, evidence ...any) error {
	return VerifyCertificate(cert, evidence...)
}

// VerifyProof is an alias for VerifyCertificate.
func VerifyProof(cert *Certificate, evidence ...any) error {
	return VerifyCertificate(cert, evidence...)
}

// Verify calls Verify on the receiver.
func (c *Certificate) Verify(evidence ...any) error {
	return Verify(c, evidence...)
}

// Status returns the certificate verdict.
func (c *Certificate) Status() Verdict {
	if c == nil {
		return Unknown
	}
	return c.Verdict
}

// IsNotAffected reports whether the certificate proves the narrow VEX claim.
func (c *Certificate) IsNotAffected() bool {
	return c != nil && c.Verdict == NotAffected
}

func coerceEvidence(input any) (Evidence, error) {
	switch v := input.(type) {
	case Evidence:
		return v, nil
	case *Evidence:
		if v == nil {
			return Evidence{}, fmt.Errorf("%w: nil evidence", ErrInvalidInput)
		}
		return *v, nil
	case []byte:
		var ev Evidence
		if err := json.Unmarshal(v, &ev); err != nil {
			return Evidence{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		return ev, nil
	case string:
		var ev Evidence
		if err := json.Unmarshal([]byte(v), &ev); err != nil {
			return Evidence{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		return ev, nil
	default:
		return Evidence{}, fmt.Errorf("%w: unsupported evidence type %T", ErrInvalidInput, input)
	}
}

func coerceOptions(options ...any) (Options, error) {
	var out Options
	for _, value := range options {
		switch v := value.(type) {
		case Options:
			out = v
		case *Options:
			if v == nil {
				return out, fmt.Errorf("%w: nil options", ErrInvalidInput)
			}
			out = *v
		case SignatureVerifier:
			out.Verifier = v
		case nil:
			continue
		default:
			return out, fmt.Errorf("%w: unsupported option type %T", ErrInvalidInput, value)
		}
	}
	return out, nil
}

func normalizeID(id ObjectID) ObjectID {
	return ObjectID{
		Compiler:     strings.TrimSpace(firstNonEmpty(id.Compiler, id.CompilerID)),
		SourceDigest: strings.TrimSpace(firstNonEmpty(id.SourceDigest, id.Digest, id.SourceHash)),
		BuildConfig: firstNonEmpty(
			strings.TrimSpace(id.BuildConfig),
			strings.TrimSpace(id.Config),
			strings.TrimSpace(id.Configuration),
		),
	}
}

func idKey(id ObjectID) string {
	id = normalizeID(id)
	return id.Compiler + "\x00" + id.SourceDigest + "\x00" + id.BuildConfig
}

func memberKey(id ObjectID) string {
	id = normalizeID(id)
	return id.Compiler + "\x00" + id.SourceDigest
}

func (id ObjectID) String() string {
	id = normalizeID(id)
	if id.BuildConfig == "" {
		return id.Compiler + "@" + id.SourceDigest
	}
	return id.Compiler + "@" + id.SourceDigest + "/" + id.BuildConfig
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func boolOption(ptr *bool, fallback bool) bool {
	if ptr == nil {
		return fallback
	}
	return *ptr
}

func hashJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func equalDigest(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == right {
		return true
	}
	left = strings.TrimPrefix(left, "sha256:")
	right = strings.TrimPrefix(right, "sha256:")
	return strings.EqualFold(left, right)
}

func certificateDigest(cert *Certificate) (string, error) {
	copy := *cert
	copy.CertificateDigest = ""
	return hashJSON(copy)
}

func checkCertificateDigest(cert *Certificate) error {
	if cert.CertificateDigest == "" {
		return fmt.Errorf("%w: empty certificate digest", ErrInvalidCertificate)
	}
	digest, err := certificateDigest(cert)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
	}
	if digest != cert.CertificateDigest {
		return fmt.Errorf("%w: certificate digest mismatch", ErrInvalidCertificate)
	}
	return nil
}

func equalJSON(a, b any) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(left) == string(right)
}

func sortIDs(ids []ObjectID) {
	sort.Slice(ids, func(i, j int) bool { return idKey(ids[i]) < idKey(ids[j]) })
}

func sortStrings(values []string) {
	sort.Strings(values)
}
