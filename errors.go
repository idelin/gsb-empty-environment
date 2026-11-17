package netsec

import "errors"

var (
	// ErrNotFound reports a missing source path or missing destination parent.
	ErrNotFound = errors.New("netsec: path does not exist")
	// ErrExists reports that a destination already exists where one is not
	// allowed, such as create and link operations.
	ErrExists = errors.New("netsec: path already exists")
	// ErrNotDirectory reports that a path component expected to be a directory
	// is a regular file.
	ErrNotDirectory = errors.New("netsec: not a directory")
	// ErrIsDirectory reports a file-only operation applied to a directory.
	ErrIsDirectory = errors.New("netsec: is a directory")
	// ErrNotEmpty reports a policy change on a directory containing entries.
	ErrNotEmpty = errors.New("netsec: directory is not empty")
	// ErrPolicyAlreadySet reports an attempt to change an existing policy.  A
	// policy is immutable once set, even if the requested value is identical.
	ErrPolicyAlreadySet = errors.New("netsec: directory policy is already set")
	// ErrPolicyMismatch reports that directories do not belong to exactly the
	// same encryption policy.
	ErrPolicyMismatch = errors.New("netsec: encryption policy mismatch")
	// ErrPlaintextIntoEncrypted reports an attempt to place an unprotected
	// inode in a directory protected by a policy.
	ErrPlaintextIntoEncrypted = errors.New("netsec: plaintext entry cannot enter encrypted directory")
	// ErrEncryptedIntoPlaintext reports an attempt to place a protected inode
	// in a plaintext directory.
	ErrEncryptedIntoPlaintext = errors.New("netsec: encrypted entry cannot enter plaintext directory")
	// ErrHardLinkToDirectory reports an attempt to hard-link a directory.
	ErrHardLinkToDirectory = errors.New("netsec: hard links to directories are not allowed")
	// ErrInvalidPath reports an unsafe or malformed path.
	ErrInvalidPath = errors.New("netsec: invalid path")
	// ErrInvalidOperation reports a rename or exchange that would create a
	// directory cycle.
	ErrInvalidOperation = errors.New("netsec: invalid filesystem operation")
)

// Short aliases used by callers that prefer fscrypt terminology.
var (
	ErrPolicyImmutable = ErrPolicyAlreadySet
	ErrCrossPolicy     = ErrPolicyMismatch
	ErrDirectoryCycle  = ErrInvalidOperation
)
