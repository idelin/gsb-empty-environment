package proofcache

import "errors"

// Sentinel errors. Callers can use errors.Is to distinguish cache states.
var (
	// ErrNotFound means no published entry matches the requested label and
	// fingerprint.
	ErrNotFound = errors.New("proofcache: no matching proof entry")

	// ErrStale means an entry or draft was produced under an older cache
	// generation and must not be admitted under the current one.
	ErrStale = errors.New("proofcache: stale generation token")

	// ErrBatchClosed means an operation targeted a batch that has already
	// been committed or aborted.
	ErrBatchClosed = errors.New("proofcache: batch is closed")

	// ErrBatchConflict means a label already has an open batch or is already
	// a member of another open batch.
	ErrBatchConflict = errors.New("proofcache: conflicting open batch")

	// ErrBatchIncomplete means a commit was attempted before every batch
	// member had supplied a draft.
	ErrBatchIncomplete = errors.New("proofcache: batch has missing drafts")

	// ErrDependency means the transitive dependency closure failed to verify.
	ErrDependency = errors.New("proofcache: dependency closure mismatch")

	// ErrRuleVersion means a draft or entry references a rule version other
	// than the one configured on the cache.
	ErrRuleVersion = errors.New("proofcache: rule version mismatch")

	// ErrInvalidArgument means a caller supplied a malformed argument.
	ErrInvalidArgument = errors.New("proofcache: invalid argument")

	// ErrBadDigest means a digest string could not be parsed.
	ErrBadDigest = errors.New("proofcache: malformed digest")
)
