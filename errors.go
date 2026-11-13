package netsec

import "errors"

var (
	// ErrUnauthorized means the current credential and policy did not
	// authorize the requested immutable operation.
	ErrUnauthorized = errors.New("netsec: unauthorized")

	// ErrInvalidRecord means a log record is structurally or
	// cryptographically inconsistent.
	ErrInvalidRecord = errors.New("netsec: invalid log record")

	// ErrLogCompromised means a complete record failed authentication or
	// the chain forked/was duplicated.  The untrusted suffix is isolated and
	// the host refuses to append to it.
	ErrLogCompromised = errors.New("netsec: log chain is compromised")

	// ErrTxIDExists is returned when a transaction id has already been
	// used.  Reusing an id with different content is never accepted.
	ErrTxIDExists = errors.New("netsec: transaction id already exists")

	// ErrStageOrder means Prepare, DataStable and Commit were not recorded
	// exactly once and in order.
	ErrStageOrder = errors.New("netsec: invalid transaction stage order")

	// ErrMetadataConflict means the object metadata did not have the
	// expected old digest (or an already applied new digest) during CAS.
	ErrMetadataConflict = errors.New("netsec: metadata CAS conflict")

	ErrDataConflict = errors.New("netsec: data CAS conflict")

	// ErrObjectGenerationChanged means an object number was reused with a
	// new generation.  An authorization for the old generation is never
	// migrated to the new one.
	ErrObjectGenerationChanged = errors.New("netsec: object generation changed")

	ErrObjectNotFound = errors.New("netsec: object not found")

	// ErrCheckpointMissing means the latest trusted checkpoint refers to a
	// log prefix that is no longer present.
	ErrCheckpointMissing = errors.New("netsec: trusted checkpoint is missing from log")

	ErrCheckpointInvalid = errors.New("netsec: invalid checkpoint")

	// ErrRecoveryConflict means a committed record could not be completed
	// because the object no longer matches either the old or new digest.
	ErrRecoveryConflict = errors.New("netsec: committed transaction cannot be completed")

	// ErrCommittedPending means Commit is durable but publication did not
	// finish.  A restart will complete it idempotently.
	ErrCommittedPending = errors.New("netsec: commit is durable but publication is pending")

	ErrStagingMissing = errors.New("netsec: staged data is missing")

	ErrGenerationRegression = errors.New("netsec: object generation must increase")

	ErrClosed = errors.New("netsec: host is closed")

	ErrNoAuthorizer   = errors.New("netsec: no authorizer configured")
	ErrNoObjectStore  = errors.New("netsec: no object store configured")
	ErrInvalidRequest = errors.New("netsec: invalid request")
	ErrHMACKey        = errors.New("netsec: an HMAC key is required")
)
