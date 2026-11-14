package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DigestAlgorithm is the only content digest algorithm supported by the store.
const DigestAlgorithm = "sha256"

// Digest is a lowercase hexadecimal SHA-256 content digest.
type Digest string

// ComputeDigest returns the SHA-256 digest of data.
func ComputeDigest(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest(hex.EncodeToString(sum[:]))
}

// ParseDigest validates a SHA-256 digest. An optional "sha256:" prefix and
// uppercase hexadecimal are accepted; the returned value is canonical.
func ParseDigest(value string) (Digest, error) {
	text := strings.ToLower(strings.TrimSpace(value))
	text = strings.TrimPrefix(text, "sha256:")
	text = strings.TrimPrefix(text, "sha256=")
	if len(text) != sha256.Size*2 {
		return "", fmt.Errorf("invalid sha256 digest %q: expected %d hexadecimal characters", value, sha256.Size*2)
	}
	decoded, err := hex.DecodeString(text)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("invalid sha256 digest %q", value)
	}
	return Digest(text), nil
}

func (d Digest) String() string { return string(d) }

// Algorithm returns the digest algorithm name.
func (d Digest) Algorithm() string { return DigestAlgorithm }

// Hex returns the digest without an algorithm prefix.
func (d Digest) Hex() string { return string(d) }

// Source identifies the workspace, scope, run, job and commit that produced an
// artifact, or the consumer that requests one. An empty Scope is normalized to
// the workspace name by the store.
type Source struct {
	Workspace string `json:"workspace"`
	Scope     string `json:"scope"`
	RunID     string `json:"run_id"`
	JobID     string `json:"job_id"`
	Commit    string `json:"commit"`
}

// Identity, AccessContext and Consumer are aliases for Source, which is used
// both for producers and consumers.
type Identity = Source
type AccessContext = Source
type Consumer = Source

func (s Source) normalize() Source {
	s.Workspace = strings.TrimSpace(s.Workspace)
	s.Scope = strings.TrimSpace(s.Scope)
	s.RunID = strings.TrimSpace(s.RunID)
	s.JobID = strings.TrimSpace(s.JobID)
	s.Commit = strings.TrimSpace(s.Commit)
	if s.Scope == "" {
		s.Scope = s.Workspace
	}
	return s
}

