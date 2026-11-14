package netsec

import "errors"

var (
	// ErrJobNotFound means the requested job is not present in the service.
	ErrJobNotFound = errors.New("netsec: job not found")

	// ErrJobExists means the requested job ID is already in use.
	ErrJobExists = errors.New("netsec: job ID already exists")

	// ErrForbidden means the authenticated actor is not allowed to act on the
	// original job.
	ErrForbidden = errors.New("netsec: actor is not permitted to access this job")

	// ErrJobCancelled means the job was cancelled and can no longer be printed
	// or reprinted.
	ErrJobCancelled = errors.New("netsec: job has been cancelled")

	// ErrJobCompleted means a completed job must be reprinted before it can be
	// printed again.
	ErrJobCompleted = errors.New("netsec: job has already completed; reprint it first")

	// ErrUnknownAttribute means an attribute was not in the parsing whitelist.
	ErrUnknownAttribute = errors.New("netsec: attribute is not allowed")

	// ErrInvalidAttribute means a whitelisted attribute had an invalid value.
	ErrInvalidAttribute = errors.New("netsec: attribute value is invalid")

	// ErrDuplicateAttribute means two aliases for the same security binding
	// were supplied with conflicting values.
	ErrDuplicateAttribute = errors.New("netsec: conflicting duplicate attribute")

	// ErrBindingMismatch means request attributes tried to change the job's
	// original submitter, queue, document URI, or content digest.
	ErrBindingMismatch = errors.New("netsec: attribute does not match the original job binding")

	// ErrURIOutsideJobStorage means a document URI is not the canonical URI for
	// the job's own isolated storage.
	ErrURIOutsideJobStorage = errors.New("netsec: document URI does not point to this job's storage")

	// ErrInvalidURI means a URI is malformed or non-canonical.
	ErrInvalidURI = errors.New("netsec: invalid document URI")

	// ErrDigestMismatch means the stored document no longer matches the digest
	// bound to the job.
	ErrDigestMismatch = errors.New("netsec: document content digest mismatch")

	// ErrDocumentTooLarge means the document exceeds the configured limit.
	ErrDocumentTooLarge = errors.New("netsec: document exceeds size limit")

	// ErrUnsafeStoragePath means a storage scope attempted to escape the
	// configured storage root.
	ErrUnsafeStoragePath = errors.New("netsec: unsafe storage path")
)
