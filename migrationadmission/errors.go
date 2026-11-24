package migrationadmission

import "errors"

var (
	// ErrShortEncoding is returned when a serialized structure is truncated.
	ErrShortEncoding = errors.New("migrationadmission: short encoding")
	// ErrTrailingBytes is returned when a serialized structure has extra bytes.
	ErrTrailingBytes = errors.New("migrationadmission: trailing bytes after structure")
	// ErrInvalidSignature means a root signature failed verification.
	ErrInvalidSignature = errors.New("migrationadmission: invalid root signature")
	// ErrLatenessExceeded means an observed unmatched-symbol buffer exceeded MaxLateness.
	ErrLatenessExceeded = errors.New("migrationadmission: unmatched symbol count exceeds 4")
	// ErrWrongLatenessBound means the declared bound disagrees with the
	// observed maximum backlog.
	ErrWrongLatenessBound = errors.New("migrationadmission: lateness certificate bound does not match observed backlog")
	// ErrUnauthorizedFold means two non-alias names would share a new
	// identifier without a complete alias proof.
	ErrUnauthorizedFold = errors.New("migrationadmission: non-alias names folded to the same new identifier")
	// ErrNoLoginIdentifier means the new policy admits legal names but none of
	// them can produce any login identifier, or the new language is empty.
	ErrNoLoginIdentifier = errors.New("migrationadmission: policy emits no usable login identifier")
	// ErrAliasProofIncomplete means an alias claim is malformed or binds a
	// different tenant/policy version.
	ErrAliasProofIncomplete = errors.New("migrationadmission: alias proof does not match tenant and policy version")
	// ErrCredentialExpired means the credential predates the current alias epoch.
	ErrCredentialExpired = errors.New("migrationadmission: credential was invalidated by an alias update")
	// ErrIllegalName means the raw name is rejected by the new grammar.
	ErrIllegalName = errors.New("migrationadmission: raw name is not legal under the new grammar")
)