func (s Source) validate() error {
	switch {
	case s.Workspace == "":
		return fmt.Errorf("%w: workspace is required", ErrInvalidSource)
	case s.RunID == "":
		return fmt.Errorf("%w: run id is required", ErrInvalidSource)
	case s.JobID == "":
		return fmt.Errorf("%w: job id is required", ErrInvalidSource)
	case hasControl(s.Workspace), hasControl(s.Scope), hasControl(s.RunID), hasControl(s.JobID), hasControl(s.Commit):
		return fmt.Errorf("%w: identity fields must not contain control characters", ErrInvalidSource)
	default:
		return nil
	}
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// Artifact is the immutable metadata record for one named artifact.
type Artifact struct {
	Name      string    `json:"name"`
	Digest    Digest    `json:"digest"`
	Size      int64     `json:"size"`
	MediaType string    `json:"media_type,omitempty"`
	Source    Source    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// Expired reports whether the artifact's retention window has elapsed at now.
func (a Artifact) Expired(now time.Time) bool {
	return !a.ExpiresAt.IsZero() && !now.Before(a.ExpiresAt)
}

// EventKind identifies the kind of provenance event.
type EventKind string

const (
	EventUpload           EventKind = "upload"
	EventDownload         EventKind = "download"
	EventConflict         EventKind = "conflict"
	EventIntegrityFailure EventKind = "integrity_failure"
)

// Event is one entry in an artifact's provenance chain.
type Event struct {
	Kind           EventKind `json:"kind"`
	At             time.Time `json:"at"`
	Actor          Source    `json:"actor"`
	Digest         Digest    `json:"digest,omitempty"`
	ActualDigest   Digest    `json:"actual_digest,omitempty"`
	IncomingDigest Digest    `json:"incoming_digest,omitempty"`
	Note           string    `json:"note,omitempty"`
}

// Provenance is the immutable artifact metadata together with its ordered
// upload, conflict, integrity and download events.
type Provenance struct {
	Artifact Artifact `json:"artifact"`
	Events   []Event  `json:"events"`
}

// Sentinel errors. Concrete errors below wrap these values, so callers should
// use errors.Is rather than string comparisons.
var (
	// ErrNotFound indicates that no artifact is registered under the requested
	// name or digest in the requested namespace.
	ErrNotFound = errors.New("artifact not found")

	// ErrConflict indicates that a name is already bound to a different digest.
	// A conflicting upload never replaces the original binding.
	ErrConflict = errors.New("artifact name is already bound to a different digest")

	// ErrDigestMismatch indicates that bytes do not match their advertised
	// digest or that an on-disk blob was tampered with.
	ErrDigestMismatch = errors.New("artifact digest mismatch")

	// ErrExpired indicates that the artifact's retention window has elapsed.
	ErrExpired = errors.New("artifact has expired")

	// ErrForbidden is the parent of all access-control failures.
	ErrForbidden = errors.New("artifact access forbidden")

	// ErrCrossWorkspace indicates an attempt to read an artifact from a
	// different workspace.
	ErrCrossWorkspace = fmt.Errorf("%w: workspace mismatch", ErrForbidden)

	// ErrCrossRun indicates an attempt to read an artifact from a different
	// pipeline run.
	ErrCrossRun = fmt.Errorf("%w: run mismatch", ErrForbidden)

	// ErrInvalidSource indicates malformed source or consumer identity data.
	ErrInvalidSource = errors.New("invalid artifact source")

	// ErrInvalidName indicates an invalid artifact name.
	ErrInvalidName = errors.New("invalid artifact name")

	// ErrTooLarge indicates that an upload exceeded the configured size limit.
	ErrTooLarge = errors.New("artifact exceeds size limit")

	// ErrCorruptStore indicates that persisted store metadata or a content
	// addressed blob is corrupt.
	ErrCorruptStore = errors.New("artifact store is corrupt")

	// ErrAmbiguous indicates that a digest reference matches more than one
	// artifact record and a name is required to disambiguate it.
	ErrAmbiguous = errors.New("artifact reference is ambiguous")
)

// ConflictError describes a rejected same-name upload.
type ConflictError struct {
	Name           string
	ExistingDigest Digest
	NewDigest      Digest
	ExistingSource Source
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("artifact %q is already bound to digest %s from job %q in run %q; refusing digest %s",
		e.Name, e.ExistingDigest, e.ExistingSource.JobID, e.ExistingSource.RunID, e.NewDigest)
}

func (e *ConflictError) Unwrap() error { return ErrConflict }

// DigestMismatchError describes content that failed digest verification.
type DigestMismatchError struct {
	Name     string
	Expected Digest
	Actual   Digest
}

func (e *DigestMismatchError) Error() string {
	if e.Name != "" {
		return fmt.Sprintf("artifact %q digest mismatch: expected %s, got %s", e.Name, e.Expected, e.Actual)
	}
	return fmt.Sprintf("artifact digest mismatch: expected %s, got %s", e.Expected, e.Actual)
}

func (e *DigestMismatchError) Unwrap() error { return ErrDigestMismatch }

// ExpiredError describes an expired artifact access.
type ExpiredError struct {
	Name      string
	ExpiresAt time.Time
	Now       time.Time
}

func (e *ExpiredError) Error() string {
	return fmt.Sprintf("artifact %q expired at %s (now %s)", e.Name, e.ExpiresAt.Format(time.RFC3339Nano), e.Now.Format(time.RFC3339Nano))
}

func (e *ExpiredError) Unwrap() error { return ErrExpired }

// AccessError describes a rejected cross-workspace or cross-run access.
type AccessError struct {
	Name    string
	Source  Source
	Request Source
	Reason  error
}

func (e *AccessError) Error() string {
	return fmt.Sprintf("access to artifact %q denied for workspace %q run %q job %q: %v",
		e.Name, e.Request.Workspace, e.Request.RunID, e.Request.JobID, e.Reason)
}

func (e *AccessError) Unwrap() error { return e.Reason }
