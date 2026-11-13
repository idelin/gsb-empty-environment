// Package snapconsist provides snapshot-consistency primitives for a
// relation-based authorization store.
//
// It implements the Zanzibar-style zookie model: every write returns a
// monotonically increasing zookie that identifies a store revision. Check
// requests can carry an at_least_as_fresh zookie; a replica may only answer
// once it has applied at least that revision, otherwise it waits or forwards
// the request to the leader. A replica can never answer from a snapshot older
// than the requested freshness, which is what prevents the "new enemy"
// problem where a revoked member keeps passing checks served from a stale
// replica.
package snapconsist

import (
	"errors"
	"strconv"
	"strings"
)

// ErrInvalidZookie is returned when a token does not use the package's wire
// format. Callers must reject such requests rather than treating them as an
// absent token.
var ErrInvalidZookie = errors.New("snapconsist: invalid zookie")

// ErrFutureZookie is returned when a token names a revision that is higher
// than the leader has ever produced. Such a token can never become fresh and
// must be rejected.
var ErrFutureZookie = errors.New("snapconsist: zookie is ahead of the leader")

// ErrStaleWait is returned by a non-forwarding check when a replica cannot
// catch up to the required revision before the request context deadline.
var ErrStaleWait = errors.New("snapconsist: replica did not reach the required revision in time")

// zookiePrefix tags the wire format and lets the parser reject foreign or
// malformed tokens outright.
const zookiePrefix = "GSBZ1-"

// Zookie is an opaque, monotonically increasing freshness token. Each write
// produces exactly one zookie whose Revision is one greater than the previous
// write; a snapshot that has applied revision R contains every write whose
// zookie revision is <= R.
type Zookie struct {
	revision uint64
}

// zookieForRevision wraps an internal store revision into a token.
func zookieForRevision(revision uint64) Zookie {
	return Zookie{revision: revision}
}

// Revision exposes the numeric store revision carried by the token.
func (z Zookie) Revision() uint64 { return z.revision }

// IsZero reports whether the token names no revision.
func (z Zookie) IsZero() bool { return z.revision == 0 }

// String renders the canonical wire format, for example "GSBZ1-2f".
func (z Zookie) String() string {
	return zookiePrefix + strconv.FormatUint(z.revision, 16)
}

// ParseZookie parses a wire-format token. It rejects empty strings, foreign
// prefixes, non-canonical encodings (surplus leading zeros) and any
// non-hexadecimal payload, returning ErrInvalidZookie wrapped with the cause.
func ParseZookie(token string) (Zookie, error) {
	body, ok := strings.CutPrefix(token, zookiePrefix)
	if !ok || body == "" || body == "0" {
		return Zookie{}, ErrInvalidZookie
	}
	revision, err := strconv.ParseUint(body, 16, 64)
	if err != nil {
		return Zookie{}, errors.Join(ErrInvalidZookie, err)
	}
	canonical := strconv.FormatUint(revision, 16)
	if body != canonical {
		return Zookie{}, ErrInvalidZookie
	}
	return Zookie{revision: revision}, nil
}
