package pinpolicy

// Time is a discrete small-integer timestamp. All schedule boundaries use
// this type so endpoint comparisons (closed window edges versus open
// revocation edges) stay explicit.
type Time int64

// Seq is a monotonic policy sequence number.
type Seq int64

// KeyID identifies a pinned public key.
type KeyID string

// HostID identifies a terminal host. A policy only applies to its own host.
type HostID string

// Role is a signing role (for example "deploy" or "ops"). Each role carries
// its own threshold.
type Role string

// RootID identifies a trust root. The initial pinned root and the migration
// target root have distinct identifiers.
type RootID string

// NeverRevoked marks a certificate that has no revocation event on the
// schedule. It is the zero value of Cert.RevokedAt, so a freshly constructed
// certificate with no revocation set is live forever. Revocation itself is
// open semantics (live for t < RevokedAt); to revoke at time zero set
// RevokedAt explicitly together with Revoked=true.
const NeverRevoked Time = 0

// Cert is a finite certificate entry: a public key, the host it is scoped
// to, the role it may sign for, and its publication and revocation times.
// RootKey certificates use Role "root"; a RootKey value names the trust root
// the certificate belongs to.
type Cert struct {
	Key       KeyID
	Host      HostID
	Role      Role
	RootKey   RootID
	PublishAt Time
	RevokedAt Time
	// Revoked must be set to true together with RevokedAt; otherwise
	// RevokedAt is treated as NeverRevoked.
	Revoked bool
}

// RootRole is the role value used by trust-root certificates.
const RootRole Role = "root"

// Signature records one signature on a policy. The verifier counts distinct
// live signer keys per role; a key that has been revoked at the real online
// time does not count toward any threshold.
type Signature struct {
	Signer KeyID
	Role   Role
	Backup bool
}

// Policy is the unsigned policy body. Digest returns the canonical digest
// referenced by handover authorizations and by the predecessor digest field.
type Policy struct {
	Sequence   Seq
	Host       HostID
	ValidFrom  Time
	ValidUntil Time
	Primary    KeyID
	Backup     KeyID
	RootKey    RootID
	Thresholds map[Role]int
	PrevDigest string
}

// SignedPolicy binds a policy body to its signatures and release metadata.
type SignedPolicy struct {
	Policy     Policy
	Signatures []Signature
	PublishAt  Time
}

// Release is one item of the policy release schedule. Releases are indexed by
// host and monotonic sequence; two fetchable releases for one host sharing a
// sequence number are a conflicting, non-accepting schedule.
type Release struct {
	SignedPolicy
}

// Handover is a transition authorization. To switch the pinned root, the
// exact same (Host, PolicyDigest, Role) triple must be authorized by one
// live old-root key and one live new-root key.
type Handover struct {
	Host         HostID
	PolicyDigest string
	Role         Role
	OldRoot      RootID
	NewRoot      RootID
	OldSigner    KeyID
	NewSigner    KeyID
	PublishAt    Time
}

// Input is the complete bounded schedule handed to Verify.
type Input struct {
	// OldRoot is the trust root initially pinned by the offline terminals.
	OldRoot RootID
	// NewRoot is the migration target trust root.
	NewRoot RootID

	Certs     []Cert
	Releases  []Release
	Handovers []Handover

	// Checkpoints enumerates the possible device initial checkpoints.
	Checkpoints []Time
	// OfflineMax is the maximum tolerable offline duration: c is viable at
	// online time t iff 0 <= t-c <= OfflineMax.
	OfflineMax Time
	// SkewMin/SkewMax bound the inclusive local-clock error interval.
	SkewMin Time
	SkewMax Time

	// Horizon bounds the exhaustive timeline. Only partition elements
	// intersecting [HorizonFrom, HorizonUntil] are judged.
	HorizonFrom  Time
	HorizonUntil Time
}

// ElementKind distinguishes exact event points from open intervals.
type ElementKind string

// KindPoint and KindOpen are the two partition element kinds.
const (
	KindPoint ElementKind = "point"
	KindOpen  ElementKind = "open"
)

