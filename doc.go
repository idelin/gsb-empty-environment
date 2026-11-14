// Package netsec implements an importable bucket policy decision point for a
// Go object-storage gateway.
//
// A Policy is evaluated statement by statement. Each statement is constrained
// by action, resource, principal, and conditions. Explicit Deny statements take
// precedence over Allow statements. Object reads and bucket listings are bound
// to the same principal: List first authorizes the bucket operation and then
// authorizes every candidate object with the read action before exposing its
// key. Prefixes, delimiters, and continuation tokens can therefore only narrow
// a listing; they can never widen it.
//
// Policy changes are published atomically through Manager. Continuation tokens
// are signed and bound to the policy fingerprint, identity, conditions, bucket,
// prefix, and delimiter, so a token minted by one policy cannot be replayed
// after a policy change.
package netsec
