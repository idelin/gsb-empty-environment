package pagestore

import "errors"

// Distinct failure classes. Callers must be able to tell a missing
// dictionary, a corrupt dictionary, and a corrupt page apart, so each
// has its own sentinel and nothing is ever collapsed into a generic error.
var (
	// ErrPageNotFound is returned when no committed ledger binding exists
	// for the page (unknown page, deleted page, or a page whose write was
	// interrupted before the ledger record was appended).
	ErrPageNotFound = errors.New("pagestore: page not found")

	// ErrPageCorrupt is returned when a page file fails its own integrity
	// checks (bad magic, bad CRC, undecodable payload, or a header that
	// disagrees with the ledger binding).
	ErrPageCorrupt = errors.New("pagestore: page corrupt")

	// ErrDictionaryMissing is returned when the exact dictionary version
	// (ID + content digest) pinned by the page header is not present.
	// A different version of the same dictionary ID never substitutes.
	ErrDictionaryMissing = errors.New("pagestore: dictionary missing")

	// ErrDictionaryCorrupt is returned when a stored dictionary's content
	// no longer hashes to the digest it was registered under.
	ErrDictionaryCorrupt = errors.New("pagestore: dictionary corrupt")
)
