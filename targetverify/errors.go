package targetverify

import "errors"

// Fail-closed sentinel errors. Use errors.Is to match the failure class.
var (
	// ErrMalformed means a metadata document is structurally invalid.
	ErrMalformed = errors.New("targetverify: malformed metadata")
	// ErrInvalidConfig means the verifier configuration is unusable.
	ErrInvalidConfig = errors.New("targetverify: invalid verifier configuration")
	// ErrThresholdNotMet means a document has too few valid signatures.
	ErrThresholdNotMet = errors.New("targetverify: signature threshold not met")
	// ErrExpired means a metadata document's expiry time has passed.
	ErrExpired = errors.New("targetverify: metadata expired")
	// ErrRollback means a document version is lower than the last seen one.
	ErrRollback = errors.New("targetverify: metadata version rollback")
	// ErrECUMismatch means the Director does not name this ECU correctly.
	ErrECUMismatch = errors.New("targetverify: director target does not name this ECU")
	// ErrNoTargetForECU means the Director has no target for this ECU.
	ErrNoTargetForECU = errors.New("targetverify: director lists no target for this ECU")
	// ErrTargetMismatch means file metadata differs between repositories or
	// a downloaded artifact does not match the verified metadata.
	ErrTargetMismatch = errors.New("targetverify: target metadata mismatch")
	// ErrImageSideMissing means a Director target is absent from the Image
	// repository, so it must not be installed.
	ErrImageSideMissing = errors.New("targetverify: target missing on image repository")
)
