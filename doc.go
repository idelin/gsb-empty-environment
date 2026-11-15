// Package netsec admits containerd certs.d hosts.toml configurations before
// they are written to disk and performs digest-verified mirror failover.
//
// Validate checks a parsed Config against a caller Policy:
//
//   - http:// endpoints and host entries with SkipVerify are rejected unless
//     they target a loopback host and the policy permits the exception.
//   - Authorization headers are only accepted on hosts in the same domain as
//     the upstream server. Domain comparison canonicalizes host names, so
//     case, default ports and trailing dots do not create bypasses.
//   - Capability sets are normalized to the containerd pull/resolve/push
//     defaults and unknown or duplicated capabilities are rejected.
//
// PullVerified additionally enforces at runtime that resolve-only mirrors
// never serve pulls, and that digest-referenced blobs are hashed before use:
// content failing the digest check is fully discarded before the next host is
// contacted and is never returned to the caller.
package netsec
