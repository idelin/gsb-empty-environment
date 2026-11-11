// Package netsec implements a concurrency-safe interval ledger that proves
// loss ranges of monotonically numbered probe records without mistaking
// retransmissions for missing entries.
//
// Each probe boot generation owns an independent sequence space. A gap is
// confirmed missing only when a probe-issued watermark (highest generated
// sequence) covers it. Retransmissions of identical content are reported as
// duplicates; conflicting content is retained in its first form. Retransmitted
// records that repair a confirmed gap append an immutable Correction. An
// optional FullSetReference keeps the complete small-sequence delivery and
// issuance history and audits duplicate commitments, content conflicts and
// watermark boundaries.
package netsec
