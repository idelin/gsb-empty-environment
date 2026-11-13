// Package netsec implements a tenant-safe cache for temporary credentials
// returned by an external STS.
//
// A cache entry is keyed by a canonical Identity: tenant ID, exact role ID,
// policy digest, canonical session-tag digest, external identity ID, audience,
// and credential-provider configuration generation. The key is a SHA-256 over
// fixed labels and big-endian length-prefixed field encodings.
//
// Cache entries stop being borrowed before the STS expiry by a configurable
// host safety margin. Concurrent misses for one identity share one provider
// refresh while every waiter retains independent cancellation. Existing
// leases own independent secret clones and survive entry invalidation, so an
// outstanding lease can never be rebound to a new identity.
//
// Configuration-generation changes, role revocation, and policy tightening
// invalidate entries immediately for new borrowing.
package netsec
