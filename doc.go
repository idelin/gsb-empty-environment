// Package netsec implements a host-authenticated authorization transaction
// log for a Go file host.
//
// An online write is authorized against the caller's current credential
// generation and current policy, then recorded as Prepare, DataStable and
// Commit records.  Every record repeats the complete immutable operation
// identity (transaction, principal, credential generation, policy and
// authorization receipt digests, object number and generation, operation,
// parameter digest, old metadata digest, metadata patch and resulting
// metadata digest).  Records are chained with the previous record digest and
// HMACed with a host key.
//
// Data and metadata are staged out of band.  They become visible only after a
// complete Commit record is durable.  Recovery validates the log from the last
// trusted checkpoint, completes a Commit idempotently only when the object's
// current metadata digest is the recorded old or new digest and the object
// generation still matches, and rolls back every Prepare/DataStable-only
// transaction.  Recovery never calls the Authorizer and never resolves a
// principal name; a stale authorization is therefore never replayed under a
// new principal generation or a reused object number.
//
// A torn tail is a crash artifact and is truncated to the last complete
// record.  A complete bad HMAC, a fork, a duplicate transaction id with
// different content or an invalid receipt poisons the chain at that point:
// the trusted prefix is still reconciled, but the suffix is isolated and the
// host refuses to append or choose one branch.
package netsec
