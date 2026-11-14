package netsec

import (
	"fmt"
	"time"
)

// JobStatus is the lifecycle state of a submitted job.
type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusPrinting  JobStatus = "printing"
	StatusCompleted JobStatus = "completed"
	StatusCancelled JobStatus = "cancelled"
)

// Job is the immutable security binding for one submitted print job. The
// submitter, queue, document name/URI and digest are all server-derived and
// verified again before every print. Changing any of them makes Verify fail.
type Job struct {
	ID           string
	Submitter    string
	Queue        string
	DocumentName string
	DocumentURI  string
	Digest       string
	Attributes   Attributes
	Status       JobStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// JobSpec contains the fields needed to construct a bound job. ID, Queue,
// Submitter and DocumentName must be safe single path segments; Digest must be
// a lowercase SHA-256 hex digest.
type JobSpec struct {
	ID           string
	Queue        string
	Submitter    string
	DocumentName string
	Digest       string
	Attributes   Attributes
}

// BindJob validates a JobSpec and derives the only document URI accepted for
// it. The URI is not accepted from the caller.
func BindJob(spec JobSpec) (Job, error) {
	scope := Scope{Queue: spec.Queue, Submitter: spec.Submitter, JobID: spec.ID}
	if err := scope.Validate(); err != nil {
		return Job{}, err
	}
	if err := validateDocumentName(spec.DocumentName); err != nil {
		return Job{}, err
	}
	if err := validateDigest(spec.Digest); err != nil {
		return Job{}, err
	}
	uri, err := CanonicalJobURI(scope, spec.DocumentName)
	if err != nil {
		return Job{}, err
	}

	job := Job{
		ID:           spec.ID,
		Submitter:    spec.Submitter,
		Queue:        spec.Queue,
		DocumentName: spec.DocumentName,
		DocumentURI:  uri,
		Digest:       spec.Digest,
		Attributes:   spec.Attributes.Clone(),
		Status:       StatusPending,
	}
	if err := job.Verify(); err != nil {
		return Job{}, err
	}
	return job, nil
}

// Scope returns the isolated storage namespace for the job.
func (j Job) Scope() Scope {
	return Scope{Queue: j.Queue, Submitter: j.Submitter, JobID: j.ID}
}

// Verify re-checks all security fields, including that DocumentURI is exactly
// the canonical URI for this job's own storage. A Job loaded from an untrusted
// cache or reconstructed from request attributes should always be verified
// before use.
func (j Job) Verify() error {
	scope := j.Scope()
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := validateDocumentName(j.DocumentName); err != nil {
		return err
	}
	if err := validateDigest(j.Digest); err != nil {
		return err
	}
	if err := ValidateJobURI(j.DocumentURI, scope, j.DocumentName); err != nil {
		return err
	}
	if err := j.CheckAttributes(j.Attributes); err != nil {
		return err
	}
	return nil
}

// CheckAttributes verifies that any binding attribute present in attrs agrees
// with this job's original binding. Operational attributes (copies, media,
// sides, title) are ignored here because they cannot redirect a read. A
// mismatch returns ErrBindingMismatch; a URI outside this job's storage returns
// ErrURIOutsideJobStorage; a malformed URI returns ErrInvalidURI.
func (j Job) CheckAttributes(attrs Attributes) error {
	if value, ok := attrs.Get(AttrJobID); ok && value != j.ID {
		return bindingMismatch(AttrJobID)
	}
	if value, ok := attrs.Get(AttrQueue); ok && value != j.Queue {
		return bindingMismatch(AttrQueue)
	}
	if value, ok := attrs.Get(AttrSubmitter); ok && value != j.Submitter {
		return bindingMismatch(AttrSubmitter)
	}
	if value, ok := attrs.Get(AttrDocumentName); ok && value != j.DocumentName {
		return bindingMismatch(AttrDocumentName)
	}

	for _, key := range []string{AttrJobURI, AttrDocumentURI} {
		value, ok := attrs.Get(key)
		if !ok {
			continue
		}
		scope, document, err := ParseJobURI(value)
		if err != nil {
			return err
		}
		if scope != j.Scope() || document != j.DocumentName {
			return fmt.Errorf("%w: %s", ErrURIOutsideJobStorage, key)
		}
	}

	for _, key := range []string{AttrSHA256, AttrContentDigest} {
		value, ok := attrs.Get(key)
		if ok && value != j.Digest {
			return bindingMismatch(key)
		}
	}
	return nil
}

func bindingMismatch(key string) error {
	return fmt.Errorf("%w: %s", ErrBindingMismatch, key)
}

func (j Job) clone() Job {
	j.Attributes = j.Attributes.Clone()
	return j
}
