package pagezip

import (
	"errors"
	"fmt"
)

// ErrDictInUse is returned when deleting a dictionary version that pages
// still reference. Old dictionaries must not be removed before the pages
// bound to them have been rewritten.
var ErrDictInUse = errors.New("pagezip: dictionary version still referenced by pages")

// ErrPageNotFound is returned when reading or deleting an unknown page.
var ErrPageNotFound = errors.New("pagezip: page not found")

// DictMissingError reports that the exact dictionary version a page header
// binds to is absent from the store. Recovery never falls back to the
// "latest" dictionary of the same ID.
type DictMissingError struct {
	ID     string
	Digest Digest
}

func (e *DictMissingError) Error() string {
	return fmt.Sprintf("pagezip: dictionary %q version %s missing", e.ID, e.Digest)
}

// DictCorruptError reports that a stored dictionary's content no longer
// hashes to the digest it is filed under.
type DictCorruptError struct {
	ID     string
	Digest Digest
}

func (e *DictCorruptError) Error() string {
	return fmt.Sprintf("pagezip: dictionary %q version %s content hash mismatch", e.ID, e.Digest)
}

// PageCorruptError reports a page file that fails structural or checksum
// validation.
type PageCorruptError struct {
	PageID string
	Reason string
}

func (e *PageCorruptError) Error() string {
	return fmt.Sprintf("pagezip: page %q corrupt: %s", e.PageID, e.Reason)
}

// IsDictMissing reports whether err is a *DictMissingError.
func IsDictMissing(err error) bool {
	_, ok := err.(*DictMissingError)
	return ok
}

// IsDictCorrupt reports whether err is a *DictCorruptError.
func IsDictCorrupt(err error) bool {
	_, ok := err.(*DictCorruptError)
	return ok
}

// IsPageCorrupt reports whether err is a *PageCorruptError.
func IsPageCorrupt(err error) bool {
	_, ok := err.(*PageCorruptError)
	return ok
}
