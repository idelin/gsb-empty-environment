// Package inspect implements version-aware object inspection with atomic
// quarantine. An object is read together with its version; the validation
// result is only applied to that exact version. If the object was updated
// or deleted in the meantime, the stale result is discarded and the new
// object is never quarantined.
package inspect

import (
	"context"
	"errors"
)

// ErrNotFound is returned when the inspected object does not exist.
var ErrNotFound = errors.New("inspect: object not found")

// Status is the health state recorded for a specific object version.
type Status int

const (
	// StatusUnknown means the current version has not been inspected yet.
	StatusUnknown Status = iota
	// StatusHealthy means the current version passed validation.
	StatusHealthy
	// StatusCorrupted means the current version failed validation and the
	// object is quarantined.
	StatusCorrupted
)

func (s Status) String() string {
	switch s {
	case StatusHealthy:
		return "healthy"
	case StatusCorrupted:
		return "corrupted"
	default:
		return "unknown"
	}
}

// Object is a snapshot of a stored object at a specific version.
type Object struct {
	Key     string
	Data    []byte
	Version uint64
}

// Backend stores objects and applies inspection results atomically.
type Backend interface {
	// Get returns the object and its current version.
	Get(ctx context.Context, key string) (Object, error)
	// SetStatusIfVersion atomically compares the stored version with
	// version and, only if they are equal, records status for it. It
	// returns false when the object is gone or its version has moved on,
	// i.e. the inspection result is stale and must be discarded.
	SetStatusIfVersion(ctx context.Context, key string, version uint64, status Status) (bool, error)
}

// Validator checks object data. A nil error means the object is healthy.
type Validator func(data []byte) error

// Inspector reads objects, validates them and records the outcome against
// the exact version that was read.
type Inspector struct {
	backend  Backend
	validate Validator
}

// NewInspector creates an Inspector storing results in backend.
func NewInspector(backend Backend, validate Validator) *Inspector {
	return &Inspector{backend: backend, validate: validate}
}

// Result describes the outcome of a single inspection pass.
type Result struct {
	Key     string
	Version uint64
	Status  Status
	// Applied is false when the object was updated or deleted after it
	// was read: the result is stale and nothing was recorded, so the
	// current object is left untouched.
	Applied bool
}

// Inspect reads key, validates the data and atomically records the result
// for the version that was read. It returns ErrNotFound if the object does
// not exist.
func (in *Inspector) Inspect(ctx context.Context, key string) (Result, error) {
	obj, err := in.backend.Get(ctx, key)
	if err != nil {
		return Result{}, err
	}

	status := StatusHealthy
	if verr := in.validate(obj.Data); verr != nil {
		status = StatusCorrupted
	}

	applied, err := in.backend.SetStatusIfVersion(ctx, key, obj.Version, status)
	if err != nil {
		return Result{}, err
	}
	return Result{Key: key, Version: obj.Version, Status: status, Applied: applied}, nil
}
