// Package scrubber implements an object inspection (scrubbing) library.
//
// The inspector reads an object and captures its version, verifies the
// payload, and then marks exactly that version healthy or corrupt. If the
// object was updated or deleted between the read and the mark, the result
// is stale: the new version is never quarantined by an old inspection.
// The version comparison and the quarantine/mark action are executed
// atomically by the backend.
package scrubber

import "errors"

var (
	// ErrNotFound is returned when the key does not exist.
	ErrNotFound = errors.New("scrubber: object not found")
	// ErrQuarantined is returned by Get when the object is quarantined.
	ErrQuarantined = errors.New("scrubber: object is quarantined")
	// ErrStaleVersion is returned when the version captured at read time
	// no longer matches the current version of the object.
	ErrStaleVersion = errors.New("scrubber: stale version, result expired")
)

// Object is a versioned blob stored in a Backend.
type Object struct {
	Key     string
	Version uint64
	Data    []byte
}

// Backend stores versioned objects. Every Put creates a new version.
// The conditional mark operations compare the expected version with the
// current one and apply the state change atomically.
type Backend interface {
	// Get returns the current object including its version.
	Get(key string) (Object, error)
	// Put stores data under key, creating a fresh version, and returns
	// the stored object.
	Put(key string, data []byte) (Object, error)
	// Delete removes the object and its quarantine state.
	Delete(key string) error
	// MarkHealthyIfVersion atomically checks that the object at key is
	// still at version and, if so, records it as healthy. It returns
	// ErrStaleVersion when the object changed and ErrNotFound when it
	// is gone; in both cases nothing is marked.
	MarkHealthyIfVersion(key string, version uint64) error
	// QuarantineIfVersion atomically checks that the object at key is
	// still at version and, if so, quarantines it. It returns
	// ErrStaleVersion when the object changed and ErrNotFound when it
	// is gone; in both cases the current object is left untouched.
	QuarantineIfVersion(key string, version uint64) error
	// Quarantined reports whether key is currently quarantined.
	Quarantined(key string) bool
}
