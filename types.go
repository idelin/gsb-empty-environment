package netsec

import (
	"errors"
	"strconv"
)

// Level is a totally ordered classification level. Higher values are more
// sensitive.
type Level int

const (
	LevelUnclassified Level = iota
	LevelConfidential
	LevelSecret
	LevelTopSecret
)

// String returns the human-readable name of the level.
func (l Level) String() string {
	switch l {
	case LevelUnclassified:
		return "UNCLASSIFIED"
	case LevelConfidential:
		return "CONFIDENTIAL"
	case LevelSecret:
		return "SECRET"
	case LevelTopSecret:
		return "TOP_SECRET"
	default:
		return "UNKNOWN(" + strconv.Itoa(int(l)) + ")"
	}
}

// Label is the security metadata bound to a specific content version.
type Label struct {
	Level Level
	Owner string
}

// Sentinels describing the security and integrity outcomes of the service.
var (
	// ErrLabelRequired is returned by Commit when no label is supplied.
	ErrLabelRequired = errors.New("netsec: security label is required to publish a version")
	// ErrDowngradeApproval is returned when a commit would lower the label
	// level and the caller has not registered an approval for that downgrade.
	ErrDowngradeApproval = errors.New("netsec: label downgrade requires a caller-issued approval")
	// ErrApprovalRejected is returned when an approval exists but does not
	// match the object, current version, or the from/to levels.
	ErrApprovalRejected = errors.New("netsec: downgrade approval does not match the requested change")
	// ErrVersionNotFound is returned when no readable version exists.
	ErrVersionNotFound = errors.New("netsec: object version not found")
	// ErrDigestMismatch is returned when stored content does not hash to the
	// digest recorded in the manifest.
	ErrDigestMismatch = errors.New("netsec: content digest does not match manifest")
	// ErrTampered is returned when a version directory is internally
	// inconsistent (for example, a wrong object or version in the manifest).
	ErrTampered = errors.New("netsec: version manifest is inconsistent or corrupted")
	// ErrMissingLabel is returned by Authorize when a version carries no
	// label. Missing labels default to deny.
	ErrMissingLabel = errors.New("netsec: version has no security label: default deny")
	// ErrDenied is returned when the reader's clearance is below the label.
	ErrDenied = errors.New("netsec: clearance is below the object label")
)

// manifest is the single on-disk record that binds version, digest and label.
type manifest struct {
	Version   int64  `json:"version"`
	Object    string `json:"object"`
	Digest    string `json:"digest"`
	Label     *Label `json:"label,omitempty"`
	CreatedAt string `json:"created_at"`
}

// CommitInput is one publication request.
type CommitInput struct {
	Object  string
	Content []byte
	Label   *Label
}

// DowngradeApproval is a caller-issued, single-use authorization to move one
// object from FromLevel to ToLevel while it is at FromVersion.
type DowngradeApproval struct {
	Object      string
	FromVersion int64
	FromLevel   Level
	ToLevel     Level
	Approver    string
	Reason      string
}

func (a DowngradeApproval) matches(object string, currentVersion int64, from, to Level) bool {
	return a.Object == object &&
		a.FromVersion == currentVersion &&
		a.FromLevel == from &&
		a.ToLevel == to
}

// failStage identifies a point inside the commit transaction where a failure
// can be injected (test support).
type failStage int

const (
	failStageNone failStage = iota
	failStageContent
	failStageManifest
	failStagePublish
	failStageCurrent
)

// Fail stage identifiers exported for use by tests in this package.
const (
	FailStageContent  = failStageContent
	FailStageManifest = failStageManifest
	FailStagePublish  = failStagePublish
	FailStageCurrent  = failStageCurrent
)

// errInjectedFailure is the synthetic error used by the test-only failure hook.
var errInjectedFailure = errors.New("netsec: injected storage write failure")
