package netsec

import "fmt"

// Error codes returned by Verify. Each value is stable and suitable for
// programmatic matching by a build gateway.
const (
	// ErrInvalidLockFile indicates the lock file is structurally unusable.
	ErrInvalidLockFile = "E_LOCK_INVALID"

	// ErrUnknownEntryType indicates an entry "type" is not one of Direct,
	// Transitive or CentralTransitive.
	ErrUnknownEntryType = "E_ENTRY_TYPE_UNKNOWN"

	// ErrMissingContentHash indicates an entry carries no contentHash.
	ErrMissingContentHash = "E_HASH_MISSING"

	// ErrNonStandardBase64 indicates a contentHash uses the URL-safe base64
	// alphabet or another non-canonical standard base64 encoding.
	ErrNonStandardBase64 = "E_HASH_ENCODING"

	// ErrHashMismatch indicates the contentHash does not equal the SHA-512
	// standard base64 of the .nupkg bytes supplied by the caller.
	ErrHashMismatch = "E_HASH_MISMATCH"

	// ErrHashUnavailable indicates the caller did not supply the actual
	// .nupkg hash for a referenced package.
	ErrHashUnavailable = "E_HASH_UNAVAILABLE"

	// ErrInvalidVersion indicates a requested or resolved version string is
	// not a parseable NuGet version.
	ErrInvalidVersion = "E_VERSION_INVALID"

	// ErrVersionOutOfRange indicates the resolved version does not satisfy
	// the requested version range.
	ErrVersionOutOfRange = "E_VERSION_OUT_OF_RANGE"

	// ErrVersionDowngrade indicates the resolved version is below the lower
	// bound of the requested range.
	ErrVersionDowngrade = "E_VERSION_DOWNGRADE"

	// ErrDirectNotInRoot indicates a Direct entry has no counterpart in the
	// root framework dependency list.
	ErrDirectNotInRoot = "E_DIRECT_NOT_IN_ROOT"

	// ErrDuplicateResolved indicates the same package id (case-insensitive)
	// appears twice under one framework with different resolved versions.
	ErrDuplicateResolved = "E_DUPLICATE_RESOLVED"

	// ErrOrphanEntry indicates a non-Direct entry is not referenced by any
	// other entry's dependencies in the same framework.
	ErrOrphanEntry = "E_ORPHAN_ENTRY"

	// ErrDependencyMissing indicates an entry depends on a package id that
	// has no entry in the same framework.
	ErrDependencyMissing = "E_DEPENDENCY_MISSING"

	// ErrDependencyCycle indicates the dependency graph contains a cycle.
	ErrDependencyCycle = "E_DEPENDENCY_CYCLE"

	// ErrUnreachable indicates an entry cannot be reached from any Direct entry
	// through dependency edges.
	ErrUnreachable = "E_CLOSURE_UNREACHABLE"
)

// VerifyError describes a single verification failure.
type VerifyError struct {
	// Code is one of the Err* constants.
	Code string
	// Framework is the target framework section containing the entry, if any.
	Framework string
	// PackageID is the entry package id, if the error is entry-scoped.
	PackageID string
	// Message is a human readable explanation.
	Message string
}

func (e VerifyError) Error() string {
	switch {
	case e.Framework != "" && e.PackageID != "":
		return fmt.Sprintf("%s: %s/%s: %s", e.Code, e.Framework, e.PackageID, e.Message)
	case e.Framework != "":
		return fmt.Sprintf("%s: %s: %s", e.Code, e.Framework, e.Message)
	case e.PackageID != "":
		return fmt.Sprintf("%s: %s: %s", e.Code, e.PackageID, e.Message)
	default:
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
}

// VerifyErrors aggregates all failures found in one Verify call.
type VerifyErrors []VerifyError

func (es VerifyErrors) Error() string {
	if len(es) == 0 {
		return "verification failed"
	}
	return es[0].Error()
}

// Codes returns the set of error codes present in the aggregated failures.
func (es VerifyErrors) Codes() map[string]bool {
	codes := make(map[string]bool, len(es))
	for _, e := range es {
		codes[e.Code] = true
	}
	return codes
}

// HasCode reports whether any failure uses the given code.
func (es VerifyErrors) HasCode(code string) bool {
	for _, e := range es {
		if e.Code == code {
			return true
		}
	}
	return false
}
