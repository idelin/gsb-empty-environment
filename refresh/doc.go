// Package refresh implements refresh-token rotation with family-wide reuse
// detection and idempotent retries for a Go authentication service.
//
// # Security model
//
// A mobile client retries a refresh request whenever its response may have
// been lost. Every request therefore carries an idempotency key, and the
// pair (old token, idempotency key) is bound to exactly one rotation:
//
//   - Retrying with the same key replays the originally persisted result and
//     is never mistaken for token theft, even after the family has since been
//     revoked.
//   - Presenting an already rotated token with any other key is reuse and
//     revokes the whole token family in the same transaction, so a rotated
//     token can never be resurrected.
//   - A response is only returned after its transaction has durably
//     committed (persist before return).
//   - A per-family mutex plus a gap-free durable event log give refresh and
//     revocation a single total order per family.
//
// CrashableStore is an in-memory, copy-on-write implementation of Store that
// emulates atomic commit and process crashes, including the lost-response
// window between durable commit and reply.
package refresh
