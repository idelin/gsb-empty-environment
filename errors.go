package netsec

import "errors"

var (
	// ErrEmptyTenant reports a scope without a tenant.
	ErrEmptyTenant = errors.New("netsec: tenant must not be empty")
	// ErrEmptyCollection reports a scope without a collection.
	ErrEmptyCollection = errors.New("netsec: collection must not be empty")
	// ErrEmptyField reports a scope without a field.
	ErrEmptyField = errors.New("netsec: field must not be empty")
	// ErrInvalidGeneration reports generation zero.
	ErrInvalidGeneration = errors.New("netsec: generation must be greater than zero")

	// ErrInvalidKey reports a key handle that did not provide key material.
	ErrInvalidKey = errors.New("netsec: key handle returned an empty key")

	// ErrInvalidRecord reports a malformed record ID or stored record.
	ErrInvalidRecord = errors.New("netsec: invalid record")
	// ErrRecordNotFound reports a missing registered record.
	ErrRecordNotFound = errors.New("netsec: record not found")

	// ErrGenerationOrder reports a rotation that does not advance the generation.
	ErrGenerationOrder = errors.New("netsec: next generation must be greater than current generation")
	// ErrGenerationReused reports an attempt to reuse a retired generation.
	ErrGenerationReused = errors.New("netsec: generation was already retired")
	// ErrRotationPending reports a second rotation before the previous one retires.
	ErrRotationPending = errors.New("netsec: previous generation has not retired")
	// ErrNoPreviousGeneration reports a retirement or migration with no prior generation.
	ErrNoPreviousGeneration = errors.New("netsec: no previous generation")
	// ErrGenerationInactive reports a generation that is neither current nor previous.
	ErrGenerationInactive = errors.New("netsec: generation is not active")
	// ErrRetiredGeneration reports a query using a retired generation.
	ErrRetiredGeneration = errors.New("netsec: generation has retired")

	// ErrMigrationPending reports records without a current-generation index.
	ErrMigrationPending = errors.New("netsec: records still require migration")
	// ErrAlreadyMigrated reports a migration for a record without its old index.
	ErrAlreadyMigrated = errors.New("netsec: record is already migrated")
	// ErrValueMismatch reports migration material that does not match the registered index.
	ErrValueMismatch = errors.New("netsec: migration value does not match the registered record")
	// ErrInvalidWitness reports a missing, altered, or otherwise untrusted migration witness.
	ErrInvalidWitness = errors.New("netsec: invalid migration witness")
	// ErrStaleMigration reports a witness for a different rotation or record state.
	ErrStaleMigration = errors.New("netsec: stale migration")
	// ErrMigrationConflict reports two different current indexes for one record.
	ErrMigrationConflict = errors.New("netsec: migration conflicts with the current index")
)
