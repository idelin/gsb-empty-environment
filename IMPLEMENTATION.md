# Implementation Notes

The importable `netsec` package provides:

- `Identity.CacheKey`: fixed labels, big-endian `uint64` length prefixes, and SHA-256 over all seven identity fields.
- `SessionTagsDigest`: canonical key/value sorting before digesting.
- `Cache.Borrow`: cache hit or singleflight refresh for one canonical identity.
- `WithSafetyMargin` and `WithMaxTTL`: cache lifetime never exceeds the STS expiry and stops before the host safety margin.
- `SetGeneration`/`BumpGeneration`, `RevokeRole`/`RevokeRoleID`, and `TightenPolicy`: immediate invalidation for new borrowing.
- `Lease`: an immutable identity binding over an independent secret clone. `Release` zeroizes the clone.
- STS validation: exact tenant, role ID, audience, and tag digest; issue/expiry checks; any rejection destroys the secret handle.
- `FindDifferentialCollision`: enumerates two candidates per field and returns the shortest witness with equal weak key, different canonical identity, and different tenant or policy digest.

Tests use an observable `MemorySecret` and a controllable STS to cover:

- same exact role in different tenants
- session-tag ordering
- policy tightening and role revocation
- configuration-generation changes
- concurrent waiters with independent cancellation
- last-waiter cancellation and late-result revalidation
- failed and rejected refreshes
- equal `accessKeyID` with different secret material
