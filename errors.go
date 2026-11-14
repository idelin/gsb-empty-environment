package netsec

import "errors"

var (
	// ErrInvalidConfig indicates an unusable router configuration.
	ErrInvalidConfig = errors.New("netsec: invalid config")
	// ErrInvalidJob indicates a missing or malformed job field.
	ErrInvalidJob = errors.New("netsec: invalid job")
	// ErrInvalidSource indicates a provenance value the router cannot trust.
	ErrInvalidSource = errors.New("netsec: invalid source")
	// ErrInvalidRef indicates a malformed or inconsistent ref/branch.
	ErrInvalidRef = errors.New("netsec: invalid ref or branch")
	// ErrInvalidRepository indicates a malformed repository policy.
	ErrInvalidRepository = errors.New("netsec: invalid repository")
	// ErrInvalidExecutor indicates a malformed executor registration.
	ErrInvalidExecutor = errors.New("netsec: invalid executor")
	// ErrUnknownDomain indicates an executor domain not present in the router
	// policy.
	ErrUnknownDomain = errors.New("netsec: unknown trust domain")
	// ErrPrivilegedLabel indicates a privileged label was requested without
	// privileged authorization.
	ErrPrivilegedLabel = errors.New("netsec: privileged label denied")
	// ErrPrivilegedCapability indicates a privileged capability was requested
	// without privileged authorization.
	ErrPrivilegedCapability = errors.New("netsec: privileged capability denied")
	// ErrNoExecutor indicates that no executor in the job's own domain has
	// every requested label and capability.
	ErrNoExecutor = errors.New("netsec: no matching executor in the job trust domain")
	// ErrRepositoryExists indicates a duplicate repository registration.
	ErrRepositoryExists = errors.New("netsec: repository already registered")
	// ErrRepositoryNotFound indicates a repository policy update for an
	// unknown repository.
	ErrRepositoryNotFound = errors.New("netsec: repository not found")
	// ErrExecutorExists indicates a duplicate executor registration.
	ErrExecutorExists = errors.New("netsec: executor already registered")
	// ErrExecutorNotFound indicates an update or removal for an unknown
	// executor.
	ErrExecutorNotFound = errors.New("netsec: executor not found")
	// ErrDomainChange indicates an attempt to move an executor between trust
	// domains.
	ErrDomainChange = errors.New("netsec: executor domain cannot change")
)
