// Package disclosure implements selective-disclosure verification for an
// append-only audit log.
//
// A log is a contiguous sequence of immutable leaves. Every leaf commits to a
// tenant, an inclusive event-time interval, a body commitment, and (for
// corrections and tombstones) the digest of the referenced earlier version.
// Corrections create later versions of the same logical record and may replace
// its tenant or interval. Tombstones do not create a version and never change
// an existing sequence; they only add an authorized erasure marker.
//
// A signed checkpoint commits to both the append-only log root and an index of
// effective records ordered by tenant, event start, event end, and original
// root sequence. A server returns a partial log, batched inclusion proofs, and
// a contiguous index range with adjacent boundary entries. VerifyDisclosure
// checks that every returned leaf comes from the same checkpoint, that the
// index range contains no gap, that every correction chain reaches an event
// root at that checkpoint, and that tombstones are authorized.
//
// Verified records are partitioned into Definite (the whole event interval is
// inside the query), Possible (the interval merely intersects the query), and
// Erased (an authorized tombstone applies). A withheld or erased body is
// reported through BodyKnown; it never removes the record from the result.
package disclosure
