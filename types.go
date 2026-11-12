package netsec

// Severity levels assigned to analysis findings.
const (
	SeverityRisk    = "risk"
	SeverityWarning = "warning"
)

// Stable finding identifiers emitted by the package.
const (
	FindingInvalidRegex            = "invalid-regex"
	FindingOverbroadRegex          = "overbroad-regex"
	FindingUnescapedDot            = "unescaped-dot"
	FindingUncoveredDynamicVersion = "uncovered-dynamic-version"
	FindingUncoveredTrustRule      = "uncovered-trust-rule"
	FindingVersionNotInLockfile    = "version-not-in-lockfile"
	FindingUnknownChecksumAlgo     = "unknown-checksum-algorithm"
)

// Checksum is a single digest entry for an artifact.
type Checksum struct {
	// Algorithm is the lower-cased element name, for example "sha256".
	Algorithm string
	// Value is the raw (typically hex-encoded) digest.
	Value string
	// AlsoTrust reports whether the entry comes from an also-trust
	// element rather than a primary checksum element.
	AlsoTrust bool
}

// Artifact is one file attached to a component.
type Artifact struct {
	Name      string
	Checksums []Checksum
}

// Component is one parsed <component> entry.
type Component struct {
	Group     string
	Name      string
	Version   string
	Artifacts []Artifact
}

// TrustRule is one <trust> entry from trusted-artifacts.
// Optional attributes are nil when absent from the XML.
type TrustRule struct {
	Group         *string
	Name          *string
	Version       *string
	File          *string
	Regex         bool
	AlsoTrustAlgs []string
}

// Metadata is the parsed content of verification-metadata.xml.
type Metadata struct {
	Components  []Component
	TrustRules  []TrustRule
	AlsoTrust   []string
	ParseErrors []string
}

// LockEntry is one coordinate recorded in a Gradle dependency lockfile.
type LockEntry struct {
	Group      string
	Name       string
	Version    string
	Classifier string
	Extension  string
}

// Lockfile is the parsed content of a gradle.lockfile.
type Lockfile struct {
	Entries []LockEntry
}

// ArtifactRef identifies the downloaded artifact that needs a decision.
type ArtifactRef struct {
	Group   string
	Name    string
	Version string
	File    string
}

// Finding describes one detected risk or bypass condition.
type Finding struct {
	ID       string
	Severity string
	Message  string
	Rule     *TrustRule
}

// Decision is the verification result for one artifact.
type Decision struct {
	Allowed  bool
	Reason   string
	Findings []Finding
}