// Element is one piece of the endpoint partition of the real timeline.
type Element struct {
	Kind ElementKind
	// At is the exact time for a point element.
	At Time
	// From/Until are the open-interval endpoints (exclusive). For a point
	// they both equal At.
	From  Time
	Until Time
	// Sample is the integer actually evaluated: At for a point, an interior
	// integer for a non-empty open interval, zero when Empty is true.
	Sample Time
	// Empty marks open intervals between adjacent integers with no interior
	// integer. They carry no admissible online time and are reported only so
	// the partition is item-by-item checkable.
	Empty bool
}

// Reason explains why a release or window is rejected.
type Reason string

// Rejection reasons. MissingAuthorization values carry the exact gap.
const (
	ReasonNotPublished     Reason = "not_published"
	ReasonWindowClosed     Reason = "policy_window_closed"
	ReasonUnknownKey       Reason = "unknown_key"
	ReasonKeyRevoked       Reason = "key_revoked"
	ReasonRoleThreshold    Reason = "role_threshold_not_met"
	ReasonHandoverMissing  Reason = "handover_authorization_missing"
	ReasonHandoverKeyDead  Reason = "handover_key_revoked_or_unpublished"
	ReasonHostMismatch     Reason = "host_mismatch"
	ReasonSequenceConflict Reason = "duplicate_sequence_conflict"
	ReasonBackupRollback   Reason = "backup_key_rollback"
	ReasonChainBroken      Reason = "chain_link_broken"
	ReasonNoCheckpoint     Reason = "no_viable_checkpoint"
	ReasonNoMigration      Reason = "no_migration_target_published"
)

// MissingAuthorization pins down the exact handover gap reported for a
// failure. Either the absent role or the dead/absent side is recorded.
type MissingAuthorization struct {
	Host         HostID
	PolicyDigest string
	Role         Role
	MissingSide  string // "old", "new", or "both"
	Detail       string
}

// ReleaseVerdict is the per-release, per-online-case acceptance decision.
type ReleaseVerdict struct {
	Host     HostID
	Sequence Seq
	Reasons  []Reason
	Missing  []MissingAuthorization
}

// CaseKey identifies one admissible (checkpoint, skew) combination.
type CaseKey struct {
	Checkpoint Time
	Skew       Time
}

// OnlineCase is one evaluated partition element together with every
// admissible case and the per-release decisions seen there.
type OnlineCase struct {
	Element          Element
	RealTime         Time
	ViableCheckpoint bool
	Cases            []CaseKey
	ReleaseVerdicts  []ReleaseVerdict
	// Reachable is true when at least one checkpoint is viable at RealTime.
	Reachable bool
	// MigrationFetchable is true when at least one release rooted at NewRoot
	// for the host is published at RealTime. Only such elements are "legal
	// online windows" subject to the liveness obligation.
	MigrationFetchable bool
	// ChainOK is true when a full migration chain exists for every case.
	ChainOK bool
	// ChainFailure records the first chain-blocking reason per case.
	ChainFailure map[CaseKey]Reason
}

// Failure is the earliest blocking finding, with the exact time element,
// checkpoint and missing authorization.
type Failure struct {
	Element     Element
	Checkpoint  Time
	Skew        Time
	Reason      Reason
	Host        HostID
	Sequence    Seq
	Missing     []MissingAuthorization
	Description string
}

// Report is the item-by-item verifier output.
type Report struct {
	Elements []Element
	Windows  []OnlineCase

	// SafetyPass means no revoked/rollback key or unauthorized migration can
	// be accepted under any admissible online window, checkpoint or skew.
	SafetyPass bool
	// LivenessPass means every legal online window admits a verifiable
	// migration chain for every admissible case.
	LivenessPass bool
	Pass         bool

	// EarliestFailure is nil on a fully passing report.
	EarliestFailure *Failure

	// Stats summarize the exhaustive enumeration.
	Stats ReportStats
}

// ReportStats counts the enumerated space so the exhaustive claim is itself
// checkable.
type ReportStats struct {
	ElementCount        int
	EmptyIntervalCount  int
	OnlineCaseCount     int
	ReleaseCount        int
	HandoverCount       int
	CheckpointCount     int
	SkewCount           int
	LegalWindowCount    int
	BlockedReleaseCount int
}
